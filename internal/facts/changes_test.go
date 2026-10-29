package facts

import (
	"testing"

	"github.com/marleypowell/footprint/internal/codehost"
	"github.com/marleypowell/footprint/internal/collect"
)

// kinds are the kinds of some events, in order.
func kinds(evs []event) []string {
	out := []string{}
	for _, e := range evs {
		out = append(out, e.kind)
	}
	return out
}

func changeFacts() *Facts {
	return &Facts{people: newPeople(collect.IdentityConfig{Emails: []string{"jane@example.com"}}), keyOf: map[string]string{},
		issues: map[string]*Issue{}, changes: map[string]*Change{}, changeOf: map[string]string{}}
}

// A change's lines leave out generated files (lockfiles, vendored code) and bulk changes (one file
// changing more than filekind.BulkLines lines), and Excluded totals each, all changes together.
func TestLines(t *testing.T) {
	f := changeFacts()
	got := f.lines([]fileChange{
		newFileChange("src/a.go", 10, 2),
		newFileChange("src/b.go", 5, 0),
		newFileChange("package-lock.json", 300, 20),  // generated, by its name
		newFileChange("vendor/x/y.go", 7, 0),         // in a vendored directory
		newFileChange("fixtures/data.json", 5001, 0), // bulk
		newFileChange("src/big.go", 4000, 1000),      // 5000 lines exactly: not bulk
	})
	check(t, "lines", got, Lines{Added: 4015, Deleted: 1002, Files: 3, ExcludedAdded: 5308, ExcludedDeleted: 20, ExcludedFiles: 3})
	check(t, "excluded", f.Excluded, Excluded{Generated: ExcludedLines{Files: 2, Added: 307, Deleted: 20}, Bulk: ExcludedLines{Files: 1, Added: 5001}})
	f.lines([]fileChange{newFileChange("yarn.lock", 9000, 0)})
	check(t, "a generated file that is bulky too is counted once, as generated", f.Excluded,
		Excluded{Generated: ExcludedLines{Files: 3, Added: 9307, Deleted: 20}, Bulk: ExcludedLines{Files: 1, Added: 5001}})
}

func TestCommitHelpers(t *testing.T) {
	for _, tc := range []struct {
		c    collect.Commit
		want bool
	}{
		{collect.Commit{Parents: []string{"a"}}, false},
		{collect.Commit{}, false}, // a root commit
		{collect.Commit{Parents: []string{"a", "b"}}, true},
		{collect.Commit{Parents: []string{"a"}, IsMerge: true}, true},
	} {
		if got := isMerge(&tc.c); got != tc.want {
			t.Errorf("isMerge(parents %v, is_merge %v) = %v, want %v", tc.c.Parents, tc.c.IsMerge, got, tc.want)
		}
	}
	for sha, want := range map[string]string{"0123456789abcdef": "0123456789ab", "0123456789ab": "0123456789ab", "c1": "c1", "": ""} {
		if got := shortSHA(sha); got != want {
			t.Errorf("shortSHA(%q) = %q, want %q", sha, got, want)
		}
	}
}

// A commit's message keys are trusted; a key only its branch names is trusted when the issue
// existed when the commit was made, give or take a day: a branch holds every older commit not yet
// on the default branch, so its name can point at later work.
func TestTrustedKeys(t *testing.T) {
	f := build(t, oneRepo(nil, nil, []collect.IssueRecord{
		issueRec("ENG-1", 0, func(r *collect.IssueRecord) { r.CreatedAt = "2024-01-10T00:00:00Z" }),
		issueRec("ENG-2", 0, func(r *collect.IssueRecord) { r.CreatedAt = "" }),
		issueRec("ENG-3", 0, func(r *collect.IssueRecord) { r.PreviousKeys = []string{"OLD-3"} }),
	}), Inputs{})
	for _, tc := range []struct {
		name         string
		date         string
		keys, branch []string
		want         []string
	}{
		{"a message key, however old the commit", "2020-01-01T00:00:00Z", []string{"ENG-1"}, nil, []string{"ENG-1"}},
		{"a branch key, long before the issue", "2023-06-01T00:00:00Z", []string{"ENG-1"}, []string{"ENG-1"}, []string{}},
		{"a branch key, within a day before the issue", "2024-01-09T01:00:00Z", []string{"ENG-1"}, []string{"ENG-1"}, []string{"ENG-1"}},
		{"a branch key, just over a day before", "2024-01-08T23:00:00Z", []string{"ENG-1"}, []string{"ENG-1"}, []string{}},
		{"a branch key, after the issue", "2024-02-01T00:00:00Z", []string{"ENG-1"}, []string{"ENG-1"}, []string{"ENG-1"}},
		{"a branch key of an issue with no creation date", "2024-02-01T00:00:00Z", []string{"ENG-2"}, []string{"ENG-2"}, []string{}},
		{"an earlier key, any case, by its current key", "2024-02-01T00:00:00Z", []string{"old-3"}, nil, []string{"ENG-3"}},
		{"a key of no issue in the dataset", "2024-02-01T00:00:00Z", []string{"ZZZ-1"}, nil, []string{}},
		{"once each, in natural order", "2024-02-01T00:00:00Z", []string{"ENG-3", "ENG-1", "OLD-3"}, nil, []string{"ENG-1", "ENG-3"}},
	} {
		c := commit("x", tc.date, jane, []string{"author"}, "x", func(c *collect.Commit) { c.IssueKeys, c.BranchIssueKeys = tc.keys, tc.branch })
		check(t, tc.name, f.trustedKeys(&c), tc.want)
	}
}

