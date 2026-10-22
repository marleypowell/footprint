package facts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/codehost"
	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/tracker"
	"github.com/marleypowell/footprint/internal/wiki"
)

func ptr[T any](v T) *T { return &v }

var (
	jane    = collect.Signature{Name: "Jane Doe", Email: "jane@example.com"}
	other   = collect.Signature{Name: "Other Person", Email: "other@example.com"}
	uJane   = &tracker.User{ID: "u-jane", Name: "Jane Doe"}
	uOther  = &tracker.User{ID: "u-other", Name: "Other Person"}
	wJane   = &wiki.User{ID: "w-jane", Name: "Jane Doe"}
	wOther  = &wiki.User{ID: "w-other", Name: "Other Person"}
	project = tracker.ProjectRef{Key: "ENG", Name: "Engineering"}
)

func sig(s collect.Signature, date string) collect.Signature { s.Date = date; return s }

func file(path string, added, deleted int) collect.FileChange {
	return collect.FileChange{Path: path, Status: "M", Additions: &added, Deletions: &deleted}
}

// commit is a non-merge commit by author (committed by them too), with the user's roles.
func commit(sha, date string, author collect.Signature, roles []string, message string, mod func(*collect.Commit)) collect.Commit {
	c := collect.Commit{
		Repo: "app", SHA: sha, URL: "https://code.example/org/app/commit/" + sha, Roles: roles,
		Author: sig(author, date), Committer: sig(author, date), Subject: strings.SplitN(message, "\n", 2)[0], Message: message,
		Parents: []string{"parent"}, OnDefaultBranch: true, InUserPRs: []int{}, MergeCommitForUserPRs: []int{},
		PRNumbersInMessage: []int{}, MigratedPRNumbersInMessage: []int{}, IssueKeys: []string{}, BranchIssueKeys: []string{},
		PatchID: ptr("patch-" + sha), Stats: &collect.CommitStats{}, Files: []collect.FileChange{file("src/"+sha+".go", 1, 0)},
	}
	if mod != nil {
		mod(&c)
	}
	return c
}

