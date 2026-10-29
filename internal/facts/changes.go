package facts

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/filekind"
)

// Change kinds.
const (
	KindPR         = "pr"          // a pull request the user authored, on the code host
	KindMigratedPR = "migrated-pr" // a PR merged on the host a repo was migrated from: its squash commit
	KindCommit     = "commit"      // a commit outside any PR of the user's
)

// Change is one line of changes.jsonl: one piece of code work, counted once however many copies of
// its commits there are.
type Change struct {
	ID        string   `json:"id"` // "<repo>#<number>" for a PR, "<repo>@<sha, 12 characters>" for a commit
	Kind      string   `json:"kind"`
	Repo      string   `json:"repo"`
	URL       string   `json:"url"`
	Title     string   `json:"title"`
	State     string   `json:"state"`  // PRs: merged, open, closed; commits: landed, unlanded
	Landed    bool     `json:"landed"` // reached the default branch
	At        string   `json:"at"`     // when it counts, UTC: a PR's merge (else its opening), a commit's authoring
	Class     string   `json:"class"`  // who wrote it: self, agent, colleague, bot (a PR: whoever wrote most of its commits), or co-authored
	Authors   *Authors `json:"authors"`
	Lines     Lines    `json:"lines"` // landed changes only
	Keys      []string `json:"keys"`  // the issues it names that facts trusts, by current key
	Item      string   `json:"item"`
	AlsoItems []string `json:"also_items"` // the items of its other keys
	Commits   []string `json:"commits"`    // the user's own commits in it, one per patch
	// Technologies are a landed change's, by its files' paths, most lines first
	Technologies []string `json:"technologies"`

	techs    []techUse
	techDays []string
	at       time.Time // when it counts (At)
	start    time.Time // when the work began: a PR's opening, a commit's authoring
	body     string
	files    []fileChange
	evs      []event
}

// Authors counts a PR's commits by who wrote them.
type Authors struct {
	Self      int `json:"self"`
	Agent     int `json:"agent"`
	Colleague int `json:"colleague"`
	Bot       int `json:"bot"`
}

// Lines are a landed change's lines, without generated files and bulk changes (Excluded).
type Lines struct {
	Added           int `json:"added"`
	Deleted         int `json:"deleted"`
	Files           int `json:"files"`
	ExcludedAdded   int `json:"excluded_added"`
	ExcludedDeleted int `json:"excluded_deleted"`
	ExcludedFiles   int `json:"excluded_files"`
	// Living is how many of the lines the repo's default branch still has, by blame at its head: null
	// when the repo was not blamed, and for a change that only names the user as co-author
	Living *int `json:"living"`
}

// Excluded totals what line counts leave out, by reason.
type Excluded struct {
	Generated ExcludedLines `json:"generated"` // lockfiles and other generated files, by name
	Bulk      ExcludedLines `json:"bulk"`      // single file changes of more than filekind.BulkLines lines
}

type ExcludedLines struct {
	Files   int `json:"files"`
	Added   int `json:"added"`
	Deleted int `json:"deleted"`
}

type fileChange struct {
	path             string
	added, deleted   int
	generated, bulky bool
}

func newFileChange(p string, added, deleted int) fileChange {
	return fileChange{path: p, added: added, deleted: deleted, generated: filekind.Generated(p), bulky: filekind.Bulky(added, deleted)}
}

// lines totals files, keeping generated and bulk changes apart.
func (f *Facts) lines(files []fileChange) Lines {
	var l Lines
	for _, fc := range files {
		switch {
		case fc.generated:
			l.ExcludedAdded, l.ExcludedDeleted, l.ExcludedFiles = l.ExcludedAdded+fc.added, l.ExcludedDeleted+fc.deleted, l.ExcludedFiles+1
			f.Excluded.Generated = ExcludedLines{f.Excluded.Generated.Files + 1, f.Excluded.Generated.Added + fc.added, f.Excluded.Generated.Deleted + fc.deleted}
		case fc.bulky:
			l.ExcludedAdded, l.ExcludedDeleted, l.ExcludedFiles = l.ExcludedAdded+fc.added, l.ExcludedDeleted+fc.deleted, l.ExcludedFiles+1
			f.Excluded.Bulk = ExcludedLines{f.Excluded.Bulk.Files + 1, f.Excluded.Bulk.Added + fc.added, f.Excluded.Bulk.Deleted + fc.deleted}
		default:
			l.Added, l.Deleted, l.Files = l.Added+fc.added, l.Deleted+fc.deleted, l.Files+1
		}
	}
	return l
}

