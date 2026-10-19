package collect

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/marleypowell/footprint/internal/tracker"
)

// IssueRecord is one line of <data>/issues/<project>.jsonl: an issue assigned to the user at some
// point, or named in their git work, with how it involves them.
type IssueRecord struct {
	tracker.Issue
	Roles        []string   `json:"roles"`
	ReferencedBy References `json:"referenced_by"`
}

// References are the places in the user's git work that name an issue.
type References struct {
	Commits []CommitRef `json:"commits"`
	PRs     []PRRef     `json:"prs"`
}

type CommitRef struct {
	Repo string `json:"repo"`
	SHA  string `json:"sha"`
}

type PRRef struct {
	Repo   string `json:"repo"`
	Number int    `json:"number"`
}

// UnresolvedKey is one line of <data>/issues/unresolved.json: a key that could not be loaded, why,
// and where the user's git work names it.
type UnresolvedKey struct {
	tracker.Missing
	ReferencedBy References `json:"referenced_by"`
}

// issueRoles are the ways an issue can involve the user, in the order records list them. An
// ancestor is above an issue that involves the user (its parent, up to the top) and was loaded only
// for that: the epic or initiative the work belongs to.
var issueRoles = []string{"assignee", "reporter", "creator", "commenter", "editor", "worklogger", "referenced", "ancestor"}

// maxAncestorDepth bounds the walk up the issue hierarchy, which in practice is a few levels deep.
const maxAncestorDepth = 10

// issueKeys returns the function that finds issue keys in text. With no issue tracker in the config
// it finds none.
func (h *harness) issueKeys(ctx context.Context) (tracker.KeyFunc, error) {
	if h.tools.Tracker == nil {
		return func(...string) []string { return []string{} }, nil
	}
	keys, err := h.tools.Tracker.Keys(ctx)
	if err != nil {
		return nil, trackerProjectsError(err)
	}
	return keys, nil
}

// trackerProjectsError explains a failure to list the issue tracker's projects.
func trackerProjectsError(err error) error {
	return fmt.Errorf("listing the issue tracker's projects, to recognise issue keys "+
		"(to collect without issues, remove issue_tracker from the config): %w", err)
}

// issues loads every issue ever assigned to the user, plus every issue their commits and PRs name
// (so it runs after prs and commits), plus the issues above those (ancestors), then refreshes its
// part of index.json. With no issue tracker in the config it does nothing.
func (h *harness) issues(ctx context.Context) error {
	t := h.tools.Tracker
	if t == nil {
		logf("issues: no issue tracker in the config; skipping")
		return nil
	}
	account, err := t.Account(ctx)
	if err != nil {
		return err
	}
	h.checkAccount("issues", account.Name, account.Email)
	refs, err := h.gitReferences()
	if err != nil {
		return err
	}
	assigned, err := t.Assigned(ctx, account)
	if err != nil {
		return err
	}
	isAssigned := map[string]bool{}
	wanted := map[string]bool{}
	for _, k := range assigned {
		isAssigned[strings.ToUpper(k)], wanted[strings.ToUpper(k)] = true, true
	}
	for k := range refs {
		wanted[k] = true
	}
	keys := sortedKeys(wanted)
	slices.SortFunc(keys, NaturalCompare)
	logf("issues: %d assigned to %s at some point, %d named in git work: loading %d", len(assigned), account.Name, len(refs), len(keys))
	loaded, missing, err := t.Issues(ctx, keys)
	if err != nil {
		return err
	}
	if len(loaded) == 0 && len(missing) > 0 { // the tracker is failing, not the user's history empty
		return fmt.Errorf("the tracker loaded none of the %d issues (%s: %s %s); keeping the previous issues/",
			len(keys), missing[0].Key, missing[0].Reason, missing[0].Error)
	}

	ancestors, gone, err := loadAncestors(ctx, t, loaded)
	if err != nil {
		return err
	}
	missing = append(missing, gone...)

	byProject := map[string][]IssueRecord{}
	add := func(issue tracker.Issue, ancestor bool) {
		rec := IssueRecord{Issue: issue}
		var known []string
		for _, k := range append([]string{issue.Key}, issue.PreviousKeys...) {
			k = strings.ToUpper(k)
			if isAssigned[k] {
				known = append(known, "assignee")
			}
			if r := refs[k]; r != nil {
				known = append(known, "referenced")
				rec.ReferencedBy.Commits = append(rec.ReferencedBy.Commits, r.Commits...)
				rec.ReferencedBy.PRs = append(rec.ReferencedBy.PRs, r.PRs...)
			}
		}
		if ancestor {
			known = append(known, "ancestor")
		}
		rec.ReferencedBy = rec.ReferencedBy.normalized()
		rec.Roles = issueInvolvement(&rec.Issue, account.ID, known...)
		byProject[issue.Project.Key] = append(byProject[issue.Project.Key], rec)
	}
	for _, issue := range loaded {
		add(issue, false)
	}
	for _, issue := range ancestors {
		add(issue, true)
	}
	dir := filepath.Join(h.raw, "issues")
	files := map[string]bool{}
	for project, rows := range byProject {
		slices.SortFunc(rows, func(a, b IssueRecord) int { return NaturalCompare(a.Key, b.Key) })
		name := fileName(project) + ".jsonl"
		if err := writeJSONL(filepath.Join(dir, name), rows); err != nil {
			return err
		}
		files[name] = true
	}
	if err := removeOtherJSONL(dir, files); err != nil {
		return err
	}
	unresolved := []UnresolvedKey{}
	reasons := map[string]int{}
	for _, m := range missing {
		u := UnresolvedKey{Missing: m, ReferencedBy: References{}.normalized()}
		if r := refs[strings.ToUpper(m.Key)]; r != nil {
			u.ReferencedBy = r.normalized()
		}
		unresolved = append(unresolved, u)
		reasons[m.Reason]++
	}
	slices.SortFunc(unresolved, func(a, b UnresolvedKey) int { return NaturalCompare(a.Key, b.Key) })
	if err := writeJSON(filepath.Join(dir, "unresolved.json"), unresolved); err != nil {
		return err
	}
	logf("issues: %d issues and %d ancestors in %d projects; %d keys not loaded %v (issues/unresolved.json)",
		len(loaded), len(ancestors), len(byProject), len(unresolved), reasons)
	groups, err := indexGroups(dir, "issues/", issueRoles, issueGroup)
	if err != nil {
		return err
	}
	return h.refreshIndex(func(index *Index) {
		index.IssuesByProject = groups
		index.Totals.Issues, index.Totals.IssueAncestors = issueTotals(groups)
	})
}

