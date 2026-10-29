package facts

import (
	"bytes"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
	reviewed := func(n int, author string, keys []string, mod func(*codehost.ReviewedPullRequest)) collect.ReviewRecord {
		p := codehost.ReviewedPullRequest{Repo: "app", Number: n, URL: "https://code.example/org/app/pull/" + itoa(n),
			Title: "theirs " + itoa(n), State: "MERGED", Author: ptr(author), Reviews: []codehost.ReviewerReview{}, Comments: []codehost.Comment{}}
		if mod != nil {
			mod(&p)
		}
		return collect.ReviewRecord{ReviewedPullRequest: p, IssueKeys: keys}
	}
	reviews := []collect.ReviewRecord{
		// a colleague's PR on the user's feature
		reviewed(21, "sam", []string{"ENG-1"}, func(p *codehost.ReviewedPullRequest) {
			p.Reviews = []codehost.ReviewerReview{
				{ID: "r1", State: codehost.ReviewChangesRequested, SubmittedAt: "2024-01-10T10:00:00Z", Body: "Needs a backoff before retrying",
					Comments: []codehost.ReviewComment{{ID: "rc1", Path: "q.go", Body: "cap this", CreatedAt: "2024-01-10T10:00:00Z"}}},
				{ID: "r2", State: codehost.ReviewApproved, SubmittedAt: "2024-01-11T10:00:00Z"},
			}
			p.Comments = []codehost.Comment{{ID: "c1", Body: "thanks", CreatedAt: "2024-01-11T11:00:00Z"}}
		}),
		reviewed(22, "renovate", nil, func(p *codehost.ReviewedPullRequest) {
			p.AuthorIsBot = true
			p.Reviews = []codehost.ReviewerReview{{ID: "r3", State: codehost.ReviewApproved, SubmittedAt: "2024-02-10T10:00:00Z"}}
		}),
		// a comment only, on a PR naming an issue outside the dataset
		reviewed(23, "sam", []string{"ENG-404"}, func(p *codehost.ReviewedPullRequest) {
			p.Comments = []codehost.Comment{{ID: "c2", Body: "a question", CreatedAt: "2024-02-11T10:00:00Z"}}
		}),
		reviewed(24, "Alex", nil, func(p *codehost.ReviewedPullRequest) {
			p.Reviews = []codehost.ReviewerReview{{ID: "r4", State: codehost.ReviewApproved, SubmittedAt: "2024-04-05T10:00:00Z"}}
		}),
	}
	return &Raw{
		Reviews: reviews, ReviewsRan: true,
		Index: collect.Index{Identity: collect.IdentityConfig{Emails: []string{"jane@example.com"}, Names: []string{"Jane Doe"}},
			Sources: collect.Sources{CodeHost: "fakehost", Catalog: "fakecatalog", IssueTracker: ptr("faketracker"), Wiki: ptr("fakewiki")},
			Repos:   []collect.IndexRepo{{Name: "app", URL: "https://code.example/org/app", FirstCommitInRepo: ptr("2024-01-02T09:00:00Z")}}},
		Projects: []collect.Project{{Name: "app", URL: "https://code.example/org/app", CodeHost: codehost.Repo{Name: "app", DefaultBranch: &mainBranch}}},
		Commits:  commits, PRs: prs, Decisions: decisions, Issues: issues, Pages: pages,
		IssueProjects: []tracker.Project{{Key: "ENG", Name: "Engineering", PreviousKeys: []string{"OLD"}}},
	}
}

// oneRepo is a small dataset: the repo app (default branch main), the user Jane Doe, and the
// commits, PRs and issues given.
func oneRepo(commits []collect.Commit, prs []collect.PullRequestRecord, issues []collect.IssueRecord) *Raw {
	mainBranch := "main"
	return &Raw{
		Index: collect.Index{Identity: collect.IdentityConfig{Emails: []string{"jane@example.com"}, Names: []string{"Jane Doe"}},
			Repos: []collect.IndexRepo{{Name: "app", URL: "https://code.example/org/app"}}},
		Projects: []collect.Project{{Name: "app", URL: "https://code.example/org/app", CodeHost: codehost.Repo{Name: "app", DefaultBranch: &mainBranch}}},
		Commits:  commits, PRs: prs, Issues: issues,
	}
}

// prRec is one of the user's PRs in app, against main.
func prRec(n int, state, created string, merged *string, mod func(*codehost.PullRequest)) collect.PullRequestRecord {
	p := codehost.PullRequest{Repo: "app", Number: n, URL: "https://code.example/org/app/pull/" + itoa(n), Title: "PR " + itoa(n),
		State: state, CreatedAt: created, MergedAt: merged, BaseRef: "main", Labels: []string{}, Assignees: []string{}, Reviews: []codehost.Review{}}
	if mod != nil {
		mod(&p)
	}
	return collect.PullRequestRecord{PullRequest: p, IssueKeys: []string{}}
}

