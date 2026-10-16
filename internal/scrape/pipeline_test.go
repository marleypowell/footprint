package scrape

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/marleypowell/footprint/internal/catalog"
	"github.com/marleypowell/footprint/internal/codehost"
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
	p := filepath.Join(f.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *gitFixture) commit(who []string, message string) string {
	f.git(who, "add", "-A")
	f.git(who, "commit", "-q", "-m", message)
	return f.git(who, "rev-parse", "HEAD")
}

// TestPipeline runs every stage, offline, through in-memory adapters, against a repository that
// exercises every commit role, renames, binaries, cherry-picks, merges, tags and change-request refs.
func TestPipeline(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	f := &gitFixture{t: t, dir: filepath.Join(root, "work")}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.git(me, "init", "-q", "-b", "main")
	f.write("README.md", "# repo\n\nDoes things.\n")
	f.write("cortex.json", `[
  {"tag": "service:repo", "category": "component", "title": "Repo service", "description": "Does things",
   "repo": "https://code.example/org/repo", "part_of": ["domain:platform", "team:otters"]},
  {"tag": "domain:platform", "category": "domain", "title": "Platform", "description": "Shared services"}
]`)
	f.commit(other, "docs: describe the repo")
	f.write("a.txt", "one\n")
	authored := f.commit(me, "feat(ENG-12): add a")
	f.write("b.txt", "two\n")
	coAuthored := f.commit(other, "add b\n\nCo-authored-by: Jane Doe <JANE.DOE@example.com>")
	f.write("c.txt", "three\n")
	committed := f.commit(otherByMe, "add c")
	f.write("d.txt", "four\n")
	unrelated := f.commit(other, "add d")

	f.git(me, "checkout", "-q", "-b", "feature/ENG-34")
	f.git(me, "mv", "a.txt", "renamed.txt")
	renamed := f.commit(me, "rename a")
	f.write("logo.bin", "\x00\x01\x02binary")
	binary := f.commit(me, "add logo")
	f.write("e.txt", "five\n")
	pushedToMyPR := f.commit(other, "colleague pushes to my MR")
	f.git(me, "update-ref", "refs/merge-requests/3/head", pushedToMyPR) // GitLab's name for MR heads
	f.git(me, "tag", "-a", "v1", "-m", "release", renamed)

	f.git(me, "checkout", "-q", "main")
	f.git(me, "cherry-pick", renamed)
	picked := f.git(me, "rev-parse", "HEAD")
	f.git(me, "checkout", "-q", "-b", "side")
	f.write("f.txt", "six\n")
	f.commit(other, "side work")
	f.git(me, "checkout", "-q", "main")
	f.git(me, "merge", "--no-ff", "-q", "-m", "Merge branch 'side'\n\nSee merge request org/repo!7", "side")
	merge := f.git(me, "rev-parse", "HEAD")

	mainBranch := "main"
	host := &fakeHost{
		remotes: map[string]string{"repo": f.dir},
		repos: []codehost.Repo{{Name: "repo", FullName: "org/repo", URL: "https://code.example/org/repo",
			DefaultBranch: &mainBranch, CreatedAt: "2024-01-01T00:00:00Z", Languages: []codehost.Language{}, Topics: []string{}}},
		prs: []codehost.PullRequest{
			{Repo: "repo", Number: 3, Title: "ENG-42: rename a", Commits: []codehost.PRCommit{{SHA: renamed}, {SHA: binary}, {SHA: pushedToMyPR}}},
			{Repo: "repo", Number: 7, Title: "side work", MergeCommitSHA: &merge},
		},
		attributed: []codehost.AttributedCommit{
			{Repo: "repo", SHA: authored, AuthorName: "Jane Doe", AuthorEmail: "jane.doe@example.com"},
			{Repo: "repo", SHA: "0000000000000000000000000000000000000000", Date: "2023-01-01T00:00:00Z", Subject: "lost"},
		},
	}
	cfg := &Config{
		Org: "org", Login: "jdoe", dir: root,
		Identity:     IdentityConfig{Emails: []string{"jane.doe@example.com"}, Names: []string{"Jane Doe"}, DiscoveryPattern: "jane|doe"},
		CodeHost:     Tool{Kind: "fakehost"},
		Catalog:      Tool{Kind: "fakecatalog"},
		IssueTracker: Tool{Kind: "faketracker"},
		Mirror:       MirrorConfig{Parallelism: 2, FetchPullRefs: true},
	}
	raw, cache := filepath.Join(root, "raw"), filepath.Join(root, "cache")
	opts := Options{DataDir: raw, CacheDir: cache}
	if err := Run(context.Background(), opts, cfg, Tools{Host: host, Catalog: fakeCatalog{}, Tracker: fakeTracker{}}, StageOrder); err != nil {
		t.Fatal(err)
	}

	check := func(what string, got, want any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", what, got, want)
		}
	}
	read := func(rel string, v any) {
		t.Helper()
		if ok, err := readJSON(filepath.Join(raw, rel), v); !ok || err != nil {
			t.Fatalf("reading %s: ok=%v err=%v", rel, ok, err)
		}
	}

	var status map[string]MirrorStatus
	if ok, err := readJSON(filepath.Join(cache, "mirror_status.json"), &status); !ok || err != nil || !status["repo"].OK {
		t.Fatalf("mirror status: %+v %v", status, err)
	}
	check("mirrored change-request refs (mapped to refs/pull/*/head)", deref(status["repo"].PullHeads), 1)

	// commits.jsonl
	rows, err := readJSONL[Commit](filepath.Join(raw, "repos", "repo", "commits.jsonl"))
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
	c := got[authored]
	check("authored roles", c.Roles, []string{"author", "committer"})
	check("authored matched_by", c.MatchedBy, MatchedBy{Author: "email", Committer: "email"})
	check("authored on default", c.OnDefaultBranch, true)
	check("authored issue keys (tracker)", c.IssueKeys, []string{"ENG-12"})
	check("authored stats", *c.Stats, CommitStats{Files: 1, Additions: 1})
	check("authored url (host convention)", c.URL, "https://code.example/org/repo/-/commit/"+authored)
	check("co-author roles", got[coAuthored].Roles, []string{"co-author"})
	check("co-authors", got[coAuthored].CoAuthors, []Person{{Name: "Jane Doe", Email: "JANE.DOE@example.com"}})
	check("committer-only roles", got[committed].Roles, []string{"committer"})
	c = got[renamed]
	check("renamed roles", c.Roles, []string{"author", "committer", "pr-author"})
	check("renamed in_user_prs", c.InUserPRs, []int{3})
	check("renamed on default", c.OnDefaultBranch, false)
	check("renamed branches", c.Branches, []string{"feature/ENG-34"})
	check("renamed PR refs", c.PullRequestRefs, []int{3})
	check("renamed tags", c.Tags, []string{"v1"})
	check("renamed issue keys (from branch)", c.IssueKeys, []string{"ENG-34"})
	check("rename", c.Files[0].Path+" <- "+c.Files[0].OldPath+" "+c.Files[0].Status, "renamed.txt <- a.txt R")
	if c.PatchID == nil || got[picked].PatchID == nil || *c.PatchID != *got[picked].PatchID {
		t.Error("a cherry-picked copy should share the original's patch_id")
	}
	check("binary file", got[binary].Files[0].Binary && got[binary].Files[0].Additions == nil, true)
	check("pushed-to-my-PR roles", got[pushedToMyPR].Roles, []string{"pr-author"})
	check("pushed-to-my-PR matched_by", got[pushedToMyPR].MatchedBy, MatchedBy{PRAuthor: "authored_pr"})
	c = got[merge]
	check("merge", c.IsMerge && c.Stats == nil && c.PatchID == nil && len(c.Files) == 0, true)
	check("merge PR numbers (host convention)", c.PRNumbersInMessage, []int{7})
	check("merge lands user PR", c.MergeCommitForUserPRs, []int{7})

	// prs.jsonl
	prs, err := readJSONL[PullRequestRecord](filepath.Join(raw, "repos", "repo", "prs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	check("PR issue keys", prs[0].IssueKeys, []string{"ENG-42"})

	// catalog and project.json
	entities, err := readJSONL[catalog.Entity](filepath.Join(raw, "catalog", "entities.jsonl"))
	if err != nil || len(entities) != 2 || deref(entities[0].SourceRepo) != "repo" || entities[1].SourceRepo != nil {
		t.Fatalf("entities: %+v %v", entities, err)
	}
	if !fileExists(filepath.Join(raw, "catalog", "files", "repo", "cortex.json")) {
		t.Error("raw descriptor not kept")
	}
	var project Project
	read("repos/repo/project.json", &project)
	check("project code host", project.CodeHost.FullName, "org/repo")
	check("project entities", len(project.Catalog.Entities), 1)
	if d := project.Catalog.Context["domain:platform"]; d == nil || deref(d.Description) != "Shared services" {
		t.Errorf("context should resolve the domain: %+v", project.Catalog.Context)
	}
	if v, ok := project.Catalog.Context["team:otters"]; !ok || v != nil {
		t.Errorf("an undefined ref should be null in context: %+v", project.Catalog.Context)
	}
	check("project docs", project.Docs[0].SavedAs, "docs/README.md")
	check("project activity authored", project.Activity.Authored, 5)

	// index.json and org files
	var index Index
	read("index.json", &index)
	check("sources", index.Sources, Sources{CodeHost: "fakehost", Catalog: "fakecatalog", IssueTracker: "faketracker"})
	check("index login", index.Login, "jdoe")
	check("index catalog summary", index.Repos[0].Catalog, []IndexEntity{{Ref: "service:repo", Category: "component",
		Title: ptr("Repo service"), Description: ptr("Does things")}})
	check("index totals", index.Totals.Commits, 8)
	var projects []map[string]any
	read("org/issue_projects.json", &projects)
	check("issue projects", projects[0]["key"], "ENG")
	var verification map[string]any
	read("org/verification.json", &verification)
	check("attributed", verification["host_attributed_commits"], float64(2))
	check("missing", verification["attributed_commits_missing_from_dataset"], float64(1))
	var identities struct {
		Identities []IdentityEntry `json:"identities"`
	}
	read("org/identities.json", &identities)
	if len(identities.Identities) == 0 || deref(identities.Identities[0].MatchedByConfig) != "email" {
		t.Errorf("identities: %+v", identities)
	}
	if !fileExists(filepath.Join(raw, "README.md")) {
		t.Error("schema doc not written")
	}
	b, _ := json.Marshal(index.Repos[0].Files)
	check("index files", string(b), `{"commits":"repos/repo/commits.jsonl","prs":"repos/repo/prs.jsonl"}`)
}