// loadAncestors loads the parents of issues that are not among them, then their parents, up to the
// top of the hierarchy. Each key is asked for once; the keys that fail come back as missing.
func loadAncestors(ctx context.Context, t tracker.Tracker, issues []tracker.Issue) ([]tracker.Issue, []tracker.Missing, error) {
	asked, loaded := map[string]bool{}, map[string]bool{} // keys asked for or answered to; current keys loaded
	answer := func(issue tracker.Issue) {
		loaded[strings.ToUpper(issue.Key)] = true
		for _, k := range append([]string{issue.Key}, issue.PreviousKeys...) {
			asked[strings.ToUpper(k)] = true
		}
	}
	for _, issue := range issues {
		answer(issue)
	}
	var ancestors []tracker.Issue
	missing := []tracker.Missing{}
	for depth, level := 0, issues; depth < maxAncestorDepth && len(level) > 0; depth++ {
		var keys []string
		for _, issue := range level {
			if p := issue.Parent; p != nil && !asked[strings.ToUpper(p.Key)] {
				asked[strings.ToUpper(p.Key)] = true
				keys = append(keys, strings.ToUpper(p.Key))
			}
		}
		if len(keys) == 0 {
			break
		}
		slices.SortFunc(keys, NaturalCompare)
		logf("issues: loading %d ancestors, %d levels up", len(keys), depth+1)
		got, gone, err := t.Issues(ctx, keys)
		if err != nil {
			return nil, nil, err
		}
		level = nil
		for _, issue := range got {
			if loaded[strings.ToUpper(issue.Key)] { // the same issue, under a key it had before
				continue
			}
			answer(issue)
			ancestors, level = append(ancestors, issue), append(level, issue)
		}
		missing = append(missing, gone...)
	}
	return ancestors, missing, nil
}

// issueTotals counts the issues involving the user, and apart from them the ancestors loaded only
// for context, from the groups indexGroups made.
func issueTotals(groups []IndexGroup) (issues, ancestors int) {
	for _, g := range groups {
		n := 0
		if v, ok := g.ByRole.Get("ancestor"); ok {
			n, _ = v.(int) // indexGroups counts in ints
		}
		issues, ancestors = issues+g.Records-n, ancestors+n
	}
	return issues, ancestors
}