// fixture is one repo's worth of work, exercising every counting rule.
func fixture() *Raw {
	mainBranch := "main"
	me := []string{"author", "committer"}
	commits := []collect.Commit{
		commit("c1", "2024-01-02T10:00:00+01:00", jane, me, "feat(ENG-1): start", func(c *collect.Commit) {
			c.IssueKeys = []string{"ENG-1"}
			c.Files = []collect.FileChange{file("src/a.go", 10, 2)}
		}),
		// c2 is a cherry-picked copy, on main, of c3 in PR 5: one change, the PR's
		commit("c2", "2024-01-03T10:00:00Z", jane, me, "fix b", func(c *collect.Commit) { c.PatchID = ptr("patch-c3") }),
		commit("c3", "2024-01-03T10:00:00Z", jane, me, "fix b", func(c *collect.Commit) { c.OnDefaultBranch, c.InUserPRs = false, []int{5} }),
		commit("c4", "2024-01-04T10:00:00Z", jane, me, "more b", func(c *collect.Commit) { c.OnDefaultBranch, c.InUserPRs = false, []int{5} }),
		// c5 is PR 5's squash commit: a copy of c3 and c4, not one more commit
		commit("c5", "2024-01-05T10:00:00Z", jane, []string{"author"}, "ENG-1: feature (#5)", func(c *collect.Commit) { c.MergeCommitForUserPRs = []int{5} }),
		// completing a colleague's PR on the host the repo came from
		commit("c6", "2024-01-06T10:00:00Z", other, []string{"committer"}, "Merged PR 77: theirs", func(c *collect.Commit) {
			c.Committer, c.MigratedPRNumbersInMessage = sig(jane, "2024-01-06T10:00:00Z"), []int{77}
		}),
		// the user's own PR there: generated and bulk files stay out of the lines
		commit("c7", "2024-02-01T10:00:00Z", jane, me, "Merged PR 88: Fix ENG-2", func(c *collect.Commit) {
			c.MigratedPRNumbersInMessage, c.IssueKeys = []int{88}, []string{"ENG-2"}
			c.Files = []collect.FileChange{file("package-lock.json", 5000, 10), file("b.go", 20, 0), file("fixtures/big.json", 6000, 0)}
		}),
		commit("c8", "2024-02-02T10:00:00Z", other, []string{"co-author"}, "squash\n\nCo-authored-by: Jane Doe <jane@example.com>", nil),
		commit("c9", "2024-01-04T11:00:00Z", collect.Signature{Name: "Claude", Email: "noreply@anthropic.com"}, []string{"pr-author"},
			"agent work", func(c *collect.Commit) { c.OnDefaultBranch, c.InUserPRs = false, []int{5} }),
		commit("c10", "2024-01-05T10:00:00Z", jane, me, "Merge pull request #6", func(c *collect.Commit) {
			c.Parents, c.IsMerge, c.PatchID, c.Stats, c.Files = []string{"a", "b"}, true, nil, nil, nil
		}),
		// a branch named after ENG-3 holds a commit older than ENG-3: not work on it
		commit("c11", "2024-03-01T10:00:00Z", jane, me, "wip", func(c *collect.Commit) {
			c.OnDefaultBranch, c.Branches, c.IssueKeys, c.BranchIssueKeys = false, []string{"feature/ENG-3"}, []string{"ENG-3"}, []string{"ENG-3"}
		}),
		commit("c12", "2024-03-02T10:00:00Z", jane, me, "polish", func(c *collect.Commit) {
			c.OnDefaultBranch, c.Branches, c.IssueKeys, c.BranchIssueKeys = false, []string{"feature/ENG-1"}, []string{"ENG-1"}, []string{"ENG-1"}
		}),
	}
	pr := func(n int, title, state, created string, merged *string, base string, keys []string, mod func(*codehost.PullRequest)) collect.PullRequestRecord {
		p := codehost.PullRequest{Repo: "app", Number: n, URL: "https://code.example/org/app/pull/" + itoa(n), Title: title,
			Body: "Does the thing for the users who asked.", State: state, CreatedAt: created, MergedAt: merged, BaseRef: base,
			Labels: []string{}, Assignees: []string{}, Reviews: []codehost.Review{}}
		if mod != nil {
			mod(&p)
		}
		return collect.PullRequestRecord{PullRequest: p, IssueKeys: keys}
	}
	prs := []collect.PullRequestRecord{
		pr(5, "ENG-1: feature", "MERGED", "2024-01-03T09:00:00Z", ptr("2024-01-05T10:00:00Z"), "main", []string{"ENG-1", "ENG-4"},
			func(p *codehost.PullRequest) {
				p.Files = []codehost.PRFile{{Path: "src/x.go", Additions: 30, Deletions: 5}, {Path: "yarn.lock", Additions: 100, Deletions: 100}}
				p.Commits = []codehost.PRCommit{
					{SHA: "c3", AuthoredAt: "2024-01-03T10:00:00Z", AuthorName: ptr("Jane Doe"), AuthorEmail: ptr("jane@example.com")},
					{SHA: "c4", AuthoredAt: "2024-01-04T10:00:00Z", AuthorName: ptr("Jane Doe"), AuthorEmail: ptr("jane@example.com")},
					{SHA: "c9", AuthoredAt: "2024-01-04T11:00:00Z", AuthorName: ptr("Claude"), AuthorEmail: ptr("noreply@anthropic.com")},
				}
			}),
		pr(6, "stacked", "MERGED", "2024-04-01T09:00:00Z", ptr("2024-04-02T10:00:00Z"), "feature-base", []string{}, nil),
		pr(7, "draft idea", "OPEN", "2024-05-01T09:00:00Z", nil, "main", []string{}, nil),
	}
	status := func(id, at string, by *tracker.User, to string) tracker.Change {
		return tracker.Change{ID: id, Author: by, CreatedAt: at, ByUser: by == uJane, Items: []tracker.ChangeItem{{Field: "status", To: ptr(to)}}}
	}
	issue := func(key string, level int, typ string, parent string, mod func(*collect.IssueRecord)) collect.IssueRecord {
		r := collect.IssueRecord{Issue: tracker.Issue{Key: key, ID: "id-" + key, URL: "https://tracker.example/browse/" + key, PreviousKeys: []string{},
			Project: project, Type: typ, Level: level, Summary: "Summary of " + key, Status: "To Do", StatusCategory: "todo",
			CreatedAt: "2023-12-01T00:00:00+00:00", Subtasks: []tracker.IssueRef{}, Comments: []tracker.Comment{}, History: []tracker.Change{},
			Worklogs: []tracker.Worklog{}}, Roles: []string{}}
		if parent != "" {
			r.Parent = &tracker.IssueRef{Key: parent}
		}
		if mod != nil {
			mod(&r)
		}
		return r
	}
	issues := []collect.IssueRecord{
		issue("ENG-10", tracker.EpicLevel, "Epic", "", func(r *collect.IssueRecord) { r.Summary, r.Roles = "Search", []string{"ancestor"} }),
		issue("ENG-1", tracker.StandardLevel, "Story", "ENG-10", func(r *collect.IssueRecord) {
			r.Summary, r.Status, r.StatusCategory, r.Assignee, r.Roles = "Fast search", "Done", "done", uJane, []string{"assignee", "editor", "referenced"}
			r.Description = "Make search fast."
			r.Subtasks = []tracker.IssueRef{{Key: "ENG-4"}, {Key: "ENG-6"}}
			r.History = []tracker.Change{status("h1", "2024-01-06T00:00:00+00:00", uJane, "Done")}
			r.Sprints = []tracker.Sprint{
				{Name: "Search Squad 2023 Sprint 26", StartAt: ptr("2023-12-18T09:00:00Z"), EndAt: ptr("2023-12-29T17:00:00Z")},
				{Name: "Search Squad CY24 Sprint 1", StartAt: ptr("2024-01-01T09:00:00Z"), EndAt: ptr("2024-01-12T17:00:00Z"), CompletedAt: ptr("2024-01-06T00:00:00Z")},
			}
		}),
		// ENG-4: finished by someone else, but the user was its assignee when it was resolved
		issue("ENG-4", tracker.SubtaskLevel, "Sub-task", "ENG-1", func(r *collect.IssueRecord) {
			r.Status, r.StatusCategory, r.Assignee, r.Roles = "Done", "done", uOther, []string{"assignee", "commenter", "referenced"}
			r.History = []tracker.Change{
				{ID: "h2", Author: uOther, CreatedAt: "2024-01-02T00:00:00+00:00", Items: []tracker.ChangeItem{{Field: "assignee", ToID: ptr("u-jane")}}},
				status("h3", "2024-01-07T00:00:00+00:00", uOther, "Done"),
				{ID: "h4", Author: uOther, CreatedAt: "2024-01-08T00:00:00+00:00", Items: []tracker.ChangeItem{{Field: "assignee", FromID: ptr("u-jane"), ToID: ptr("u-other")}}},
			}
			r.Comments = []tracker.Comment{{ID: "k1", Author: uJane, CreatedAt: "2024-01-03T12:00:00+00:00", Body: "Indexed it.", ByUser: true}}
			r.Sprints = []tracker.Sprint{{Name: "Search Squad 2024 - Sprint 1", StartAt: ptr("2024-01-01T09:00:00Z"), EndAt: ptr("2024-01-12T17:00:00Z")}}
		}),
		issue("ENG-2", tracker.StandardLevel, "Bug", "", func(r *collect.IssueRecord) {
			r.Reporter, r.Creator, r.Roles = uJane, uJane, []string{"reporter", "creator", "referenced"}
			r.CreatedAt, r.Team = "2024-01-20T00:00:00+00:00", ptr("Platform")
		}),
		issue("ENG-3", tracker.StandardLevel, "Task", "", func(r *collect.IssueRecord) {
			r.Assignee, r.Roles, r.CreatedAt = uJane, []string{"assignee", "referenced"}, "2024-06-01T00:00:00+00:00"
		}),
	}
	pages := []collect.PageRecord{
		{Page: wiki.Page{ID: "100", Type: "page", Title: "Search design", URL: "https://wiki.example/100", Space: wiki.Space{Key: "ENG", Name: "Engineering"},
			Ancestors: []wiki.PageRef{}, Labels: []string{"design"}, CreatedAt: "2024-01-02T00:00:00+00:00", CreatedBy: wJane,
			Versions: []wiki.Version{{Number: 1, Author: wJane, CreatedAt: "2024-01-02T00:00:00+00:00", ByUser: true},
				{Number: 2, Author: wJane, CreatedAt: "2024-01-09T00:00:00+00:00", ByUser: true}},
			Body: ptr(strings.Repeat("word ", 350) + "See [jira: key=ENG-1].")},
			Roles: []string{"creator", "editor"}, UserVersions: 2, IssueKeys: []string{"ENG-1"}},
		{Page: wiki.Page{ID: "101", Type: "page", Title: "Team page", URL: "https://wiki.example/101", Space: wiki.Space{Key: "ENG", Name: "Engineering"},
			Ancestors: []wiki.PageRef{}, Labels: []string{}, CreatedAt: "2023-01-01T00:00:00+00:00", CreatedBy: wOther,
			Versions: []wiki.Version{{Number: 1, Author: wOther}, {Number: 2, Author: wOther}, {Number: 3, Author: wJane, CreatedAt: "2024-02-01T00:00:00+00:00", ByUser: true},
				{Number: 4, Author: wOther}},
			Body: ptr(strings.Repeat("word ", 300))},
			Roles: []string{"editor"}, UserVersions: 1, IssueKeys: []string{}},
	}
	claude := collect.Signature{Name: "Claude", Email: "noreply@anthropic.com"}
	version := func(sha, status, path string, author collect.Signature, date string, roles ...string) collect.DecisionVersion {
		v := collect.DecisionVersion{SHA: sha, Path: path, Status: status[:1], Author: sig(author, date), Subject: "docs", Roles: append([]string{}, roles...)}
		if len(status) > 1 {
			v.Similarity = ptr(100)
		}
		return v
	}
	decisions := []collect.Decision{
		// the user's, in c1 and by their agent in PR 5, edited by a colleague; renamed since, which is
		// not a version
		{Repo: "app", Path: "docs/decisions/0001-use-queues.md", URL: "https://code.example/org/app/blob/main/docs/decisions/0001-use-queues.md",
			Title: "Use queues", IssueKeys: []string{"ENG-2"}, CreatedAt: "2024-01-02T10:00:00+01:00", Body: strings.Repeat("word ", 220) + "For ENG-2.",
			Versions: []collect.DecisionVersion{
				version("c1", "A", "docs/decisions/001-use-queues.md", jane, "2024-01-02T10:00:00+01:00", "author", "committer"),
				version("c9", "M", "docs/decisions/001-use-queues.md", claude, "2024-01-04T11:00:00Z", "pr-author"),
				version("o1", "M", "docs/decisions/001-use-queues.md", other, "2024-03-01T10:00:00Z"),
				version("c11", "R100", "docs/decisions/01-use-queues.md", jane, "2024-03-01T10:00:00Z", "author", "committer"),
				version("o3", "R100", "docs/decisions/0001-use-queues.md", other, "2024-03-05T10:00:00Z"),
			}},
		// a colleague's, that the user touched once
		{Repo: "app", Path: "docs/decisions/0002-theirs.md", URL: "https://code.example/org/app/blob/main/docs/decisions/0002-theirs.md",
			Title: "Theirs", IssueKeys: []string{}, CreatedAt: "2023-11-01T10:00:00Z", Body: strings.Repeat("word ", 400),
			Versions: []collect.DecisionVersion{
				version("o2", "A", "docs/decisions/0002-theirs.md", other, "2023-11-01T10:00:00Z"),
				version("c7", "M", "docs/decisions/0002-theirs.md", jane, "2024-02-01T10:00:00Z", "author", "committer"),
			}},
	}
	return &Raw{
		Index: collect.Index{Identity: collect.IdentityConfig{Emails: []string{"jane@example.com"}, Names: []string{"Jane Doe"}},
			Sources: collect.Sources{CodeHost: "fakehost", Catalog: "fakecatalog", IssueTracker: ptr("faketracker"), Wiki: ptr("fakewiki")},
			Repos:   []collect.IndexRepo{{Name: "app", URL: "https://code.example/org/app", FirstCommitInRepo: ptr("2024-01-02T09:00:00Z")}}},
		Projects: []collect.Project{{Name: "app", URL: "https://code.example/org/app", CodeHost: codehost.Repo{Name: "app", DefaultBranch: &mainBranch}}},
		Commits:  commits, PRs: prs, Decisions: decisions, Issues: issues, Pages: pages,
		IssueProjects: []tracker.Project{{Key: "ENG", Name: "Engineering", PreviousKeys: []string{"OLD"}}},
	}
}

