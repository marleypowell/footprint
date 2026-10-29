package collect

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/codehost"
	"github.com/marleypowell/footprint/internal/doc"
)

func TestParseDiff(t *testing.T) {
	// The layout `git log --raw --numstat -z` emits after the format terminator.
	section := strings.Join([]string{
		"\n:100644 100644 1111111 2222222 M", "src/app.go",
		":000000 100644 0000000 3333333 A", "img/logo.png",
		":100644 100644 4444444 4444444 R100", "old/name.txt", "new/name.txt",
		"3\t1\tsrc/app.go",
		"-\t-\timg/logo.png",
		"0\t0\t", "old/name.txt", "new/name.txt",
		"",
	}, "\x00")
	got := parseDiff(section)
	hundred := 100
	want := []FileChange{
		{Path: "src/app.go", Status: "M", Additions: ptr(3), Deletions: ptr(1)},
		{Path: "img/logo.png", Status: "A", Binary: true},
		{Path: "new/name.txt", OldPath: "old/name.txt", Status: "R", Similarity: &hundred, Additions: ptr(0), Deletions: ptr(0)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseDiff:\n got %s\nwant %s", mustJSON(t, got), mustJSON(t, want))
	}
	if got := parseDiff("\x00"); got == nil || len(got) != 0 {
		t.Errorf("a merge (no diff) should give an empty, non-nil list, got %#v", got)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := doc.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseDiffEdgeCases(t *testing.T) {
	section := strings.Join([]string{
		":100644 100644 1111111 1111111 C075", "base.txt", "copy.txt",
		":100644 100644 5555555 6666666 M", "unlisted.dat", // a raw entry numstat has no line for
		"2\t0\t", "base.txt", "copy.txt",
		"",
	}, "\x00")
	want := []FileChange{
		{Path: "copy.txt", OldPath: "base.txt", Status: "C", Similarity: ptr(75), Additions: ptr(2), Deletions: ptr(0)},
		{Path: "unlisted.dat", Status: "M", Binary: true}, // no line counts: reported as binary
	}
	if got := parseDiff(section); !reflect.DeepEqual(got, want) {
		t.Errorf("parseDiff:\n got %s\nwant %s", mustJSON(t, got), mustJSON(t, want))
	}
}

func TestCountPtr(t *testing.T) {
	for in, want := range map[string]*int{"0": ptr(0), "42": ptr(42), "-": nil, "": nil} {
		if got := countPtr(in); !reflect.DeepEqual(got, want) {
			t.Errorf("countPtr(%q) = %v, want %v", in, got, want)
		}
	}
}

// Roles are listed in a fixed order, whatever matched first.
func TestMatchedByRoles(t *testing.T) {
	tests := []struct {
		m    MatchedBy
		want []string
	}{
		{MatchedBy{}, []string{}},
		{MatchedBy{PRAuthor: "authored_pr", Author: "name"}, []string{"author", "pr-author"}},
		{MatchedBy{Author: "email", Committer: "email", CoAuthor: "name", PRAuthor: "authored_pr"},
			[]string{"author", "committer", "co-author", "pr-author"}},
	}
	for _, tt := range tests {
		if got := tt.m.roles(); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%+v.roles() = %q, want %q", tt.m, got, tt.want)
		}
	}
}

func TestSortedSetHelpers(t *testing.T) {
	if got := sortedInts([]int{7, 3, 3}); !reflect.DeepEqual(got, []int{3, 3, 7}) {
		t.Errorf("sortedInts = %v", got)
	}
	if got := sortedInts(nil); got == nil {
		t.Error("sortedInts(nil) should be [] for JSON, not null")
	}
	in := []int{2, 1}
	sortedInts(in)
	if in[0] != 2 {
		t.Error("sortedInts sorted its input in place")
	}
	if got := mergeSorted([]string{"ENG-2", "ENG-1"}, []string{"ENG-1", "ENG-3"}); !reflect.DeepEqual(got, []string{"ENG-1", "ENG-2", "ENG-3"}) {
		t.Errorf("mergeSorted = %q", got)
	}
	if got := subtractSorted([]string{"ENG-3", "ENG-1", "ENG-2"}, []string{"ENG-2", "ENG-9"}); !reflect.DeepEqual(got, []string{"ENG-1", "ENG-3"}) {
		t.Errorf("subtractSorted = %q", got)
	}
}

// extractCommits keeps a commit when the user authored it, committed it, is a co-author in its
// message, or it is in or lands one of the user's PRs. Each role says how it matched: by email, by
// name, or by the PR. A message that only mentions the user does not make a commit theirs.
func TestExtractCommitsWhichCommitsAreTheUsers(t *testing.T) {
	f := newWorkRepo(t)
	laptop := identityEnv("Jane Doe", "jane@laptop.local", "Other Person", "other@example.com") // her name, not a configured email
	step := 0
	commit := func(who []string, message string) string {
		step++
		f.write(fmt.Sprintf("file%d.txt", step), "x\n")
		return f.commit(who, message)
	}
	authored := commit(me, "add a")
	byName := commit(laptop, "add b")
	committed := commit(otherByMe, "add c")
	coAuthored := commit(other, "add d\n\nCo-authored-by: jane doe <jane@laptop.local>")
	thanked := commit(other, "add e, thanks Jane Doe")                                    // a mention, no trailer
	namesake := commit(other, "add f\n\nCo-authored-by: Janet Smith <janet@example.com>") // matches the discovery pattern, not the identity
	unrelated := commit(other, "add g")
	f.git(me, "checkout", "-q", "-b", "feature")
	inMyPR := commit(other, "a colleague pushes to my PR")
	f.git(me, "checkout", "-q", "main")
	f.git(other, "merge", "--no-ff", "-q", "-m", "Merge branch 'feature'", "feature")
	merge := f.git(me, "rev-parse", "HEAD")

	h := newTestHarness(t, Tools{Host: &fakeHost{}})
	repo := testRepo("api")
	f.mirrorTo(h.mirrorPath(repo.Name))
	writeRows(t, h, "repos/api/prs.jsonl",
		PullRequestRecord{PullRequest: codehost.PullRequest{Repo: "api", Number: 5, MergeCommitSHA: &merge,
			Commits: []codehost.PRCommit{{SHA: inMyPR}}}},
		// a PR merged into a branch since deleted: its commit is in no ref, and is skipped
		PullRequestRecord{PullRequest: codehost.PullRequest{Repo: "api", Number: 6,
			Commits: []codehost.PRCommit{{SHA: "1111111111111111111111111111111111111111"}}}})
	rows, err := h.extractCommits(context.Background(), repo, linearKeys)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Commit{}
	var order []string
	for _, r := range rows {
		got[r.SHA] = r
		order = append(order, r.SHA)
	}
	tests := []struct {
		name    string
		sha     string
		matched MatchedBy // zero: not the user's
	}{
		{"authored and committed with a configured email", authored, MatchedBy{Author: "email", Committer: "email"}},
		{"authored under a configured name", byName, MatchedBy{Author: "name"}},
		{"committed only (applied a colleague's patch)", committed, MatchedBy{Committer: "email"}},
		{"co-author trailer, by name, ignoring case", coAuthored, MatchedBy{CoAuthor: "name"}},
		{"a colleague's commit in the user's PR", inMyPR, MatchedBy{PRAuthor: "authored_pr"}},
		{"the merge commit landing the user's PR", merge, MatchedBy{PRAuthor: "authored_pr"}},
		{"mentioned in the message, not a co-author", thanked, MatchedBy{}},
		{"a co-author who only resembles the user", namesake, MatchedBy{}},
		{"someone else's", unrelated, MatchedBy{}},
	}
	for _, tt := range tests {
		c, ok := got[tt.sha]
		switch {
		case tt.matched == MatchedBy{} && ok:
			t.Errorf("%s: kept with roles %q, want it left out", tt.name, c.Roles)
		case tt.matched != MatchedBy{} && !ok:
			t.Errorf("%s: left out, want matched_by %+v", tt.name, tt.matched)
		case ok && (c.MatchedBy != tt.matched || !reflect.DeepEqual(c.Roles, tt.matched.roles())):
			t.Errorf("%s: roles %q, matched_by %+v, want %+v", tt.name, c.Roles, c.MatchedBy, tt.matched)
		}
	}
	if want := []string{authored, byName, committed, coAuthored, inMyPR, merge}; !reflect.DeepEqual(order, want) {
		t.Errorf("rows are not oldest first:\n got %q\nwant %q", order, want)
	}
	if c := got[inMyPR]; !reflect.DeepEqual(c.InUserPRs, []int{5}) || !reflect.DeepEqual(c.MergeCommitForUserPRs, []int{}) {
		t.Errorf("PR commit: in_user_prs %v, merge_commit_for_user_prs %v", c.InUserPRs, c.MergeCommitForUserPRs)
	}
	if c := got[merge]; !reflect.DeepEqual(c.MergeCommitForUserPRs, []int{5}) || !c.IsMerge || c.Stats != nil || c.PatchID != nil {
		t.Errorf("merge commit: lands %v, is_merge %v, stats %v, patch_id %v; want a merge without stats or patch id",
			c.MergeCommitForUserPRs, c.IsMerge, c.Stats, c.PatchID)
	}
	if c := got[authored]; c.Stats == nil || *c.Stats != (CommitStats{Files: 1, Additions: 1}) || !c.OnDefaultBranch {
		t.Errorf("authored commit: stats %+v, on default %v", c.Stats, c.OnDefaultBranch)
	}
}

// Issue keys come from the message, and for commits not on the default branch also from the
// names of the branches holding them; branch_issue_keys are those only a branch name supplies.
func TestExtractCommitsIssueKeys(t *testing.T) {
	f := newWorkRepo(t)
	f.write("a.txt", "one\n")
	onMain := f.commit(me, "ENG-1: add a")
	f.git(me, "checkout", "-q", "-b", "feature/ENG-7-retries")
	f.write("b.txt", "two\n")
	fromBoth := f.commit(me, "ENG-3: start retries")
	f.write("c.txt", "three\n")
	named := f.commit(me, "finish eng-7")
	f.git(me, "branch", "keep/ENG-1", onMain) // a branch on a default-branch commit adds nothing

	h := newTestHarness(t, Tools{Host: &fakeHost{}})
	repo := testRepo("api")
	f.mirrorTo(h.mirrorPath(repo.Name))
	rows, err := h.extractCommits(context.Background(), repo, linearKeys)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2][]string{}
	for _, r := range rows {
		got[r.SHA] = [2][]string{r.IssueKeys, r.BranchIssueKeys}
	}
	want := map[string][2][]string{
		onMain:   {{"ENG-1"}, {}},
		fromBoth: {{"ENG-3", "ENG-7"}, {"ENG-7"}},
		named:    {{"ENG-7"}, {}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("issue keys, branch-only keys = %q, want %q", got, want)
	}
}

// The commits stage writes commits.jsonl where the user has commits, and removes it where they no
// longer have any (after an identity config change, say).
func TestCommitsRemovesAStaleFile(t *testing.T) {
	f := newWorkRepo(t)
	f.write("a.txt", "one\n")
	f.commit(other, "someone else's")
	h := newTestHarness(t, Tools{Host: &fakeHost{}})
	writeInventory(t, h, testRepo("api"))
	f.mirrorTo(h.mirrorPath("api"))
	stale := filepath.Join(h.repoDir("api"), "commits.jsonl")
	writeRows(t, h, "repos/api/commits.jsonl", Commit{Repo: "api", SHA: "gone"})
	if err := h.commits(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fileExists(stale) {
		t.Error("commits.jsonl outlived the user's commits")
	}
}

func TestCommitDetails(t *testing.T) {
	f := newWorkRepo(t)
	f.write("a.txt", "one\n")
	parent := f.commit(me, "add a")
	f.write("a.txt", "one\ntwo\n")
	sha := f.commit(otherByMe, "extend a\n\nWhy it grew.\n\nCo-authored-by: Jane Doe <jane.doe@example.com>\n")
	details, err := commitDetails(context.Background(), f.gitDir(), []string{sha})
	if err != nil {
		t.Fatal(err)
	}
	d := details[sha]
	got := []any{d.parents, d.author.Name, d.committer.Name, d.subject, d.body, d.message}
	want := []any{[]string{parent}, "Other Person", "Jane Doe", "extend a",
		"Why it grew.\n\nCo-authored-by: Jane Doe <jane.doe@example.com>",
		"extend a\n\nWhy it grew.\n\nCo-authored-by: Jane Doe <jane.doe@example.com>"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commitDetails = %q, want %q", got, want)
	}
	if want := []FileChange{{Path: "a.txt", Status: "M", Additions: ptr(1), Deletions: ptr(0)}}; !reflect.DeepEqual(d.files, want) {
		t.Errorf("files = %s, want %s", mustJSON(t, d.files), mustJSON(t, want))
	}
}

// patch-id is the same for the same change on another base (a rebase or cherry-pick), and absent
// for a commit that changes nothing.
func TestPatchIDs(t *testing.T) {
	f := newWorkRepo(t)
	f.write("base.txt", "base\n")
	base := f.commit(other, "base")
	f.write("a.txt", "one\n")
	original := f.commit(me, "add a")
	f.git(me, "checkout", "-q", "-b", "other", base)
	f.write("b.txt", "unrelated\n")
	f.commit(other, "add b")
	f.write("a.txt", "one\n")
	copied := f.commit(me, "add a, again")
	f.write("a.txt", "one, but different\n")
	different := f.commit(me, "change a")
	f.git(me, "commit", "-q", "--allow-empty", "-m", "nothing")
	empty := f.git(me, "rev-parse", "HEAD")

	ids, err := patchIDs(context.Background(), f.gitDir(), []string{original, copied, different, empty})
	if err != nil {
		t.Fatal(err)
	}
	if ids[original] == "" || ids[original] != ids[copied] {
		t.Errorf("the same change on two bases: %q and %q, want the same id", ids[original], ids[copied])
	}
	if ids[different] == ids[original] {
		t.Error("a different change got the same id")
	}
	if _, ok := ids[empty]; ok {
		t.Error("an empty commit got a patch id")
	}
	if ids, err := patchIDs(context.Background(), "/nonexistent", nil); err != nil || len(ids) != 0 {
		t.Errorf("patchIDs(no commits) = %v, %v; want nothing, without running git", ids, err)
	}
	if _, err := patchIDs(context.Background(), f.gitDir(), []string{"1111111111111111111111111111111111111111"}); err == nil {
		t.Error("patchIDs of an unknown commit should fail")
	}
}

// existingCommits keeps the shas that name a commit in the repo: PR commits can be gone (a branch
// deleted unmerged, history rewritten), and a sha can name another kind of object.
func TestExistingCommits(t *testing.T) {
	f := newWorkRepo(t)
	f.write("a.txt", "one\n")
	sha := f.commit(me, "add a")
	tree := f.git(me, "rev-parse", "HEAD^{tree}")
	got, err := existingCommits(context.Background(), f.gitDir(), []string{sha, tree, "1111111111111111111111111111111111111111"})
	if want := map[string]bool{sha: true}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("existingCommits = %v, %v, want only the commit", got, err)
	}
}