// issueRec is an issue in project ENG, created on 2023-12-01, that the user has no part in.
func issueRec(key string, level int, mod func(*collect.IssueRecord)) collect.IssueRecord {
	r := collect.IssueRecord{Issue: tracker.Issue{Key: key, ID: "id-" + key, URL: "https://tracker.example/browse/" + key, PreviousKeys: []string{},
		Project: project, Type: "Story", Level: level, Summary: "Summary of " + key, Status: "To Do", StatusCategory: "todo",
		CreatedAt: "2023-12-01T00:00:00+00:00", Subtasks: []tracker.IssueRef{}, Comments: []tracker.Comment{}, History: []tracker.Change{},
		Worklogs: []tracker.Worklog{}}, Roles: []string{}}
	if mod != nil {
		mod(&r)
	}
	return r
}

// mustTime parses a timestamp or a day, failing the test on a typo.
func mustTime(t *testing.T, ts string) time.Time {
	t.Helper()
	v, ok := parseTime(ts)
	if !ok {
		t.Fatalf("parseTime(%q) failed", ts)
	}
	return v
}

// writeFile writes text to dir/rel, making its directories.
func writeFile(t *testing.T, dir, rel, text string) string {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
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
		PRsReviewed: 2, ChangesRequested: 1, ReviewComments: 3, AuthorsReviewed: 2, BotPRsReviewed: 1,
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

// testFramework is a framework.md with every kind of marker, and a problem of each kind; the framework
// tests and TestDeterministic read it.
const testFramework = "# Framework\n\n## Competencies\n\n" +
	"- **Code {#code}:** Write clean code.\n" +
	"- **Quality {#quality reviews}:** Drive best practices via code reviews.\n\n" +
	"#### Senior Engineer {level=senior}\n\n" +
	"- Coach on technical practices. {#senior-coach}\n" +
	"- Drive process improvements. {#senior-process}\n\n" +
	"#### Lead Engineer {level=lead}\n\n" +
	"- Lead work-streams. {#lead-streams}\n\n" +
	"### Proficiency\n\n" +
	"| Competency | Applying {level=applying} | Leading {level=leading} |\n|---|---|---|\n" +
	"| Testing {#testing} | Writes tests | Drives the test strategy |\n| Other | x | y |\n\n" +
	"- Code again {#code}\n\n```\n{level=fenced}\n```\n\n- a stray {level=oops}\n"

// The same dataset, notes and context give the same bytes, whatever order the records come in.
func TestDeterministic(t *testing.T) {
	ctx := t.TempDir()
	for name, text := range map[string]string{"framework.md": testFramework,
		filepath.Join("evidence", "talk.md"): "---\nwhen: 2024-02-01\nshows: code\n---\nA talk.\n"} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(ctx, name)), 0o755)
		if err := os.WriteFile(filepath.Join(ctx, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write := func(raw *Raw) map[string][]byte {
		dir := filepath.Join(t.TempDir(), "facts")
		w, _ := NewWindow("2024-01-01", "2024-03-31")
		if err := build(t, raw, Inputs{ContextDir: ctx}).Write(dir, w); err != nil {
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
	r.Shuffle(len(shuffled.Reviews), func(i, j int) { shuffled.Reviews[i], shuffled.Reviews[j] = shuffled.Reviews[j], shuffled.Reviews[i] })
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
	for _, name := range []string{"README.md", "facts.json", "items.jsonl", "packs/ENG-1.md", "windows/2024-01-01_2024-03-31/overview.md", "catalogue.md", "todo.txt", "reviews.jsonl", "technologies.jsonl", "criteria.md", "tags.txt", "windows/2024-01-01_2024-03-31/criteria.md"} {
		if _, ok := a[name]; !ok {
			t.Errorf("no %s", name)
		}
	}
}

func TestSortedSet(t *testing.T) {
	check(t, "natural order", sortedSet(map[string]bool{"ENG-10": true, "ENG-2": true, "app#5": true}), []string{"ENG-2", "ENG-10", "app#5"})
	check(t, "never nil", sortedSet(nil), []string{})
	check(t, "deref", []any{deref[int](nil), deref(ptr("x"))}, []any{0, "x"})
}

// An event without a time (an unreadable timestamp) counts toward nothing.
func TestEmit(t *testing.T) {
	f := &Facts{}
	f.emit(event{kind: evCommit})
	f.emit(event{at: mustTime(t, "2024-01-01"), kind: evCommit})
	check(t, "events", kinds(f.events), []string{evCommit})
}

// Build reads notes and context from directories that may be absent, but not unreadable.
func TestBuildInputs(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := Build(fixture(), Inputs{NotesDir: missing, ContextDir: missing}); err != nil {
		t.Errorf("Build with absent notes and context: %v", err)
	}
	notADir := writeFile(t, t.TempDir(), "file", "x")
	for name, in := range map[string]Inputs{"notes": {NotesDir: notADir}, "context": {ContextDir: notADir}} {
		if _, err := Build(fixture(), in); err == nil {
			t.Errorf("Build with a file for its %s directory: nil error", name)
		}
	}
	f := build(t, &Raw{}, Inputs{})
	check(t, "an empty dataset", []int{len(f.Changes), len(f.Items), len(f.Docs), len(f.Repos)}, []int{0, 0, 0, 0})
}
