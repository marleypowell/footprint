// Package doctor says whether a collected dataset is complete, and what to run or change when it is
// not. It reads the dataset, the run state in the cache and, when there is one, the config. It calls
// no tools and reads no token values, so it works on a copy of a dataset too.
package doctor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/envfile"
	"github.com/marleypowell/footprint/internal/tracker"
)

// Status is a check's outcome. Only Fail makes the dataset incomplete.
type Status string

const (
	Pass Status = "pass"
	Warn Status = "warn"
	Fail Status = "fail"
	Skip Status = "skip"
)

// Check is one finding, with what to run or change.
type Check struct {
	ID      string   `json:"id"`
	Status  Status   `json:"status"`
	Summary string   `json:"summary"`
	Details []string `json:"details,omitempty"`
	Fix     string   `json:"fix,omitempty"`
	// Snippet is config to add, for the user to confirm first.
	Snippet string `json:"snippet,omitempty"`
}

// Report is every check, in a fixed order.
type Report struct {
	Checks []Check `json:"checks"`
}

// Failed says whether any check failed.
func (r Report) Failed() bool {
	return slices.ContainsFunc(r.Checks, func(c Check) bool { return c.Status == Fail })
}

// WriteText writes one line per check, with its details, fix and snippet indented below it.
func (r Report) WriteText(w io.Writer) error {
	var b strings.Builder
	for _, c := range r.Checks {
		fmt.Fprintf(&b, "%-4s %s: %s\n", strings.ToUpper(string(c.Status)), c.ID, c.Summary)
		for _, d := range c.Details {
			fmt.Fprintf(&b, "     - %s\n", d)
		}
		if c.Fix != "" {
			fmt.Fprintf(&b, "     fix: %s\n", c.Fix)
		}
		if c.Snippet != "" {
			b.WriteString("     in config.json, once the user confirms:\n")
			for _, line := range strings.Split(c.Snippet, "\n") {
				fmt.Fprintf(&b, "       %s\n", line)
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// EnvNeed is a variable a configured tool authenticates with: any one of AnyOf will do.
type EnvNeed struct {
	Tool  string
	AnyOf []string
}

// Inputs say where the dataset is and what the run is configured with.
type Inputs struct {
	DataDir  string
	CacheDir string
	Config   *collect.Config // nil: the identity and tools the dataset recorded
	EnvFile  string          // the .env file, checked for who can read it; "" when there is none
	Env      []EnvNeed
	IsSet    func(name string) bool // nil: set and not empty in the environment
	Now      time.Time
	MaxAge   time.Duration // older data is a warning; 0 means 30 days
}

// Run reads the dataset and reports on it. An error means a file could not be read or decoded.
func Run(in Inputs) (Report, error) {
	if in.IsSet == nil {
		in.IsSet = func(name string) bool { return os.Getenv(name) != "" }
	}
	if in.MaxAge == 0 {
		in.MaxAge = 30 * 24 * time.Hour
	}
	d := &dataset{in: in}
	ok, err := readJSON(filepath.Join(in.DataDir, "index.json"), &d.index)
	if err != nil {
		return Report{}, err
	}
	if !ok {
		return Report{Checks: []Check{{ID: "dataset", Status: Fail,
			Summary: "no dataset in " + in.DataDir + " (no index.json)", Fix: "footprint all"}}}, nil
	}
	d.identity, d.login = d.index.Identity, d.index.Login
	d.tracker, d.wiki = d.index.Sources.IssueTracker != nil, d.index.Sources.Wiki != nil
	if c := in.Config; c != nil {
		d.identity, d.login = c.Identity, c.Login
		d.tracker, d.wiki = c.IssueTracker != nil, c.Wiki != nil
	}
	var r Report
	for _, check := range []func() (Check, error){d.verify, d.identities, d.reviews, d.issues, d.pages, d.mirror, d.stale, d.env} {
		c, err := check()
		if err != nil {
			return Report{}, err
		}
		r.Checks = append(r.Checks, c)
	}
	return r, nil
}

type dataset struct {
	in       Inputs
	index    collect.Index
	identity collect.IdentityConfig
	login    string
	tracker  bool
	wiki     bool
}

func (d *dataset) raw(parts ...string) string {
	return filepath.Join(append([]string{d.in.DataDir}, parts...)...)
}

var nameEmailRE = regexp.MustCompile(`^(.*?)\s*<([^>]*)>$`)

// verify: every default-branch commit the code host credits to the account is in the dataset.
func (d *dataset) verify() (Check, error) {
	c := Check{ID: "verify"}
	var v collect.Verification
	ok, err := readJSON(d.raw("org", "verification.json"), &v)
	if err != nil || !ok {
		c.Status, c.Summary, c.Fix = Fail, "the dataset was never compared with the code host's attribution", "footprint verify"
		return c, err
	}
	if v.AttributedMissingFromDataset == 0 {
		c.Status = Pass
		c.Summary = fmt.Sprintf("all %d commits the code host credits to the account are in the dataset", v.HostAttributedCommits)
		return c, nil
	}
	type author struct {
		name, email string
		commits     int
		repos       map[string]bool
	}
	byEmail := map[string]*author{}
	for _, m := range v.Missing {
		name, email := m.Author, ""
		if ne := nameEmailRE.FindStringSubmatch(m.Author); ne != nil {
			name, email = ne[1], ne[2]
		}
		k := strings.ToLower(email)
		if byEmail[k] == nil {
			byEmail[k] = &author{name: name, email: email, repos: map[string]bool{}}
		}
		byEmail[k].commits++
		byEmail[k].repos[m.Repo] = true
	}
	authors := make([]*author, 0, len(byEmail))
	for _, a := range byEmail {
		authors = append(authors, a)
	}
	sort.Slice(authors, func(i, j int) bool {
		if authors[i].commits != authors[j].commits {
			return authors[i].commits > authors[j].commits
		}
		return strings.ToLower(authors[i].email) < strings.ToLower(authors[j].email)
	})
	var add []string
	unfetched := map[string]bool{}
	for _, a := range authors {
		repos := sortedKeys(a.repos)
		where := fmt.Sprintf("%s <%s>: %d commits in %s", a.name, a.email, a.commits, strings.Join(repos, ", "))
		if hasFold(d.identity.Emails, a.email) || a.email == "" {
			c.Details = append(c.Details, where+"; the email is configured, so the mirrors or the commits stage missed them")
			for _, r := range repos {
				unfetched[r] = true
			}
			continue
		}
		c.Details = append(c.Details, where+"; the email is not in identity.emails")
		add = append(add, a.email)
	}
	c.Status = Fail
	c.Summary = fmt.Sprintf("%d of the %d commits the code host credits to the account are missing from the dataset",
		v.AttributedMissingFromDataset, v.HostAttributedCommits)
	var fixes []string
	if len(add) > 0 {
		fixes = append(fixes, "add the emails below to identity.emails (the code host already credits their commits to the account)")
		c.Snippet = listSnippet("emails", append(slices.Clone(d.identity.Emails), add...))
	}
	if len(unfetched) > 0 {
		fixes = append(fixes, "footprint mirror -only "+strings.Join(sortedKeys(unfetched), ","))
	}
	c.Fix = strings.Join(append(fixes, "footprint commits projects verify issues"), ", then ")
	return c, nil
}

// identities: every identity resembling the user is configured as them, or dismissed as someone
// else. Only the user can say which, so this warns and never fails.
func (d *dataset) identities() (Check, error) {
	c := Check{ID: "identities"}
	var rep collect.IdentitiesReport
	ok, err := readJSON(d.raw("org", "identities.json"), &rep)
	if err != nil || !ok {
		c.Status, c.Summary, c.Fix = Fail, "no identities report", "footprint identities"
		return c, err
	}
	var likely, others []string
	var add []string
	for _, e := range rep.Identities {
		if e.MatchedByConfig != nil || d.configured(e.Name, e.Email) || hasFold(d.identity.NotMe, e.Email) || hasFold(d.identity.NotMe, e.Name) {
			continue
		}
		line := fmt.Sprintf("%s <%s>: author %d, committer %d, co-author %d, in %d repos", e.Name, e.Email, e.Author, e.Committer, e.CoAuthor, len(e.Repos))
		if why := resemblance(e.Name, e.Email, d.identity, d.login); why != "" {
			likely = append(likely, line+"; "+why)
			if e.Email != "" && !slices.Contains(add, e.Email) {
				add = append(add, e.Email)
			}
			continue
		}
		others = append(others, line)
	}
	n := len(likely) + len(others)
	if n == 0 {
		c.Status = Pass
		c.Summary = fmt.Sprintf("every identity resembling the user (%d) is configured or dismissed", len(rep.Identities))
		return c, nil
	}
	c.Status = Warn
	c.Summary = fmt.Sprintf("%d identities resembling the user are neither configured nor dismissed: confirm with the user whether each is them", n)
	c.Details = likely
	const shown = 10
	for i, o := range others {
		if i == shown {
			c.Details = append(c.Details, fmt.Sprintf("and %d more", len(others)-shown))
			break
		}
		c.Details = append(c.Details, o)
	}
	c.Fix = "add the user's to identity.emails (or a full name to identity.names) and run footprint commits projects verify issues; " +
		"add everyone else's email to identity.not_me"
	if len(add) > 0 {
		c.Snippet = listSnippet("emails", append(slices.Clone(d.identity.Emails), add...))
	}
	return c, nil
}

// configured says whether the identity config matches a (name, email) pair now, which it may do
// when the config changed after the identities report.
func (d *dataset) configured(name, email string) bool {
	return email != "" && hasFold(d.identity.Emails, email) || name != "" && hasFold(d.identity.Names, name)
}

// resemblance says why a (name, email) pair looks like the user's, or "": an email with the same
// local part as one of theirs, an address naming their login, or the same name in another form.
func resemblance(name, email string, id collect.IdentityConfig, login string) string {
	local := strings.ToLower(email)
	if i := strings.LastIndex(local, "@"); i >= 0 {
		local = local[:i]
	}
	if local != "" {
		for _, e := range id.Emails {
			theirs := strings.ToLower(e)
			if i := strings.LastIndex(theirs, "@"); i >= 0 && theirs[:i] == local {
				return "the same local part as " + e
			}
		}
		if login != "" {
			for _, tok := range strings.FieldsFunc(local, func(r rune) bool { return strings.ContainsRune("+._-", r) }) {
				if tok == strings.ToLower(login) {
					return "the address names the login " + login
				}
			}
		}
	}
	words := nameWords(name)
	for _, n := range id.Names {
		theirs := nameWords(n)
		if len(words) == 0 || len(theirs) == 0 {
			continue
		}
		if slices.Equal(sortedCopy(words), sortedCopy(theirs)) {
			return "the same name as " + n + ", but for case, punctuation or order"
		}
		a, b := words, theirs
		if a[len(a)-1] == b[len(b)-1] && (len(a[0]) == 1 || len(b[0]) == 1) && a[0][0] == b[0][0] {
			return "the same name as " + n + ", with an initial"
		}
	}
	return ""
}

func nameWords(name string) []string {
	return strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// issues: the issue keys the user's work names were all loaded, or are typos and deleted issues.
func (d *dataset) issues() (Check, error) {
	c := Check{ID: "issues"}
	if !d.tracker {
		c.Status, c.Summary = Skip, "no issue tracker configured"
		return c, nil
	}
	var keys []collect.UnresolvedKey
	ok, err := readJSON(d.raw("issues", "unresolved.json"), &keys)
	if err != nil || !ok {
		c.Status, c.Summary, c.Fix = Fail, "issues were never collected", "footprint issues"
		return c, err
	}
	by := map[string][]collect.UnresolvedKey{}
	for _, k := range keys {
		by[k.Reason] = append(by[k.Reason], k)
	}
	list := func(ks []collect.UnresolvedKey) string {
		names := make([]string, 0, len(ks))
		for _, k := range ks {
			names = append(names, k.Key)
		}
		return strings.Join(names, ", ")
	}
	c.Status = Pass
	c.Summary = "every issue key the user's work names was loaded"
	if nf := by[tracker.NotFound]; len(nf) > 0 {
		c.Summary = fmt.Sprintf("%d keys were not found: typos or deleted issues, unless many are of one project the token cannot see", len(nf))
		c.Details = append(c.Details, "not found: "+list(nf))
	}
	if h := by[tracker.Hidden]; len(h) > 0 {
		c.Status = Warn
		c.Summary = fmt.Sprintf("the token may not see %d issues", len(h))
		c.Details = append(c.Details, "hidden: "+list(h))
		c.Fix = "a token of an account that can see them, then footprint issues"
	}
	if f := by[tracker.Failed]; len(f) > 0 {
		c.Status = Fail
		c.Summary = fmt.Sprintf("%d issues failed to load", len(f))
		c.Details = append(c.Details, "failed: "+list(f), "first error: "+truncate(f[0].Error, 300))
		c.Fix = "footprint issues"
	}
	return c, nil
}

// reviews: the reviews stage ran, so that no reviews reads as none rather than unknown.
func (d *dataset) reviews() (Check, error) {
	c := Check{ID: "reviews"}
	var s collect.ReviewsSummary
	ok, err := readJSON(d.raw("org", "reviews.json"), &s)
	if err != nil || !ok {
		c.Status, c.Summary, c.Fix = Warn, "reviews of other people's PRs were never collected", "footprint reviews projects"
		return c, err
	}
	c.Status = Pass
	c.Summary = fmt.Sprintf("%d PRs by others reviewed or commented on, in %d repos", s.PRs, s.Repos)
	return c, nil
}

// pages: with a wiki configured, the pages stage ran.
func (d *dataset) pages() (Check, error) {
	c := Check{ID: "pages"}
	if !d.wiki {
		c.Status, c.Summary = Skip, "no wiki configured"
		return c, nil
	}
	entries, err := os.ReadDir(d.raw("wiki"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return c, err
	}
	if len(entries) == 0 && len(d.index.WikiBySpace) == 0 {
		c.Status, c.Summary, c.Fix = Warn, "wiki pages were never collected (or the user wrote none)", "footprint pages"
		return c, nil
	}
	c.Status, c.Summary = Pass, fmt.Sprintf("wiki content from %d spaces", len(d.index.WikiBySpace))
	return c, nil
}

// mirror: every non-empty repo in the inventory has a mirror that fetched cleanly.
func (d *dataset) mirror() (Check, error) {
	c := Check{ID: "mirror"}
	status := map[string]collect.MirrorStatus{}
	ok, err := readJSON(filepath.Join(d.in.CacheDir, "mirror_status.json"), &status)
	if err != nil || !ok {
		c.Status, c.Summary = Skip, "no mirror state in "+d.in.CacheDir+" (a copied dataset, or never mirrored)"
		return c, err
	}
	var repos []collect.Repo
	if _, err := readJSON(d.raw("org", "repos.json"), &repos); err != nil {
		return c, err
	}
	var failed, never []string
	for _, r := range repos {
		s, ok := status[r.Name]
		switch {
		case r.IsEmpty:
		case !ok:
			never = append(never, r.Name)
		case !s.OK:
			failed = append(failed, r.Name)
			c.Details = append(c.Details, r.Name+": "+truncate(s.Error, 200))
		}
	}
	slices.Sort(failed)
	slices.Sort(never)
	switch {
	case len(failed) > 0:
		c.Status = Fail
		c.Summary = fmt.Sprintf("%d repos failed to mirror", len(failed))
		c.Fix = "footprint mirror -only " + strings.Join(failed, ",") + ", then footprint commits projects verify issues"
	case len(never) > 0:
		c.Status = Warn
		c.Summary = fmt.Sprintf("%d non-empty repos in the inventory were never mirrored", len(never))
		c.Details = []string{strings.Join(never, ", ")}
		c.Fix = "footprint mirror identities commits projects verify issues"
	default:
		c.Status = Pass
		c.Summary = fmt.Sprintf("%d repos mirrored", len(status))
	}
	return c, nil
}

// stale: no stage's output is older than what it was made from. Each finding names the stages to
// re-run; the fix runs them all, in the order `all` does.
func (d *dataset) stale() (Check, error) {
	c := Check{ID: "stale"}
	rerun := map[string]bool{}
	found := func(detail string, stages ...string) {
		stages = slices.DeleteFunc(stages, func(s string) bool { return s == "issues" && !d.tracker || s == "pages" && !d.wiki })
		c.Details = append(c.Details, detail+" (re-run "+strings.Join(stages, " ")+")")
		for _, s := range stages {
			rerun[s] = true
		}
	}
	if d.in.Config != nil {
		if !sameIdentity(d.in.Config.Identity, d.index.Identity) {
			found("the config's identity changed after the dataset was collected", "commits", "projects", "verify", "issues")
		}
		var rep collect.IdentitiesReport
		ok, err := readJSON(d.raw("org", "identities.json"), &rep)
		if err != nil {
			return c, err
		}
		if ok && !sameIdentity(d.in.Config.Identity, rep.Configured) {
			found("the config's identity changed after the identities report", "identities")
		}
	}
	commits, err := newest(d.raw("repos"), "commits.jsonl")
	if err != nil {
		return c, err
	}
	prs, err := newest(d.raw("repos"), "prs.jsonl")
	if err != nil {
		return c, err
	}
	var v collect.Verification
	if ok, err := readJSON(d.raw("org", "verification.json"), &v); err != nil {
		return c, err
	} else if ok && after(commits, v.GeneratedAt) {
		found("commits were collected after verify ran", "verify")
	}
	if d.tracker {
		info, err := os.Stat(d.raw("issues", "unresolved.json"))
		if err == nil && (laterThan(commits, info.ModTime()) || laterThan(prs, info.ModTime())) {
			found("commits or PRs were collected after issues ran, so issues may miss keys they name", "issues")
		}
	}
	status := map[string]collect.MirrorStatus{}
	if _, err := readJSON(filepath.Join(d.in.CacheDir, "mirror_status.json"), &status); err != nil {
		return c, err
	}
	fetched := ""
	for _, s := range status {
		fetched = max(fetched, s.FetchedAt)
	}
	if fetchedAt, err := time.Parse(time.RFC3339, fetched); err == nil && laterThan(fetchedAt, commits) {
		found("the mirrors were fetched after commits ran", "identities", "commits", "projects", "verify", "issues")
	}
	last := fetched
	if last == "" {
		last = d.index.GeneratedAt
	}
	if t, err := time.Parse(time.RFC3339, last); err == nil && d.in.Now.Sub(t) > d.in.MaxAge {
		found(fmt.Sprintf("the data was last collected %d days ago", int(d.in.Now.Sub(t).Hours()/24)),
			"mirror", "prs", "reviews", "commits", "projects", "verify", "issues", "pages")
	}
	if len(c.Details) == 0 {
		c.Status, c.Summary = Pass, "every stage ran after the stages it reads"
		return c, nil
	}
	var stages []string
	for _, s := range collect.StageOrder {
		if rerun[s] {
			stages = append(stages, s)
		}
	}
	c.Status, c.Summary = Warn, "some output is older than what it was made from"
	c.Fix = "footprint " + strings.Join(stages, " ")
	return c, nil
}

// env: the variables the configured tools authenticate with are set (only their names are read),
// and the .env file holding them is the user's alone.
func (d *dataset) env() (Check, error) {
	c := Check{ID: "env"}
	for _, need := range d.in.Env {
		if !slices.ContainsFunc(need.AnyOf, d.in.IsSet) {
			c.Details = append(c.Details, fmt.Sprintf("%s needs %s", need.Tool, strings.Join(need.AnyOf, " or ")))
		}
	}
	var fixes []string
	if len(c.Details) > 0 {
		fixes = append(fixes, "set them in .env (see .env.example) or the environment; collecting stops without them")
	}
	if d.in.EnvFile != "" && envfile.TooOpen(d.in.EnvFile) {
		c.Details = append(c.Details, d.in.EnvFile+" holds tokens and other users can read it")
		fixes = append(fixes, "chmod 600 "+d.in.EnvFile)
	}
	if len(c.Details) == 0 {
		c.Status, c.Summary = Pass, "the variables the configured tools authenticate with are set"
		if len(d.in.Env) == 0 {
			c.Summary = "no configured tool needs a variable"
		}
		return c, nil
	}
	c.Status, c.Summary, c.Fix = Warn, "collecting again needs attention to the environment", strings.Join(fixes, "; ")
	return c, nil
}

// sameIdentity compares what decides who the user is: emails, names and the discovery pattern.
func sameIdentity(a, b collect.IdentityConfig) bool {
	norm := func(s []string) []string {
		out := make([]string, 0, len(s))
		for _, v := range s {
			out = append(out, strings.ToLower(strings.TrimSpace(v)))
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	return slices.Equal(norm(a.Emails), norm(b.Emails)) && slices.Equal(norm(a.Names), norm(b.Names)) &&
		a.DiscoveryPattern == b.DiscoveryPattern
}

// newest is the latest modification time of a file of this name in any repo directory.
func newest(dir, name string) (time.Time, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	var t time.Time
	for _, e := range entries {
		info, err := os.Stat(filepath.Join(dir, e.Name(), name))
		if err == nil && info.ModTime().After(t) {
			t = info.ModTime()
		}
	}
	return t, nil
}

// laterThan says whether a is after b, to the second (the dataset's timestamps have no fraction).
func laterThan(a, b time.Time) bool {
	return !a.IsZero() && !b.IsZero() && a.Truncate(time.Second).After(b.Truncate(time.Second))
}

func after(t time.Time, stamp string) bool {
	s, err := time.Parse(time.RFC3339, stamp)
	return err == nil && laterThan(t, s)
}

func listSnippet(key string, values []string) string {
	b, _ := json.MarshalIndent(values, "", "  ")
	return fmt.Sprintf("%q: %s", key, b)
}

func readJSON(path string, v any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("decoding %s: %w", path, err)
	}
	return true, nil
}

func hasFold(list []string, s string) bool {
	s = strings.TrimSpace(s)
	return slices.ContainsFunc(list, func(v string) bool { return strings.EqualFold(strings.TrimSpace(v), s) })
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