func build(t *testing.T, raw *Raw, in Inputs) *Facts {
	t.Helper()
	f, err := Build(raw, in)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func check(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", what, got, want)
	}
}

func TestCountingRules(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	ids := map[string]*Change{}
	for _, c := range f.Changes {
		ids[c.ID] = c
	}
	var got []string
	for _, c := range f.Changes {
		got = append(got, c.ID)
	}
	// c2 folds into PR 5 (its cherry-picked original is there), c5 is PR 5's squash copy, c6 is
	// someone else's PR, c9 is inside PR 5, c10 is a merge.
	check(t, "changes, by when they count", got, []string{"app@c1", "app#5", "app@c7", "app@c8", "app@c11", "app@c12", "app#6", "app#7"})
	pr := ids["app#5"]
	check(t, "PR: landed, by merge time", []any{pr.Kind, pr.State, pr.Landed, pr.At}, []any{KindPR, "merged", true, "2024-01-05T10:00:00Z"})
	check(t, "PR: own commits, one per patch", pr.Commits, []string{"c3", "c4"})
	check(t, "PR: authors", *pr.Authors, Authors{Self: 2, Agent: 1})
	check(t, "PR: lines without the lockfile", pr.Lines, Lines{Added: 30, Deleted: 5, Files: 1, ExcludedAdded: 100, ExcludedDeleted: 100, ExcludedFiles: 1})
	check(t, "PR: keys, and the item of the strongest", []any{pr.Keys, pr.Item, pr.AlsoItems}, []any{[]string{"ENG-1", "ENG-4"}, "ENG-1", []string{}})
	check(t, "stacked PR: merged, not landed", []any{ids["app#6"].State, ids["app#6"].Landed}, []any{"merged", false})
	m := ids["app@c7"]
	check(t, "migrated PR", []any{m.Kind, m.Landed, m.Keys, m.Item}, []any{KindMigratedPR, true, []string{"ENG-2"}, "ENG-2"})
	check(t, "migrated PR: generated and bulk files left out", m.Lines, Lines{Added: 20, Files: 1, ExcludedAdded: 11000, ExcludedDeleted: 10, ExcludedFiles: 2})
	check(t, "co-authored", []any{ids["app@c8"].Class, ids["app@c8"].Lines}, []any{CoAuthor, Lines{}})
	check(t, "branch key older than its issue: not trusted", ids["app@c11"].Keys, []string{})
	check(t, "branch key of an issue that existed", []any{ids["app@c12"].Keys, ids["app@c12"].Landed}, []any{[]string{"ENG-1"}, false})
	check(t, "unticketed work goes to a bucket by the quarter it began", []any{ids["app@c11"].Item, ids["app#6"].Item}, []any{"repo+app+2024q1", "repo+app+2024q2"})
	check(t, "excluded", f.Excluded, Excluded{Generated: ExcludedLines{Files: 2, Added: 5100, Deleted: 110}, Bulk: ExcludedLines{Files: 1, Added: 6000}})

	all := totalsOf(f.events, All)
	check(t, "totals", all, Totals{
		ActiveDays: all.ActiveDays, ChangesLanded: 3, PRsOpened: 3, PRsMerged: 2, MigratedPRsMerged: 1, MergedForOthers: 1,
		Commits: 6, CommitsCoAuthored: 1, AgentCommits: 1, LinesAdded: 60, LinesDeleted: 7, Repos: 1, IssuesDone: 2, IssuesWorked: 3,
		IssuesRaised: 1, PagesCreated: 1, PagesEdited: 2, DocsWritten: 2, ADRsWritten: 1, Items: all.Items,
	})

	// Every event counts toward one item, or none: items (initiatives aside) add up to the totals.
	var sum Totals
	for _, it := range f.Items {
		if it.Kind != KindInitiative {
			t := totalsOf(it.events, All)
			sum.Commits, sum.PRsMerged, sum.LinesAdded = sum.Commits+t.Commits, sum.PRsMerged+t.PRsMerged, sum.LinesAdded+t.LinesAdded
		}
	}
	check(t, "items add up", []int{sum.Commits, sum.PRsMerged, sum.LinesAdded}, []int{all.Commits, all.PRsMerged, all.LinesAdded})
}