// A PR's class is whoever wrote most of its commits: the user unless an agent wrote more (and no
// fewer than colleagues), or colleagues wrote more than both. Bots' commits do not decide it.
func TestPRChangeClass(t *testing.T) {
	who := map[byte][2]string{'s': {"Jane Doe", "jane@example.com"}, 'a': {"Claude", "noreply@anthropic.com"},
		'c': {"Other Person", "other@example.com"}, 'b': {"dependabot[bot]", "bot@example.com"}}
	for _, tc := range []struct {
		authors string
		want    string
	}{
		{"", Self}, {"ss", Self}, {"ssa", Self}, {"sa", Self}, {"bbs", Self}, {"sc", Self}, {"bb", Self},
		{"saa", Agent}, {"a", Agent}, {"ac", Agent}, {"ccaa", Agent},
		{"scc", Colleague}, {"c", Colleague}, {"cca", Colleague},
	} {
		pr := prRec(1, "OPEN", "2024-01-01T00:00:00Z", nil, func(p *codehost.PullRequest) {
			for i := range len(tc.authors) {
				a := who[tc.authors[i]]
				p.Commits = append(p.Commits, codehost.PRCommit{SHA: "s" + itoa(i), AuthoredAt: "2024-01-01T00:00:00Z", AuthorName: ptr(a[0]), AuthorEmail: ptr(a[1])})
			}
		})
		if got := changeFacts().prChange("main", &pr, nil, map[string]bool{}).Class; got != tc.want {
			t.Errorf("class of a PR with commits by %q = %s, want %s", tc.authors, got, tc.want)
		}
	}
}

// A PR counts when it merged (else when it opened), lands only when merged into the default
// branch, and has lines only when it landed.
func TestPRChangeState(t *testing.T) {
	files := func(p *codehost.PullRequest) {
		p.Files = []codehost.PRFile{{Path: "a.go", Additions: 10, Deletions: 1}}
	}
	stacked := func(p *codehost.PullRequest) { files(p); p.BaseRef = "feature" }
	for _, tc := range []struct {
		name, state   string
		merged        *string
		defaultBranch string
		mod           func(*codehost.PullRequest)
		wantState     string
		wantAt        string
		wantLanded    bool
		wantLines     Lines
		wantEvents    []string
	}{
		{"open", "OPEN", nil, "main", files, "open", "2024-01-01T09:00:00Z", false, Lines{},
			[]string{evPROpened, evActive}},
		{"closed unmerged", "CLOSED", nil, "main", files, "closed", "2024-01-01T09:00:00Z", false, Lines{},
			[]string{evPROpened, evActive}},
		{"merged into the default branch", "MERGED", ptr("2024-01-03T10:00:00Z"), "main", files, "merged", "2024-01-03T10:00:00Z", true,
			Lines{Added: 10, Deleted: 1, Files: 1}, []string{evPROpened, evActive, evPRMerged, evLanded}},
		{"merged into another branch: a stacked PR", "MERGED", ptr("2024-01-03T10:00:00Z"), "main", stacked, "merged", "2024-01-03T10:00:00Z", false,
			Lines{}, []string{evPROpened, evActive, evPRMerged}},
		{"merged in a repo whose default branch is unknown", "MERGED", ptr("2024-01-03T10:00:00Z"), "", stacked, "merged", "2024-01-03T10:00:00Z", true,
			Lines{Added: 10, Deleted: 1, Files: 1}, []string{evPROpened, evActive, evPRMerged, evLanded}},
	} {
		pr := prRec(4, tc.state, "2024-01-01T09:00:00Z", tc.merged, tc.mod)
		c := changeFacts().prChange(tc.defaultBranch, &pr, nil, map[string]bool{})
		check(t, tc.name, []any{c.ID, c.Kind, c.State, c.At, c.Landed, c.Lines, kinds(c.evs)},
			[]any{"app#4", KindPR, tc.wantState, tc.wantAt, tc.wantLanded, tc.wantLines, tc.wantEvents})
	}
}

