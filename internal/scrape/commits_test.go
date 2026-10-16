package scrape

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// gitFixture builds repositories with git itself, isolated from the user's git config.
type gitFixture struct {
	t    *testing.T
	dir  string
	tick int
}

func identityEnv(author, authorEmail, committer, committerEmail string) []string {
	return []string{"GIT_AUTHOR_NAME=" + author, "GIT_AUTHOR_EMAIL=" + authorEmail,
		"GIT_COMMITTER_NAME=" + committer, "GIT_COMMITTER_EMAIL=" + committerEmail}
}

var (
	me        = identityEnv("Jane Doe", "jane.doe@example.com", "Jane Doe", "jane.doe@example.com")
	other     = identityEnv("Other Person", "other@example.com", "Other Person", "other@example.com")
	otherByMe = identityEnv("Other Person", "other@example.com", "Jane Doe", "jane.doe@example.com")
)

func (f *gitFixture) git(who []string, args ...string) string {
	f.t.Helper()
	f.tick++ // one minute per git call keeps author dates distinct and increasing
	date := time.Date(2024, 1, 1, 10, f.tick, 0, 0, time.UTC).Format(time.RFC3339)
	cmd := exec.Command("git", args...)
	cmd.Dir = f.dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	cmd.Env = append(cmd.Env, who...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *gitFixture) write(name, content string) {
	f.t.Helper()
	path := filepath.Join(f.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *gitFixture) commit(who []string, message string) string {
	f.git(who, "add", "-A")
	f.git(who, "commit", "-q", "-m", message)
	return f.git(who, "rev-parse", "HEAD")
}

func TestExtractCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	f := &gitFixture{t: t, dir: filepath.Join(root, "work")}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.git(me, "init", "-q", "-b", "main")
	f.write("a.txt", "one\n")
	authored := f.commit(me, "feat(SHOP-12): add a")
	f.write("b.txt", "two\n")
	coAuthored := f.commit(other, "add b\n\nCo-authored-by: Jane Doe <JANE.DOE@example.com>")
	f.write("c.txt", "three\n")
	committed := f.commit(otherByMe, "add c")
	f.write("d.txt", "four\n")
	unrelated := f.commit(other, "add d")

	f.git(me, "checkout", "-q", "-b", "feature/SHOP-34")
	f.git(me, "mv", "a.txt", "renamed.txt")
	renamed := f.commit(me, "rename a")
	f.write("logo.bin", "\x00\x01\x02binary")
	binary := f.commit(me, "add logo")
	f.write("e.txt", "five\n")
	pushedToMyPR := f.commit(other, "colleague pushes to my PR")

	f.git(me, "checkout", "-q", "main")
	f.git(me, "cherry-pick", renamed)
	picked := f.git(me, "rev-parse", "HEAD")
	f.git(me, "checkout", "-q", "-b", "side")
	f.write("f.txt", "six\n")
	f.commit(other, "side work")
	f.git(me, "checkout", "-q", "main")
	f.git(me, "merge", "--no-ff", "-q", "-m", "Merge pull request #7 from org/side", "side")
	merge := f.git(me, "rev-parse", "HEAD")

	cache := filepath.Join(root, "cache")
	bare := filepath.Join(cache, "mirrors", "repo.git")
	f.git(me, "clone", "--bare", "-q", f.dir, bare)
	f.git(me, "--git-dir", bare, "update-ref", "refs/pull/3/head", pushedToMyPR)
	f.git(me, "--git-dir", bare, "tag", "-a", "v1", "-m", "release", renamed)

	raw := filepath.Join(root, "raw")
	err := writeJSONL(filepath.Join(raw, "repos", "repo", "prs.jsonl"), []PullRequest{
		{Repo: "repo", Number: 3, Commits: []PRCommit{{SHA: renamed}, {SHA: binary}, {SHA: pushedToMyPR}}},
		{Repo: "repo", Number: 7, MergeCommitSHA: &merge},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Org: "org", GitHubLogin: "jdoe", dir: root, Identity: IdentityConfig{
		Emails: []string{"jane.doe@example.com"}, Names: []string{"Jane Doe"}, DiscoveryPattern: "jane|doe",
	}}
	h, err := newHarness(cfg, Options{DataDir: raw, CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	h.jira = &jiraMatcher{keys: map[string]bool{"SHOP": true}}
	mainBranch := "main"
	rows, err := h.extractCommits(context.Background(), Repo{Name: "repo", FullName: "org/repo", DefaultBranch: &mainBranch})
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]Commit{}
	for _, r := range rows {
		got[r.SHA] = r
	}
	if !sort.SliceIsSorted(rows, func(i, j int) bool { return parseTime(rows[i].Author.Date).Before(parseTime(rows[j].Author.Date)) }) {
		t.Error("rows are not in author-date order")
	}
	if _, ok := got[unrelated]; ok || len(rows) != 8 {
		t.Errorf("want the 8 commits involving the user, got %d (unrelated included: %v)", len(rows), ok)
	}
	check := func(sha, what string, value, want any) {
		t.Helper()
		if !reflect.DeepEqual(value, want) {
			t.Errorf("%s %s = %#v, want %#v", sha[:7], what, value, want)
		}
	}

	c := got[authored]
	check(authored, "roles", c.Roles, []string{"author", "committer"})
	check(authored, "matched_by", c.MatchedBy, MatchedBy{Author: "email", Committer: "email"})
	check(authored, "on default", c.OnDefaultBranch, true)
	check(authored, "jira", c.JiraKeys, []string{"SHOP-12"})
	check(authored, "stats", *c.Stats, CommitStats{Files: 1, Additions: 1})
	check(authored, "branches", c.Branches, []string{})
	check(authored, "url", c.URL, "https://github.com/org/repo/commit/"+authored)

	c = got[coAuthored]
	check(coAuthored, "roles", c.Roles, []string{"co-author"})
	check(coAuthored, "co_authors", c.CoAuthors, []Person{{Name: "Jane Doe", Email: "JANE.DOE@example.com"}})

	check(committed, "roles", got[committed].Roles, []string{"committer"})

	c = got[renamed]
	check(renamed, "roles", c.Roles, []string{"author", "committer", "pr-author"})
	check(renamed, "in_user_prs", c.InUserPRs, []int{3})
	check(renamed, "on default", c.OnDefaultBranch, false)
	check(renamed, "branches", c.Branches, []string{"feature/SHOP-34"})
	check(renamed, "pull_request_refs", c.PullRequestRefs, []int{3})
	check(renamed, "tags", c.Tags, []string{"v1"})
	check(renamed, "jira (from branch)", c.JiraKeys, []string{"SHOP-34"})
	check(renamed, "file", c.Files[0].Path+" <- "+c.Files[0].OldPath+" "+c.Files[0].Status, "renamed.txt <- a.txt R")
	if c.PatchID == nil || got[picked].PatchID == nil || *c.PatchID != *got[picked].PatchID {
		t.Error("a cherry-picked copy should share the original's patch_id")
	}
	check(picked, "on default", got[picked].OnDefaultBranch, true)

	c = got[binary]
	check(binary, "binary", c.Files[0].Binary, true)
	check(binary, "additions", c.Files[0].Additions, (*int)(nil))
	check(binary, "binary_files", c.Stats.BinaryFiles, 1)

	c = got[pushedToMyPR]
	check(pushedToMyPR, "roles", c.Roles, []string{"pr-author"})
	check(pushedToMyPR, "matched_by", c.MatchedBy, MatchedBy{PRAuthor: "github_pr"})

	c = got[merge]
	check(merge, "is_merge", c.IsMerge, true)
	check(merge, "stats", c.Stats, (*CommitStats)(nil))
	check(merge, "files", c.Files, []FileChange{})
	check(merge, "patch_id", c.PatchID, (*string)(nil))
	check(merge, "pr numbers", c.PRNumbersInMessage, []int{7})
	check(merge, "lands user PR", c.MergeCommitForUserPRs, []int{7})

	found, err := h.scanIdentities(context.Background(), Repo{Name: "repo"})
	if err != nil {
		t.Fatal(err)
	}
	if found.counts[identityKey{"co-author", "Jane Doe", "JANE.DOE@example.com"}] != 1 ||
		found.counts[identityKey{"committer", "Jane Doe", "jane.doe@example.com"}] == 0 {
		t.Errorf("scanIdentities missed variants: %v", found.counts)
	}
}