func hasRole(c *collect.Commit, role string) bool { return slices.Contains(c.Roles, role) }

func isMerge(c *collect.Commit) bool { return c.IsMerge || len(c.Parents) > 1 }

func commitTime(c *collect.Commit) time.Time {
	t, _ := parseTime(c.Author.Date)
	return t
}

func shortSHA(sha string) string { return sha[:min(12, len(sha))] }

// buildChanges turns each repo's commits and PRs into changes. The user's own commits are grouped by
// patch (copies made by rebasing or cherry-picking are one change); a group with a commit in one of
// the user's PRs belongs to that PR (the lowest-numbered); the other groups are changes of their own.
func (f *Facts) buildChanges() {
	byRepo := map[string][]*collect.Commit{}
	for i := range f.raw.Commits {
		c := &f.raw.Commits[i]
		byRepo[c.Repo] = append(byRepo[c.Repo], c)
	}
	prsByRepo := map[string][]*collect.PullRequestRecord{}
	for i := range f.raw.PRs {
		pr := &f.raw.PRs[i]
		prsByRepo[pr.Repo] = append(prsByRepo[pr.Repo], pr)
	}
	for _, project := range f.raw.Projects {
		f.repoChanges(project, byRepo[project.Name], prsByRepo[project.Name])
	}
	slices.SortFunc(f.Changes, func(a, b *Change) int { return cmp.Or(a.at.Compare(b.at), collect.NaturalCompare(a.ID, b.ID)) })
}

func (f *Facts) repoChanges(project collect.Project, commits []*collect.Commit, prs []*collect.PullRequestRecord) {
	repo, defaultBranch := project.Name, project.CodeHost.DefaultBranchName()

	// The user's own commits, by patch.
	groups := map[string][]*collect.Commit{}
	var groupKeys []string
	for _, c := range commits {
		if isMerge(c) || !hasRole(c, "author") {
			continue
		}
		k := "sha:" + c.SHA
		if c.PatchID != nil {
			k = "patch:" + *c.PatchID
		}
		if _, ok := groups[k]; !ok {
			groupKeys = append(groupKeys, k)
		}
		groups[k] = append(groups[k], c)
	}
	slices.Sort(groupKeys)
	prGroups := map[int][]*collect.Commit{} // PR -> the representative of each group in it
	var standalone []*collect.Commit
	for _, k := range groupKeys {
		members := groups[k]
		slices.SortFunc(members, func(a, b *collect.Commit) int {
			// the default-branch copy first, then the earliest, then by sha
			if a.OnDefaultBranch != b.OnDefaultBranch {
				if a.OnDefaultBranch {
					return -1
				}
				return 1
			}
			return cmp.Or(commitTime(a).Compare(commitTime(b)), cmp.Compare(a.SHA, b.SHA))
		})
		rep, pr, copyOnly := members[0], 0, true
		for _, c := range members {
			for _, n := range append(slices.Clone(c.InUserPRs), c.MergeCommitForUserPRs...) {
				if pr == 0 || n < pr {
					pr = n
				}
			}
			copyOnly = copyOnly && len(c.InUserPRs) == 0 && len(c.MergeCommitForUserPRs) > 0
		}
		switch {
		case pr != 0:

			if !copyOnly { // a squash commit alone is a copy of the PR's own commits, not one more commit
				for _, c := range members { // the PR's own copy stands for the group
					if slices.Contains(c.InUserPRs, pr) {
						rep = c
						break
					}
				}
				prGroups[pr] = append(prGroups[pr], rep)
			}
			for _, c := range members {
				f.changeOf[repo+"@"+c.SHA] = repo + "#" + itoa(pr)
			}
		default:
			standalone = append(standalone, rep)
			for _, c := range members {
				f.changeOf[repo+"@"+c.SHA] = repo + "@" + shortSHA(rep.SHA)
			}
		}
	}

	agentCommits := map[string]bool{} // an agent's commit in several of the user's PRs counts once
	for _, pr := range prs {
		f.addChange(f.prChange(defaultBranch, pr, prGroups[pr.Number], agentCommits))
	}
	for _, c := range standalone {
		f.addChange(f.commitChange(c))
	}
	for _, c := range commits {
		inMyPRs := len(c.InUserPRs) > 0 || len(c.MergeCommitForUserPRs) > 0 // part of a PR change
		switch {
		case isMerge(c) || hasRole(c, "author"):
		case inMyPRs: // someone else's commit in the user's PR
			pr := slices.Min(append(slices.Clone(c.InUserPRs), c.MergeCommitForUserPRs...))
			f.changeOf[repo+"@"+c.SHA] = repo + "#" + itoa(pr)
		case hasRole(c, "co-author"):
			f.changeOf[repo+"@"+c.SHA] = repo + "@" + shortSHA(c.SHA)
			f.addChange(f.coAuthoredChange(c))
		case len(c.MigratedPRNumbersInMessage) > 0 && len(c.Parents) == 1 && c.OnDefaultBranch && hasRole(c, "committer"):
			// completing someone else's PR on the earlier host
			f.emit(event{at: commitTime(c), kind: evMergedForOthers, key: repo + "@" + shortSHA(c.SHA), repo: repo})
		}
	}
}