func TestIssues(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	is := func(key string) *Issue { return f.issues[key] }
	check(t, "the user's tracker account, from their marked activity", f.userID, "u-jane")
	check(t, "done: made the final transition", []any{is("ENG-1").DoneByMe, is("ENG-1").Involvement}, []any{true, Done})
	check(t, "done: assignee at resolution, reassigned since", []any{is("ENG-4").DoneByMe, is("ENG-4").Involvement}, []any{true, Done})
	check(t, "raised, and named by a change", is("ENG-2").Involvement, Worked)
	check(t, "assigned, named only by an older branch", []any{is("ENG-3").Involvement, is("ENG-3").Item}, []any{Assigned, ""})
	check(t, "ancestor", is("ENG-10").Involvement, Ancestor)
	check(t, "sub-task belongs to its story", is("ENG-4").Item, "ENG-1")

	it := f.items["ENG-1"]
	check(t, "feature", []any{it.Kind, it.Title, it.Initiative, it.Issues}, []any{KindFeature, "Fast search", &Ref{ID: "ENG-10", Title: "Search"}, []string{"ENG-1", "ENG-4"}})
	check(t, "feature sub-tasks", *it.Subtasks, Subtasks{Total: 2, DoneByMe: 1})
	check(t, "feature changes", []any{it.Changes, it.Docs}, []any{[]string{"app@c1", "app#5", "app@c12"}, []string{"doc+100", "adr+app+0001-use-queues"}})
	ini := f.items["ENG-10"]
	check(t, "initiative", []any{ini.Kind, ini.Features, ini.Involvement}, []any{KindInitiative, []string{"ENG-1"}, "contributed"})
	check(t, "initiative totals include its features'", ini.Totals.PRsMerged, it.Totals.PRsMerged)
	if _, ok := f.items["ENG-3"]; ok {
		t.Error("an issue the user only had assigned made an item")
	}
}