// The user's own commits in a PR are its commits, oldest first, each a day of activity; an
// agent's commit in several of the user's PRs counts once.
func TestPRChangeCommits(t *testing.T) {
	f := changeFacts()
	agentCommits := map[string]bool{}
	agent := func(p *codehost.PullRequest) {
		p.Commits = []codehost.PRCommit{{SHA: "agent1", AuthoredAt: "2024-01-02T00:00:00Z", AuthorName: ptr("Claude"), AuthorEmail: ptr("noreply@anthropic.com")}}
	}
	first, second := prRec(1, "OPEN", "2024-01-01T00:00:00Z", nil, agent), prRec(2, "OPEN", "2024-01-01T00:00:00Z", nil, agent)
	own := []*collect.Commit{
		ptr(commit("b", "2024-01-03T00:00:00Z", jane, nil, "later", nil)),
		ptr(commit("c", "2024-01-02T00:00:00Z", jane, nil, "same time, by sha", nil)),
		ptr(commit("a", "2024-01-02T00:00:00Z", jane, nil, "earlier", nil)),
	}
	c := f.prChange("main", &first, own, agentCommits)
	check(t, "own commits, by time then sha", c.Commits, []string{"a", "c", "b"})
	check(t, "events", kinds(c.evs), []string{evAgentCommit, evPROpened, evActive, evCommit, evActive, evCommit, evActive, evCommit, evActive})
	check(t, "the agent's commit, again in another PR", kinds(f.prChange("main", &second, nil, agentCommits).evs), []string{evPROpened, evActive})
}

func TestCommitChange(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mod        func(*collect.Commit)
		wantKind   string
		wantState  string
		wantLines  Lines
		wantEvents []string
	}{
		{"landed", nil, KindCommit, "landed", Lines{Added: 1, Files: 1}, []string{evCommit, evActive, evLanded}},
		{"not on the default branch: no lines", func(c *collect.Commit) { c.OnDefaultBranch = false }, KindCommit, "unlanded", Lines{},
			[]string{evCommit, evActive}},
		{"a PR merged on the host the repo came from: its squash commit", func(c *collect.Commit) { c.MigratedPRNumbersInMessage = []int{88} },
			KindMigratedPR, "landed", Lines{Added: 1, Files: 1}, []string{evCommit, evActive, evLanded, evMigratedPR}},
		{"such a squash commit, not landed", func(c *collect.Commit) { c.MigratedPRNumbersInMessage, c.OnDefaultBranch = []int{88}, false },
			KindMigratedPR, "unlanded", Lines{}, []string{evCommit, evActive}},
	} {
		cm := commit("0123456789abcdef", "2024-01-02T10:00:00+01:00", jane, []string{"author"}, "subject\n\nbody", tc.mod)
		c := changeFacts().commitChange(&cm)
		check(t, tc.name, []any{c.ID, c.Kind, c.State, c.Landed, c.At, c.Class, c.Commits, c.Lines, kinds(c.evs)},
			[]any{"app@0123456789ab", tc.wantKind, tc.wantState, tc.wantState == "landed", "2024-01-02T09:00:00Z", Self,
				[]string{"0123456789abcdef"}, tc.wantLines, tc.wantEvents})
	}
}

// A commit someone else wrote naming the user as co-author is theirs, not the user's: no lines and
// no day of the user's activity. An agent's counts as an agent commit.
func TestCoAuthoredChange(t *testing.T) {
	colleague := commit("c1", "2024-01-02T10:00:00Z", other, []string{"co-author"}, "squash", nil)
	c := changeFacts().coAuthoredChange(&colleague)
	check(t, "a colleague's", []any{c.Class, c.State, c.Landed, c.Lines, c.Commits, kinds(c.evs)},
		[]any{CoAuthor, "landed", true, Lines{}, []string{}, []string{evCoAuthored}})
	agent := commit("c2", "2024-01-02T10:00:00Z", collect.Signature{Name: "Claude", Email: "noreply@anthropic.com"}, []string{"co-author"}, "agent",
		func(c *collect.Commit) { c.OnDefaultBranch = false })
	c = changeFacts().coAuthoredChange(&agent)
	check(t, "an agent's", []any{c.Class, c.State, kinds(c.evs)}, []any{Agent, "unlanded", []string{evAgentCommit}})
}

