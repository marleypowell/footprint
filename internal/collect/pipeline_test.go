package collect

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/marleypowell/footprint/internal/catalog"
	"github.com/marleypowell/footprint/internal/codehost"
	"github.com/marleypowell/footprint/internal/tracker"
	"github.com/marleypowell/footprint/internal/wiki"
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
	committed := f.commit(otherByMe, "Merged in feature/c (pull request #12)\n\nadd c") // completed on an earlier host
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
			{Repo: "repo", Number: 7, Title: "side work, see ENG-404", MergeCommitSHA: &merge},
		},
		attributed: []codehost.AttributedCommit{
			{Repo: "repo", SHA: authored, AuthorName: "Jane Doe", AuthorEmail: "jane.doe@example.com"},
			{Repo: "repo", SHA: "0000000000000000000000000000000000000000", Date: "2023-01-01T00:00:00Z", Subject: "lost"},
		},
	}
	jane, colleague := &tracker.User{ID: "u-jane", Name: "Jane Doe"}, &tracker.User{ID: "u-other", Name: "Other Person"}
	eng := tracker.ProjectRef{Key: "ENG", Name: "Engineering"}
	issues := &fakeTracker{
		account:  tracker.Account{ID: "u-jane", Name: "Jane Doe", Email: "jane.doe@example.com"},
		assigned: []string{"ENG-7", "ENG-99", "OPS-5"},
		issues: []tracker.Issue{
			{Key: "ENG-7", Project: eng, Summary: "Planned work", CreatedAt: "2024-01-01T00:00:00+00:00", UpdatedAt: "2024-02-01T00:00:00+00:00",
				History:  []tracker.Change{{ID: "h1", Author: jane, Items: []tracker.ChangeItem{{Field: "status", To: ptr("Done")}}}},
				Worklogs: []tracker.Worklog{{ID: "w1", Author: jane, Seconds: 3600}}},
			{Key: "ENG-12", Project: eng, Summary: "Add a", Reporter: colleague, CreatedAt: "2024-01-02T00:00:00+00:00"},
			{Key: "ENG-42", Project: eng, Summary: "Rename a", Assignee: colleague, Reporter: jane, Creator: jane,
				History: []tracker.Change{{ID: "h2", Author: colleague, Items: []tracker.ChangeItem{{Field: "assignee", ToID: ptr("u-jane")}}}}},
			{Key: "ENG-99", PreviousKeys: []string{"ENG-34"}, Project: eng, Summary: "Moved from another team", Assignee: jane,
				Comments: []tracker.Comment{{ID: "c1", Author: jane, Body: "done"}, {ID: "c2", Author: colleague, Body: "thanks"}}},
			{Key: "OPS-5", Project: tracker.ProjectRef{Key: "OPS", Name: "Operations"}, Summary: "On call", Assignee: jane},
		},
	}
	wikiJane, wikiOther := &wiki.User{ID: "u-jane", Name: "Jane Doe"}, &wiki.User{ID: "u-other", Name: "Other Person"}
	engSpace := wiki.Space{Key: "eng", Name: "Engineering"}
	notes := &fakeWiki{account: wiki.Account{ID: "u-jane", Name: "Jane Doe"}, pages: []wiki.Page{
		{ID: "9a2d-design", Type: "page", Title: "Design notes", Space: engSpace, CreatedAt: "2024-03-01T00:00:00+00:00",
			CreatedBy: wikiJane, Body: ptr("# Design"), Versions: []wiki.Version{{Number: 1, Author: wikiJane}, {Number: 2, Author: wikiOther}}},
		{ID: "6f1c-team", Type: "page", Title: "Team wiki", Space: engSpace, CreatedAt: "2023-05-01T00:00:00+00:00", CreatedBy: wikiOther,
			Versions: []wiki.Version{{Number: 1, Author: wikiOther}, {Number: 2, Author: wikiJane}, {Number: 3, Author: wikiJane}}},
		{ID: "b7e0-roadmap", Type: "database", Title: "Roadmap", Space: wiki.Space{Key: "product", Name: "Product"},
			CreatedBy: wikiJane, Owner: wikiJane, Versions: []wiki.Version{{Number: 1, Author: wikiJane}}},
		// Edited by the user once, but it keeps only its current version, saved by someone else.
		{ID: "c4a1-sketch", Type: "whiteboard", Title: "Sketch", Space: wiki.Space{Key: "product", Name: "Product"},
			CreatedAt: "2024-05-01T00:00:00+00:00", CreatedBy: wikiOther, Version: 5, Versions: []wiki.Version{{Number: 5, Author: wikiOther}}},
	}}
	cfg := &Config{
		Org: "org", Login: "jdoe",
		Identity:     IdentityConfig{Emails: []string{"jane.doe@example.com"}, Names: []string{"Jane Doe"}, DiscoveryPattern: "jane|doe"},
		CodeHost:     Tool{Kind: "fakehost"},
		Catalog:      Tool{Kind: "fakecatalog"},
		IssueTracker: &Tool{Kind: "faketracker"},
		Wiki:         &Tool{Kind: "fakewiki"},
		Mirror:       MirrorConfig{Parallelism: 2, FetchPullRefs: true},
	}
	raw, cache := filepath.Join(root, "raw"), filepath.Join(root, "cache")
	stale := filepath.Join(raw, "issues", "GONE.jsonl") // a project the user no longer has issues in
	if err := writeFileAtomic(stale, []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	opts := Options{DataDir: raw, CacheDir: cache}
	if err := Run(context.Background(), opts, cfg, Tools{Host: host, Catalog: fakeCatalog{}, Tracker: issues, Wiki: notes}, StageOrder); err != nil {
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
	check("migrated PR numbers (earlier host's convention)", got[committed].MigratedPRNumbersInMessage, []int{12})
	check("migrated PR numbers are not the host's", got[committed].PRNumbersInMessage, []int{})
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
	check("merge migrated PR numbers (none: [] not null)", c.MigratedPRNumbersInMessage, []int{})
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
	check("sources", index.Sources, Sources{CodeHost: "fakehost", Catalog: "fakecatalog", IssueTracker: ptr("faketracker"), Wiki: ptr("fakewiki")})
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

	// issues/: assigned at some point (by the tracker's search, or its history), or named in git work
	records, err := readJSONL[IssueRecord](filepath.Join(raw, "issues", "ENG.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	byKey, order := map[string]IssueRecord{}, []string{}
	for _, r := range records {
		byKey[r.Key], order = r, append(order, r.Key)
	}
	check("issues, by number", order, []string{"ENG-7", "ENG-12", "ENG-42", "ENG-99"})
	check("roles: assigned per the search, edited, logged work", byKey["ENG-7"].Roles, []string{"assignee", "editor", "worklogger"})
	check("roles: only named in a commit", byKey["ENG-12"].Roles, []string{"referenced"})
	check("roles: assigned per the history", byKey["ENG-42"].Roles, []string{"assignee", "reporter", "creator", "referenced"})
	check("roles: moved issue, named by its old key", byKey["ENG-99"].Roles, []string{"assignee", "commenter", "referenced"})
	check("commit references", byKey["ENG-12"].ReferencedBy, References{Commits: []CommitRef{{Repo: "repo", SHA: authored}}, PRs: []PRRef{}})
	check("PR references", byKey["ENG-42"].ReferencedBy.PRs, []PRRef{{Repo: "repo", Number: 3}})
	onBranch := []CommitRef{{Repo: "repo", SHA: renamed}, {Repo: "repo", SHA: binary}, {Repo: "repo", SHA: pushedToMyPR}}
	sort.Slice(onBranch, func(i, j int) bool { return onBranch[i].SHA < onBranch[j].SHA })
	check("old-key references (the feature/ENG-34 branch)", byKey["ENG-99"].ReferencedBy.Commits, onBranch)
	comments := byKey["ENG-99"].Comments
	check("comments by_user", []bool{comments[0].ByUser, comments[1].ByUser}, []bool{true, false})
	check("changes by_user", byKey["ENG-7"].History[0].ByUser, true)
	if ops, err := readJSONL[IssueRecord](filepath.Join(raw, "issues", "OPS.jsonl")); err != nil || len(ops) != 1 {
		t.Errorf("OPS.jsonl: %d records, %v", len(ops), err)
	}
	if fileExists(stale) {
		t.Error("a stale project file was kept")
	}
	var unresolved []UnresolvedKey
	read("issues/unresolved.json", &unresolved)
	check("unresolved keys", unresolved, []UnresolvedKey{{Missing: tracker.Missing{Key: "ENG-404", Reason: tracker.NotFound},
		ReferencedBy: References{Commits: []CommitRef{}, PRs: []PRRef{{Repo: "repo", Number: 7}}}}})

	// wiki/: content the user created or edited, oldest first per space
	pages, err := readJSONL[PageRecord](filepath.Join(raw, "wiki", "eng.jsonl"))
	if err != nil || len(pages) != 2 {
		t.Fatalf("eng.jsonl: %d pages, %v", len(pages), err)
	}
	check("edited page", []any{pages[0].Title, pages[0].Roles, pages[0].UserVersions}, []any{"Team wiki", []string{"editor"}, 2})
	check("created page", []any{pages[1].Title, pages[1].Roles, pages[1].UserVersions}, []any{"Design notes", []string{"creator"}, 1})
	check("versions by_user", []bool{pages[1].Versions[0].ByUser, pages[1].Versions[1].ByUser}, []bool{true, false})
	product, err := readJSONL[PageRecord](filepath.Join(raw, "wiki", "product.jsonl"))
	if err != nil || len(product) != 2 {
		t.Fatalf("product.jsonl: %d pages, %v", len(product), err)
	}
	check("created and owned database", product[0].Roles, []string{"creator", "owner"})
	check("whiteboard without the user's version", []any{product[1].Roles, product[1].UserVersions}, []any{[]string{"contributor"}, 0})

	// The issues and pages stages ran after projects, and refreshed their parts of the index.
	read("index.json", &index)
	check("totals", []int{index.Totals.Issues, index.Totals.WikiPages, index.Totals.Commits}, []int{5, 4, 8})
	b, _ = json.Marshal(index.IssuesByProject[0])
	check("issue project", string(b), `{"key":"ENG","name":"Engineering","file":"issues/ENG.jsonl","records":4,`+
		`"by_role":{"assignee":3,"reporter":1,"creator":1,"commenter":1,"editor":1,"worklogger":1,"referenced":3},`+
		`"first_created_at":"2024-01-01T00:00:00+00:00","last_updated_at":"2024-02-01T00:00:00+00:00"}`)
	check("issue projects", len(index.IssuesByProject), 2) // the stale GONE.jsonl the projects stage saw is gone
	check("wiki spaces", []string{index.WikiBySpace[0].File, index.WikiBySpace[1].File}, []string{"wiki/eng.jsonl", "wiki/product.jsonl"})
	if index.Repos[0].CommitsByRoles == nil || len(index.Repos[0].CommitsByRoles.Keys()) == 0 {
		t.Error("refreshing the index lost the repos' commits_by_roles")
	}

	// Without an issue tracker in the config, git work names no issues and the issues stage does
	// nothing; the issues/ files already collected stay.
	cfg.IssueTracker = nil
	noTracker := Tools{Host: host, Catalog: fakeCatalog{}, Wiki: notes}
	if err := Run(context.Background(), opts, cfg, noTracker, []string{"prs", "commits", "projects", "issues"}); err != nil {
		t.Fatal(err)
	}
	if rows, err = readJSONL[Commit](filepath.Join(raw, "repos", "repo", "commits.jsonl")); err != nil || len(rows) != 8 {
		t.Fatalf("commits without an issue tracker: %d, %v", len(rows), err)
	}
	for _, r := range rows {
		check("issue keys without an issue tracker", r.IssueKeys, []string{})
	}
	if prs, err = readJSONL[PullRequestRecord](filepath.Join(raw, "repos", "repo", "prs.jsonl")); err != nil {
		t.Fatal(err)
	}
	check("PR issue keys without an issue tracker", prs[0].IssueKeys, []string{})
	read("index.json", &index)
	check("sources without an issue tracker", index.Sources.IssueTracker, (*string)(nil))
	if fileExists(filepath.Join(raw, "org", "issue_projects.json")) {
		t.Error("issue_projects.json outlived the issue tracker")
	}
	if !fileExists(filepath.Join(raw, "issues", "ENG.jsonl")) {
		t.Error("the issues stage removed issues without an issue tracker")
	}
}

// When the tracker loads none of the issues, the stage fails and keeps the previous files.
func TestIssuesKeepsDataWhenNothingLoads(t *testing.T) {
	raw := t.TempDir()
	previous := filepath.Join(raw, "issues", "ENG.jsonl")
	if err := writeFileAtomic(previous, []byte(`{"key":"ENG-1"}`+"\n")); err != nil {
		t.Fatal(err)
	}
	tr := &fakeTracker{account: tracker.Account{ID: "u-jane"}, assigned: []string{"ENG-1", "ENG-2"}}
	h := &harness{raw: raw, tools: Tools{Tracker: tr}, id: &identity{emails: map[string]bool{}}}
	if err := h.issues(context.Background()); err == nil || !strings.Contains(err.Error(), "none of the 2 issues") {
		t.Errorf("want a refusal, got %v", err)
	}
	if !fileExists(previous) {
		t.Error("the previous issues were removed")
	}
}

func TestNaturalCompare(t *testing.T) {
	keys := []string{"ENG-10", "ENG-9", "AB-100", "ENG-009", "ENG-1a", "ENG-1"}
	slices.SortFunc(keys, naturalCompare)
	if want := []string{"AB-100", "ENG-1", "ENG-1a", "ENG-009", "ENG-9", "ENG-10"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("got %v, want %v", keys, want)
	}
}

func TestFileName(t *testing.T) {
	for key, want := range map[string]string{
		"ENG": "ENG", "~557058:ab-c": "~557058_ab-c", "": "_", "..": "_..", "a/../b": "a_.._b",
		"CON": "_CON", "aux": "_aux", "COM1": "_COM1", "LPT9": "_LPT9", "NUL.x": "_NUL.x",
		"CONF": "CONF", "COM": "COM", "COM10": "COM10", "PRNT": "PRNT",
	} {
		if got := fileName(key); got != want {
			t.Errorf("fileName(%q) = %q, want %q", key, got, want)
		}
	}
}

// Without a wiki in the config, the pages stage does nothing.
func TestPagesWithoutWiki(t *testing.T) {
	raw := t.TempDir()
	h := &harness{raw: raw, tools: Tools{}}
	if err := h.pages(context.Background()); err != nil || fileExists(filepath.Join(raw, "wiki")) {
		t.Errorf("err %v; wiki/ written: %v", err, fileExists(filepath.Join(raw, "wiki")))
	}
}