func (f *Facts) addChange(c *Change) {
	if c == nil || f.changes[c.ID] != nil {
		return
	}
	f.changes[c.ID] = c
	f.Changes = append(f.Changes, c)
}

// trustedKeys are the issue keys of a commit that facts believes: those in its message, and those
// only its branches name if the issue existed when the commit was made (a day's grace). A branch
// holds every older commit not yet on the default branch, so its name can point at later work.
func (f *Facts) trustedKeys(c *collect.Commit) []string {
	set := map[string]bool{}
	at := commitTime(c)
	for _, k := range c.IssueKeys {
		if slices.Contains(c.BranchIssueKeys, k) {
			is := f.issues[f.currentKey(k)]
			if is == nil || is.created.IsZero() || at.Before(is.created.Add(-24*time.Hour)) {
				continue
			}
		}
		if key := f.currentKey(k); f.issues[key] != nil {
			set[key] = true
		}
	}
	return sortedSet(set)
}

// knownKeys are keys as a PR names them, by current key, for issues in the dataset.
func (f *Facts) knownKeys(keys []string) []string {
	set := map[string]bool{}
	for _, k := range keys {
		if key := f.currentKey(k); f.issues[key] != nil {
			set[key] = true
		}
	}
	return sortedSet(set)
}

func (f *Facts) prChange(defaultBranch string, pr *collect.PullRequestRecord, own []*collect.Commit, agentCommits map[string]bool) *Change {
	created, _ := parseTime(pr.CreatedAt)
	merged, isMerged := timeOf(pr.MergedAt)
	c := &Change{
		ID: pr.Repo + "#" + itoa(pr.Number), Kind: KindPR, Repo: pr.Repo, URL: pr.URL, Title: pr.Title,
		State: strings.ToLower(pr.State), Authors: &Authors{}, Keys: f.knownKeys(pr.IssueKeys), AlsoItems: []string{},
		Commits: []string{}, at: created, start: created, body: pr.Body,
	}
	if isMerged {
		c.State, c.at = "merged", merged
		c.Landed = defaultBranch == "" || pr.BaseRef == defaultBranch
	}
	c.At = c.at.Format(time.RFC3339)
	for _, pc := range pr.Commits {
		if pc.IsMerge {
			continue
		}
		switch f.people.class(deref(pc.AuthorName), deref(pc.AuthorEmail)) {
		case Self:
			c.Authors.Self++
		case Agent:
			c.Authors.Agent++
			if at, ok := parseTime(pc.AuthoredAt); ok && !agentCommits[pc.SHA] {
				agentCommits[pc.SHA] = true
				c.evs = append(c.evs, event{at: at, kind: evAgentCommit, key: pr.Repo + "@" + shortSHA(pc.SHA)})
			}
		case Bot:
			c.Authors.Bot++
		default:
			c.Authors.Colleague++
		}
	}
	c.Class = Self
	if c.Authors.Agent > c.Authors.Self && c.Authors.Agent >= c.Authors.Colleague {
		c.Class = Agent
	} else if c.Authors.Colleague > c.Authors.Self && c.Authors.Colleague > c.Authors.Agent {
		c.Class = Colleague
	}
	for _, file := range pr.Files {
		c.files = append(c.files, newFileChange(file.Path, file.Additions, file.Deletions))
	}
	c.evs = append(c.evs, event{at: created, kind: evPROpened, key: c.ID}, event{at: created, kind: evActive, key: c.ID})
	if isMerged {
		c.evs = append(c.evs, event{at: merged, kind: evPRMerged, key: c.ID})
	}
	if c.Landed {
		c.Lines = f.lines(c.files)
		c.evs = append(c.evs, event{at: merged, kind: evLanded, key: c.ID, added: c.Lines.Added, deleted: c.Lines.Deleted})
	}
	slices.SortFunc(own, func(a, b *collect.Commit) int {
		return cmp.Or(commitTime(a).Compare(commitTime(b)), cmp.Compare(a.SHA, b.SHA))
	})
	for _, oc := range own {
		c.Commits = append(c.Commits, oc.SHA)
		c.evs = append(c.evs, event{at: commitTime(oc), kind: evCommit, key: pr.Repo + "@" + shortSHA(oc.SHA)},
			event{at: commitTime(oc), kind: evActive, key: c.ID})
	}
	return c
}