// issueInvolvement marks the user's comments, changes and worklogs, and returns the user's roles:
// those the issue shows, plus known, the roles found elsewhere ("assignee" from the tracker's
// search, "referenced", "ancestor").
func issueInvolvement(issue *tracker.Issue, userID string, known ...string) []string {
	isUser := func(u *tracker.User) bool { return u != nil && u.ID == userID }
	has := map[string]bool{"assignee": isUser(issue.Assignee), "reporter": isUser(issue.Reporter), "creator": isUser(issue.Creator)}
	for _, role := range known {
		has[role] = true
	}
	for i := range issue.Comments {
		c := &issue.Comments[i]
		c.ByUser = isUser(c.Author)
		has["commenter"] = has["commenter"] || c.ByUser
	}
	for i := range issue.History {
		ch := &issue.History[i]
		ch.ByUser = isUser(ch.Author)
		has["editor"] = has["editor"] || ch.ByUser
		for _, item := range ch.Items {
			if item.Field == "assignee" && deref(item.ToID) == userID {
				has["assignee"] = true
			}
		}
	}
	for i := range issue.Worklogs {
		w := &issue.Worklogs[i]
		w.ByUser = isUser(w.Author)
		has["worklogger"] = has["worklogger"] || w.ByUser
	}
	roles := []string{}
	for _, r := range issueRoles {
		if has[r] {
			roles = append(roles, r)
		}
	}
	return roles
}

// gitReferences maps each issue key that the user's commits and PRs name to where they name it.
func (h *harness) gitReferences() (map[string]*References, error) {
	refs := map[string]*References{}
	at := func(key string) *References {
		key = strings.ToUpper(key)
		if refs[key] == nil {
			refs[key] = &References{}
		}
		return refs[key]
	}
	entries, err := os.ReadDir(filepath.Join(h.raw, "repos"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		dir := filepath.Join(h.raw, "repos", e.Name())
		commits, err := readJSONL[Commit](filepath.Join(dir, "commits.jsonl"))
		if err != nil {
			return nil, err
		}
		for _, c := range commits {
			for _, k := range c.IssueKeys {
				r := at(k)
				r.Commits = append(r.Commits, CommitRef{Repo: c.Repo, SHA: c.SHA})
			}
		}
		prs, err := readJSONL[PullRequestRecord](filepath.Join(dir, "prs.jsonl"))
		if err != nil {
			return nil, err
		}
		for _, p := range prs {
			for _, k := range p.IssueKeys {
				r := at(k)
				r.PRs = append(r.PRs, PRRef{Repo: p.Repo, Number: p.Number})
			}
		}
	}
	return refs, nil
}

// normalized sorts and de-duplicates the references, with empty lists rather than null.
func (r References) normalized() References {
	commits := slices.Clone(r.Commits)
	slices.SortFunc(commits, func(a, b CommitRef) int { return cmp.Or(cmp.Compare(a.Repo, b.Repo), cmp.Compare(a.SHA, b.SHA)) })
	prs := slices.Clone(r.PRs)
	slices.SortFunc(prs, func(a, b PRRef) int { return cmp.Or(cmp.Compare(a.Repo, b.Repo), cmp.Compare(a.Number, b.Number)) })
	return References{Commits: orEmpty(slices.Compact(commits)), PRs: orEmpty(slices.Compact(prs))}
}

// NaturalCompare orders strings with runs of digits compared as numbers, so issue keys sort as
// ENG-9 before ENG-10 whatever the tracker's key format.
func NaturalCompare(a, b string) int {
	x, y := a, b
	for x != "" && y != "" {
		if isDigit(x[0]) && isDigit(y[0]) {
			nx, ny := digitRun(x), digitRun(y)
			tx, ty := strings.TrimLeft(x[:nx], "0"), strings.TrimLeft(y[:ny], "0")
			if c := cmp.Or(cmp.Compare(len(tx), len(ty)), cmp.Compare(tx, ty)); c != 0 {
				return c
			}
			x, y = x[nx:], y[ny:]
			continue
		}
		if x[0] != y[0] {
			return cmp.Compare(x[0], y[0])
		}
		x, y = x[1:], y[1:]
	}
	return cmp.Or(cmp.Compare(len(x), len(y)), strings.Compare(a, b))
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func digitRun(s string) int {
	n := 0
	for n < len(s) && isDigit(s[n]) {
		n++
	}
	return n
}

// checkAccount warns when a token belongs to someone the config does not describe: the email the
// tool reports is not one of identity.emails.
func (h *harness) checkAccount(stage, name, email string) {
	if email != "" && !h.id.emails[strings.ToLower(strings.TrimSpace(email))] {
		logf("%s: WARNING: the token belongs to %s <%s>, which is not in identity.emails; is this the right account?", stage, name, email)
	}
}

var (
	unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9_.~-]`)
	// Windows device names, reserved whatever the extension.
	reservedFileName = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[0-9]|lpt[0-9])(\.|$)`)
)

// fileName turns a project or space key into a file name that stays inside its directory, and
// is the same, and valid, on every OS.
func fileName(key string) string {
	name := unsafeFileChars.ReplaceAllString(key, "_")
	if name == "" || strings.HasPrefix(name, ".") || reservedFileName.MatchString(name) {
		name = "_" + name
	}
	return name
}

// removeOtherJSONL deletes the .jsonl files in dir that a stage did not write this run.
func removeOtherJSONL(dir string, keep map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") && !keep[e.Name()] {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