// The user's commits are grouped by patch, so a rebased or cherry-picked copy is not more work; a
// group with a commit in one of the user's PRs is that PR's; the rest are changes of their own.
func TestRepoChanges(t *testing.T) {
	me := []string{"author", "committer"}
	raw := oneRepo([]collect.Commit{
		// two copies of one patch: the change is the default branch's, though the other is older
		commit("rebased1", "2024-01-01T10:00:00Z", jane, me, "fix", func(c *collect.Commit) { c.PatchID, c.OnDefaultBranch = ptr("p1"), false }),
		commit("landed1", "2024-01-05T10:00:00Z", jane, me, "fix", func(c *collect.Commit) { c.PatchID = ptr("p1") }),
		// one patch in two of the user's PRs: the lower-numbered one's, by its own copy
		commit("in3", "2024-01-02T10:00:00Z", jane, me, "shared", func(c *collect.Commit) { c.PatchID, c.OnDefaultBranch, c.InUserPRs = ptr("p2"), false, []int{3} }),
		commit("in2", "2024-01-03T10:00:00Z", jane, me, "shared", func(c *collect.Commit) { c.PatchID, c.OnDefaultBranch, c.InUserPRs = ptr("p2"), false, []int{2} }),
		// PR 2's squash commit, alone in its patch group: a copy, not one more commit
		commit("squash2", "2024-01-06T10:00:00Z", jane, me, "PR 2 (#2)", func(c *collect.Commit) { c.MergeCommitForUserPRs = []int{2} }),
		// a colleague's commit in the user's PR: part of it, not the user's
		commit("theirs", "2024-01-03T11:00:00Z", other, []string{"pr-author"}, "help", func(c *collect.Commit) { c.OnDefaultBranch, c.InUserPRs = false, []int{2} }),
		// without a patch id (a binary change, say): a group of its own
		commit("nopatch", "2024-01-04T10:00:00Z", jane, me, "binary", func(c *collect.Commit) { c.PatchID = nil }),
		commit("merge", "2024-01-07T10:00:00Z", jane, me, "Merge branch", func(c *collect.Commit) { c.Parents = []string{"a", "b"} }),
		// completing a colleague's PR on the host the repo came from, and not quite
		commit("done4them", "2024-01-08T10:00:00Z", other, []string{"committer"}, "Merged PR 70", func(c *collect.Commit) {
			c.Committer, c.MigratedPRNumbersInMessage = sig(jane, "2024-01-08T10:00:00Z"), []int{70}
		}),
		commit("offmain", "2024-01-08T11:00:00Z", other, []string{"committer"}, "Merged PR 71", func(c *collect.Commit) {
			c.Committer, c.MigratedPRNumbersInMessage, c.OnDefaultBranch = sig(jane, "2024-01-08T11:00:00Z"), []int{71}, false
		}),
		commit("elsewhere", "2024-01-09T10:00:00Z", jane, me, "a repo not in the dataset", func(c *collect.Commit) { c.Repo = "ghost" }),
	}, []collect.PullRequestRecord{
		prRec(3, "OPEN", "2024-01-02T09:00:00Z", nil, nil),
		prRec(2, "MERGED", "2024-01-02T09:00:00Z", ptr("2024-01-06T10:00:00Z"), nil),
		prRec(2, "MERGED", "2024-01-02T09:00:00Z", ptr("2024-01-06T10:00:00Z"), nil), // listed twice: one change
	}, nil)
	f := build(t, raw, Inputs{})
	var ids []string
	for _, c := range f.Changes {
		ids = append(ids, c.ID)
	}
	check(t, "changes, by when they count", ids, []string{"app#3", "app@nopatch", "app@landed1", "app#2"})
	check(t, "PR 2's commits: its own copy of the shared patch", f.changes["app#2"].Commits, []string{"in2"})
	check(t, "PR 3 has none: the shared patch is PR 2's", f.changes["app#3"].Commits, []string{})
	check(t, "every copy, and others' commits in the user's PRs, map to their change", map[string]string{
		"rebased1": f.changeOf["app@rebased1"], "in3": f.changeOf["app@in3"], "squash2": f.changeOf["app@squash2"], "theirs": f.changeOf["app@theirs"],
	}, map[string]string{"rebased1": "app@landed1", "in3": "app#2", "squash2": "app#2", "theirs": "app#2"})
	merged := 0
	for _, e := range f.events {
		if e.kind == evMergedForOthers {
			merged++
			check(t, "completing a colleague's PR", e.key, "app@done4them")
		}
	}
	check(t, "colleagues' PRs completed, on the default branch only", merged, 1)
}