func (f *Facts) commitChange(cm *collect.Commit) *Change {
	at := commitTime(cm)
	c := &Change{
		ID: cm.Repo + "@" + shortSHA(cm.SHA), Kind: KindCommit, Repo: cm.Repo, URL: cm.URL, Title: cm.Subject,
		State: "unlanded", Landed: cm.OnDefaultBranch, At: at.Format(time.RFC3339), Class: Self, Keys: f.trustedKeys(cm),
		AlsoItems: []string{}, Commits: []string{cm.SHA}, at: at, start: at, body: cm.Body,
	}
	if len(cm.MigratedPRNumbersInMessage) > 0 && len(cm.Parents) == 1 {
		c.Kind = KindMigratedPR
	}
	for _, file := range cm.Files {
		c.files = append(c.files, newFileChange(file.Path, deref(file.Additions), deref(file.Deletions)))
	}
	c.evs = append(c.evs, event{at: at, kind: evCommit, key: c.ID}, event{at: at, kind: evActive, key: c.ID})
	if c.Landed {
		c.State, c.Lines = "landed", f.lines(c.files)
		c.evs = append(c.evs, event{at: at, kind: evLanded, key: c.ID, added: c.Lines.Added, deleted: c.Lines.Deleted})
		if c.Kind == KindMigratedPR {
			c.evs = append(c.evs, event{at: at, kind: evMigratedPR, key: c.ID})
		}
	}
	return c
}

// coAuthoredChange is a commit someone (or something) else wrote that names the user as co-author:
// a colleague's squash merge including the user's work, or an agent crediting them. Its lines are
// not the user's.
func (f *Facts) coAuthoredChange(cm *collect.Commit) *Change {
	at := commitTime(cm)
	c := &Change{
		ID: cm.Repo + "@" + shortSHA(cm.SHA), Kind: KindCommit, Repo: cm.Repo, URL: cm.URL, Title: cm.Subject,
		State: "unlanded", Landed: cm.OnDefaultBranch, At: at.Format(time.RFC3339), Class: CoAuthor, Keys: f.trustedKeys(cm),
		AlsoItems: []string{}, Commits: []string{}, at: at, start: at, body: cm.Body,
	}
	if c.Landed {
		c.State = "landed"
	}
	kind := evCoAuthored
	if f.people.class(cm.Author.Name, cm.Author.Email) == Agent {
		c.Class, kind = Agent, evAgentCommit
	}
	c.evs = append(c.evs, event{at: at, kind: kind, key: c.ID})
	return c
}