// Team signals: the board of the sprint that held each issue when the user worked on it, the
// issues' projects, and their team field, by quarter.
func TestTeamSignals(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	check(t, "signals", f.teamSignals(All), []TeamSignals{{
		Period:   "2024q1",
		Boards:   []Signal{{Name: "Search Squad", Issues: 2}},
		Projects: []Signal{{Name: "ENG", Title: "Engineering", Issues: 3}},
		Teams:    []Signal{{Name: "Platform", Issues: 1}},
	}})
	w, _ := NewWindow("2024-04-01", "2024-06-30")
	check(t, "a window with no issue work", f.teamSignals(w), []TeamSignals{})
	for name, want := range map[string]string{
		"Checkout 2020 Sprint 10": "Checkout", "Checkout CY22 Sprint 23": "Checkout", "Web Team CY23 TO 2": "Web Team",
		"Checkout 2021 - Sprint 5": "Checkout", "Payments Sprint 1": "Payments", "Payments Archive": "Payments Archive",
		"Sprint 12": "Sprint 12", "Cycle 3": "Cycle 3",
	} {
		if got := boardOf(name); got != want {
			t.Errorf("boardOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestDocs(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	d, e := f.docs["100"], f.docs["101"]
	check(t, "written", []any{d.Share, d.Words >= substantialWords, d.Substantial, d.Item, d.Items}, []any{100, true, true, "doc+100", []string{"ENG-1"}})
	check(t, "edited once", []any{e.Share, e.Substantial, e.Item}, []any{25, false, ""})

	a, b := f.docs["app/docs/decisions/0001-use-queues.md"], f.docs["app/docs/decisions/0002-theirs.md"]
	check(t, "ADR: the user's commit and their agent's", []any{a.Ref, a.Type, a.CreatedAt, a.CreatedByMe, a.MyVersions, a.Versions, a.Share},
		[]any{"adr+app+0001-use-queues", "adr", "2024-01-02T09:00:00Z", true, 2, 3, 66})
	check(t, "ADR: substantial at its own length", []any{a.Words < substantialWords, a.Substantial, a.Item}, []any{true, true, "adr+app+0001-use-queues"})
	check(t, "ADR: the changes that wrote it (not the rename), and theirs and its issues' items", []any{a.Changes, a.Items, a.First, a.Last},
		[]any{[]string{"app@c1", "app#5"}, []string{"ENG-1", "ENG-2"}, "2024-01-02", "2024-01-04"})
	check(t, "ADR: a colleague's", []any{b.CreatedByMe, b.Share, b.Substantial, b.Item, b.Changes}, []any{false, 50, false, "", []string{"app@c7"}})
	it := f.items[a.Ref]
	check(t, "ADR item", []any{it.Kind, it.Type, it.AlsoChanges, it.ActiveDays}, []any{KindDoc, "adr", []string{"app@c1", "app#5"}, 2})
	pack := string(renderPack(it.pack, it.Pack))
	for _, want := range []string{"## The decision record", "- app: docs/decisions/0001-use-queues.md", "created by you; 2 of 3 commits yours",
		"- written in app#5 · ENG-1: feature"} {
		if !strings.Contains(pack, want) {
			t.Errorf("ADR pack has no %q:\n%s", want, pack)
		}
	}
	if pack := string(renderPack(f.items["ENG-1"].pack, f.items["ENG-1"].Pack)); !strings.Contains(pack, "- adr+app+0001-use-queues · Use queues") {
		t.Errorf("a feature's pack should name the ADR written in its changes:\n%s", pack)
	}
	if pack := string(renderPack(f.items["repo+app+2024q1"].pack, "")); strings.Contains(pack, "adr+app") {
		t.Errorf("renaming an ADR is not writing it:\n%s", pack)
	}
	agentPR := &packInput{Doc: &packDoc{Repo: "app", Changes: []packDocChange{{ID: "app#9", Class: Agent, Title: "docs: an ADR"}}}}
	if pack := string(renderPack(agentPR, "")); !strings.Contains(pack, "- written in app#9 (agent) · docs: an ADR") {
		t.Errorf("an ADR's pack should say who wrote the changes it was written in:\n%s", pack)
	}
	check(t, "refs: by path where file names clash", (&Facts{raw: &Raw{Decisions: []collect.Decision{
		{Repo: "web", Path: "apps/a/docs/adr/0001-x.md"}, {Repo: "web", Path: "apps/b/docs/adr/0001-x.md"}, {Repo: "a+b", Path: "docs/adr/0001-x.md"},
	}}}).decisionRefs(), []string{"adr+web+apps%2Fa%2Fdocs%2Fadr%2F0001-x", "adr+web+apps%2Fb%2Fdocs%2Fadr%2F0001-x", "adr+a%2Bb+0001-x"})
}

func TestEffortAndIDs(t *testing.T) {
	for days, want := range map[int]string{0: "XS", 1: "XS", 2: "S", 3: "S", 4: "M", 10: "M", 11: "L", 30: "L", 31: "XL"} {
		if got := effortOf(days); got != want {
			t.Errorf("effortOf(%d) = %s, want %s", days, got, want)
		}
	}
	at, _ := parseTime("2024-12-31T23:30:00-02:00") // 2025-01-01 in UTC
	check(t, "bucket ids are path-safe and UTC", []string{bucketID(".github-private", at), bucketID("a+b", at), itemPath("doc+x/y z")},
		[]string{"repo+.github-private+2025q1", "repo+a%2Bb+2025q1", "doc+x%2Fy%20z"})
}

func TestWindows(t *testing.T) {
	w, err := ParseWindow("2024-01-01..2024-01-31")
	if err != nil {
		t.Fatal(err)
	}
	in, _ := parseTime("2024-01-31T23:59:59Z")
	out, _ := parseTime("2024-02-01T00:00:00Z")
	check(t, "both days included, half-open", []bool{w.Contains(in), w.Contains(out)}, []bool{true, false})
	check(t, "names", []string{w.String(), w.Dir(), All.String()}, []string{"2024-01-01..2024-01-31", "2024-01-01_2024-01-31", "all"})
	if _, err := ParseWindow("2024-02-01..2024-01-01"); err == nil {
		t.Error("want an error for a window that ends before it starts")
	}
	f := build(t, fixture(), Inputs{})
	s := f.Summary(w)
	check(t, "window totals", []int{s.Totals.PRsMerged, s.Totals.Commits, s.Totals.MigratedPRsMerged}, []int{1, 3, 0})
	for _, it := range f.windowItems(w) {
		if it.ID == "repo+app+2024q2" {
			t.Error("an item with nothing in the window is in its view")
		}
	}
}

func TestNotes(t *testing.T) {
	dir := t.TempDir()
	f := build(t, fixture(), Inputs{})
	pack := f.items["ENG-1"].Pack
	note := func(item, inputs string, sections ...string) string {
		var b strings.Builder
		b.WriteString("---\nitem: " + item + "\ninputs: " + inputs + "\n---\n")
		for _, s := range sections {
			b.WriteString("\n## " + s + "\n\nText.\n")
		}
		return b.String()
	}
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("ENG-1.md", note("ENG-1", pack, NoteSections...))
	write("ENG-10.md", note("ENG-10", "sha256:old", NoteSections...))
	write("ENG-2.md", note("ENG-2", "x", "Summary"))
	write("GONE-1.md", note("GONE-1", "x", NoteSections...))
	f = build(t, fixture(), Inputs{NotesDir: dir})
	states := map[string]string{}
	for _, it := range f.Items {
		states[it.ID] = it.Note
	}
	check(t, "note states", []string{states["ENG-1"], states["ENG-10"], states["ENG-2"], states["doc+100"], states["adr+app+0001-use-queues"]},
		[]string{NoteCurrent, NoteStale, NoteInvalid, NoteMissing, NoteMissing})
	check(t, "orphans", f.orphans, []string{"GONE-1"})
	todo := string(f.todo(All))
	if !strings.Contains(todo, "stale\tENG-10\t") || !strings.Contains(todo, "invalid\tENG-2\t") || !strings.Contains(todo, "orphan\tGONE-1.md") ||
		strings.Contains(todo, "\tENG-1\t") {
		t.Errorf("todo:\n%s", todo)
	}

	// The user's corrections join the pack, and so its hash: the note goes stale.
	ctx := filepath.Join(t.TempDir(), "items")
	if err := os.MkdirAll(ctx, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ctx, "ENG-1.md"), []byte("It cut search time for everyone."), 0o644); err != nil {
		t.Fatal(err)
	}
	g := build(t, fixture(), Inputs{NotesDir: dir, ContextDir: filepath.Dir(ctx)})
	if g.items["ENG-1"].Note != NoteStale || !strings.Contains(string(renderPack(g.items["ENG-1"].pack, g.items["ENG-1"].Pack)), "It cut search time") {
		t.Error("a correction should reach the pack and make the note stale")
	}
}

func TestCheck(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	run := func(text string) []string {
		var out []string
		for _, fd := range f.Check("notes/ENG-1.md", text) {
			out = append(out, fd.String())
		}
		return out
	}
	head := "---\nitem: ENG-1\ninputs: " + f.items["ENG-1"].Pack + "\n---\n"
	var body strings.Builder
	for _, s := range NoteSections {
		body.WriteString("\n## " + s + "\n\nShipped fast search [[ENG-1]] with [[app#5]] and [[app@c1]], see [[doc+100]], [[adr+app+0001-use-queues]] and [[ENG-4]].\n")
	}
	check(t, "a good note", run(head+body.String()), []string(nil))
	check(t, "a good note with Windows line endings", run(strings.ReplaceAll(head+body.String(), "\n", "\r\n")), []string(nil))
	var names strings.Builder
	for _, s := range NoteSections {
		names.WriteString("\n## " + s + "\n\nMoved Office 365 tenants to Angular 9 and .NET 10 [[ENG-1]], fixing HTTP 429 errors in version 2.\n" +
			"Angular 9, C# 12, Node 18 and Windows 11.\n\n- Python 3\n- Storybook (CSF 3.0 stories and docs pages)\n")
	}
	check(t, "versions and names are not counts", run(head+names.String()), []string(nil))
	for _, f := range run(head + "\n## Summary\n\nI upgraded the monorepo to\nAngular 15 and the library too.\n" +
		"Office 365 tenants could connect.\n\n- Angular 15 upgrade for the library.\n") {
		if strings.Contains(f, "no counts") {
			t.Errorf("a name that opens a line or sentence was flagged: %s", f)
		}
	}
	counts := run(head + "\n## Summary\n\nMerged 12 PRs. Led 4 people, and I wrote 3 docs.\nI rebuilt Storybook 12 times.\n" +
		"TypeScript 1,200 lines, Jira 9 issues, Confluence 5 pages and ADR 2 docs.\n")
	for _, n := range []string{`"12"`, `"4"`, `"3"`, `"1,200"`, `"9"`, `"5"`, `"2"`} {
		if !strings.Contains(strings.Join(counts, "\n"), n+": a note carries no counts") {
			t.Errorf("no count warning for %s:\n%s", n, strings.Join(counts, "\n"))
		}
	}
	got := run(head + "\n## Summary\n\nAlso [[ENG-2]] and [[ENG-404]], in 12 PRs.\n")
	if len(got) < 3 || !strings.Contains(strings.Join(got, "\n"), "[[ENG-2]] is not part of ENG-1's evidence") ||
		!strings.Contains(strings.Join(got, "\n"), "[[ENG-404]] names nothing") || !strings.Contains(strings.Join(got, "\n"), `"12": a note carries no counts`) ||
		!strings.Contains(strings.Join(got, "\n"), "missing section: ## What I did") {
		t.Errorf("findings:\n%s", strings.Join(got, "\n"))
	}

	draft := "---\nwindow: 2024-01-01..2024-12-31\n---\n\n" +
		"I merged 2 PRs and about 6 commits, and finished [[ENG-1]] with its 2 sub-tasks.\n\n" +
		"I mentored 4 people, cut 40% of the time, and upgraded to Go 1.22 on 2024-03-01.\n\n" +
		"See https://tracker.example/browse/ENG-1 and https://tracker.example/browse/ENG-999 and ENG-77.\n"
	var findings []string
	for _, fd := range f.Check("draft.md", draft) {
		findings = append(findings, fd.String())
	}
	want := []string{
		`7: warning: "4" matches no fact in scope`, `7: warning: "40%" is derived`, // Go 1.22 is a version
		"9: warning: https://tracker.example/browse/ENG-999 is not in the dataset", "9: warning: ENG-77 is not in the dataset",
	}
	if len(findings) != len(want) {
		t.Errorf("findings:\n%s", strings.Join(findings, "\n"))
	}
	for i := range want {
		if i < len(findings) && !strings.HasPrefix(findings[i], want[i]) {
			t.Errorf("finding %d = %q, want prefix %q", i, findings[i], want[i])
		}
	}
	adr := f.items["adr+app+0001-use-queues"]
	var adrNote []string
	for _, fd := range f.Check("notes/adr+app+0001-use-queues.md", "---\nitem: "+adr.ID+"\ninputs: "+adr.Pack+"\n---\n"+
		strings.ReplaceAll(body.String(), "[[doc+100]], ", "")) {
		adrNote = append(adrNote, fd.String())
	}
	if s := strings.Join(adrNote, "\n"); !strings.Contains(s, "[[ENG-4]] is not part of") || strings.Contains(s, "[[app#5]]") ||
		strings.Contains(s, "[[app@c1]]") || strings.Contains(s, "[[ENG-1]]") {
		t.Errorf("a note on an ADR cites the changes that wrote it, and the items they count toward:\n%s", s)
	}
	text := "---\nwindow: all\n---\n\nShipped search [[ENG-1]], and [[app#5|the PR]].\n"
	check(t, "plain", f.Final(text, false), "Shipped search, and the PR.\n")
	check(t, "links", f.Final(text, true), "Shipped search [ENG-1](https://tracker.example/browse/ENG-1), and [the PR](https://code.example/org/app/pull/5).\n")
}

// The same dataset, notes and context give the same bytes, whatever order the records come in.
func TestDeterministic(t *testing.T) {
	write := func(raw *Raw) map[string][]byte {
		dir := filepath.Join(t.TempDir(), "facts")
		w, _ := NewWindow("2024-01-01", "2024-03-31")
		if err := build(t, raw, Inputs{}).Write(dir, w); err != nil {
			t.Fatal(err)
		}
		files := map[string][]byte{}
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(dir, p)
				files[filepath.ToSlash(rel)], err = os.ReadFile(p)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	a := write(fixture())
	shuffled := fixture()
	r := rand.New(rand.NewSource(1))
	r.Shuffle(len(shuffled.Commits), func(i, j int) { shuffled.Commits[i], shuffled.Commits[j] = shuffled.Commits[j], shuffled.Commits[i] })
	r.Shuffle(len(shuffled.PRs), func(i, j int) { shuffled.PRs[i], shuffled.PRs[j] = shuffled.PRs[j], shuffled.PRs[i] })
	r.Shuffle(len(shuffled.Issues), func(i, j int) { shuffled.Issues[i], shuffled.Issues[j] = shuffled.Issues[j], shuffled.Issues[i] })
	r.Shuffle(len(shuffled.Pages), func(i, j int) { shuffled.Pages[i], shuffled.Pages[j] = shuffled.Pages[j], shuffled.Pages[i] })
	r.Shuffle(len(shuffled.Decisions), func(i, j int) {
		shuffled.Decisions[i], shuffled.Decisions[j] = shuffled.Decisions[j], shuffled.Decisions[i]
	})
	b := write(shuffled)
	if len(a) != len(b) {
		t.Fatalf("%d files, then %d", len(a), len(b))
	}
	for name, data := range a {
		if !bytes.Equal(data, b[name]) {
			t.Errorf("%s differs when the input is shuffled", name)
		}
	}
	for _, name := range []string{"README.md", "facts.json", "items.jsonl", "packs/ENG-1.md", "windows/2024-01-01_2024-03-31/overview.md", "catalogue.md", "todo.txt"} {
		if _, ok := a[name]; !ok {
			t.Errorf("no %s", name)
		}
	}
}

// Load reads what the collection stages write, through index.json, dot-directories included.
func TestLoad(t *testing.T) {
	dir := t.TempDir()
	raw := fixture()
	write := func(rel string, v any, lines bool) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		if lines {
			for _, row := range reflectRows(v) {
				_ = enc.Encode(row)
			}
		} else {
			_ = enc.Encode(v)
		}
		if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw.Index.Repos[0].Dir = "repos/.app"
	raw.Index.Repos[0].Files = map[string]string{"commits": "repos/.app/commits.jsonl", "prs": "repos/.app/prs.jsonl",
		"decisions": "repos/.app/decisions.jsonl"}
	// a repo without PRs: the index lists no prs file for it
	lib := commit("l1", "2024-01-02T10:00:00Z", jane, []string{"author", "committer"}, "lib work", func(c *collect.Commit) { c.Repo = "lib" })
	raw.Index.Repos = append(raw.Index.Repos, collect.IndexRepo{Name: "lib", Dir: "repos/lib", Files: map[string]string{"commits": "repos/lib/commits.jsonl"}})
	write("repos/lib/project.json", collect.Project{Name: "lib"}, false)
	write("repos/lib/commits.jsonl", []collect.Commit{lib}, true)
	raw.Index.IssuesByProject = []collect.IndexGroup{{Key: "ENG", File: "issues/ENG.jsonl"}}
	raw.Index.WikiBySpace = []collect.IndexGroup{{Key: "ENG", File: "wiki/ENG.jsonl"}}
	write("index.json", raw.Index, false)
	write("repos/.app/project.json", raw.Projects[0], false)
	write("repos/.app/commits.jsonl", raw.Commits, true)
	write("repos/.app/prs.jsonl", raw.PRs, true)
	write("repos/.app/decisions.jsonl", raw.Decisions, true)
	write("issues/ENG.jsonl", raw.Issues, true)
	write("wiki/ENG.jsonl", raw.Pages, true)
	write("org/issue_projects.json", raw.IssueProjects, false)
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "loaded", []int{len(got.Projects), len(got.Commits), len(got.PRs), len(got.Decisions), len(got.Issues), len(got.Pages), len(got.IssueProjects)},
		[]int{2, len(raw.Commits) + 1, len(raw.PRs), len(raw.Decisions), len(raw.Issues), len(raw.Pages), 1})
	if _, err := Load(t.TempDir()); err == nil || !strings.Contains(err.Error(), "no index.json") {
		t.Errorf("want a clear error without a dataset, got %v", err)
	}
}

func reflectRows(v any) []any {
	rv := reflect.ValueOf(v)
	rows := make([]any, rv.Len())
	for i := range rows {
		rows[i] = rv.Index(i).Interface()
	}
	return rows
}

func TestFrontmatter(t *testing.T) {
	for _, nl := range []string{"\n", "\r\n"} {
		text := strings.ReplaceAll("---\nitem: ENG-1\ninputs: \"sha256:x\"\n---\n\n## Summary\n", "\n", nl)
		front, body, ok := Frontmatter(text)
		check(t, fmt.Sprintf("frontmatter, lines ending %q", nl), []any{ok, front, body},
			[]any{true, map[string]string{"item": "ENG-1", "inputs": "sha256:x"}, nl + "## Summary" + nl})
	}
	for _, text := range []string{"no frontmatter\n", "---\nitem: ENG-1\nnever closed\n"} {
		if _, body, ok := Frontmatter(text); ok || body != text {
			t.Errorf("Frontmatter(%q) = %q, %v; want the text back, false", text, body, ok)
		}
	}
}
