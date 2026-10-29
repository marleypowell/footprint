package collect

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/catalog"
	"github.com/marleypowell/footprint/internal/codehost"
	"github.com/marleypowell/footprint/internal/doc"
)

func objectEntries(o *doc.Object) [][2]any {
	var out [][2]any
	for _, k := range o.Keys() {
		v, _ := o.Get(k)
		out = append(out, [2]any{k, v})
	}
	return out
}

// A repo's entities list systems first, then components, APIs, resources and the rest; within a
// category, descriptors nearer the root first, then by path and place in the file.
func TestOwnEntities(t *testing.T) {
	api, web := ptr("api"), ptr("web")
	entities := []catalog.Entity{
		{Ref: "resource:db", Category: catalog.Resource, Path: "catalog-info.yaml", SourceRepo: api},
		{Ref: "group:otters", Category: catalog.Group, Path: "catalog-info.yaml", SourceRepo: api},
		{Ref: "component:deep", Category: catalog.Component, Path: "services/x/catalog-info.yaml", SourceRepo: api},
		{Ref: "component:second", Category: catalog.Component, Path: "catalog-info.yaml", DocIndex: 2, SourceRepo: api},
		{Ref: "component:first", Category: catalog.Component, Path: "catalog-info.yaml", DocIndex: 1, SourceRepo: api},
		{Ref: "system:shop", Category: catalog.System, Path: "z/deep/catalog-info.yaml", SourceRepo: api},
		{Ref: "system:web", Category: catalog.System, SourceRepo: web}, // another repo's
		{Ref: "component:unresolved", Category: catalog.Component},     // describes no known repo
		{Ref: "api:rest", Category: catalog.API, Path: "api.yaml", SourceRepo: api},
		{Ref: "component:by-path", Category: catalog.Component, Path: "api.yaml", SourceRepo: api},
	}
	var got []string
	for _, e := range ownEntities(entities, "api") {
		got = append(got, e.Ref)
	}
	want := []string{"system:shop", "component:by-path", "component:first", "component:second", "component:deep",
		"api:rest", "resource:db", "group:otters"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ownEntities = %q, want %q", got, want)
	}
}

// A repo's context is what its entities are part of, followed up the chain (component -> system ->
// domain), with null for a ref no descriptor defines. It stops at cycles and leaves out the repo's
// own entities.
func TestRelatedEntities(t *testing.T) {
	own := []*catalog.Entity{{Ref: "component:api", PartOf: []string{"system:shop", "group:otters"}}}
	shop := &catalog.Entity{Ref: "system:shop", PartOf: []string{"domain:retail", "component:api"}}
	retail := &catalog.Entity{Ref: "domain:retail", PartOf: []string{"system:shop"}}
	byRef := map[string]*catalog.Entity{"system:shop": shop, "domain:retail": retail, "component:api": own[0]}
	got := relatedEntities(own, byRef)
	want := map[string]*catalog.Entity{"system:shop": shop, "domain:retail": retail, "group:otters": nil}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("relatedEntities = %v, want %v", got, want)
	}
}

func TestParentDir(t *testing.T) {
	for p, want := range map[string]string{"catalog-info.yaml": "", "services/api/catalog-info.yaml": "services/api", "a/b": "a"} {
		if got := parentDir(p); got != want {
			t.Errorf("parentDir(%q) = %q, want %q", p, got, want)
		}
	}
}

// Activity counts what the user authored (lines only for non-merges), where it landed, and when;
// role combinations most common first; and the issue keys of every commit and PR.
func TestSummarizeActivity(t *testing.T) {
	stats := func(add, del int) *CommitStats { return &CommitStats{Additions: add, Deletions: del} }
	at := func(date string) Signature { return Signature{Date: date} }
	ac := []string{"author", "committer"}
	commits := []Commit{
		{Roles: ac, OnDefaultBranch: true, Stats: stats(10, 2), Author: at("2024-07-01T01:30:00+02:00"), IssueKeys: []string{"ENG-1"}},
		{Roles: ac, Stats: stats(5, 0), Author: at("2024-01-15T09:00:00Z")}, // only on a branch
		{Roles: []string{"author"}, OnDefaultBranch: true, IsMerge: true, Author: at("2024-06-01T00:00:00Z")},
		{Roles: []string{"committer"}, OnDefaultBranch: true, Stats: stats(100, 100), Author: at("2023-01-01T00:00:00Z"), IssueKeys: []string{"ENG-2"}},
		{Roles: []string{"co-author"}, Stats: stats(7, 7), Author: at("2023-02-01T00:00:00Z")},
		{Roles: ac, OnDefaultBranch: true, Stats: stats(1, 1), Author: at("2024-02-01T00:00:00Z")},
	}
	prs := []PullRequestRecord{
		{PullRequest: codehost.PullRequest{CreatedAt: "2024-02-01T00:00:00Z", MergedAt: ptr("2024-02-02T00:00:00Z")}, IssueKeys: []string{"ENG-1", "ENG-3"}},
		{PullRequest: codehost.PullRequest{CreatedAt: "2023-12-01T00:00:00Z"}},
	}
	a := summarizeActivity(commits, prs)
	counts := []int{a.CommitsTotal, a.Authored, a.AuthoredOnDefaultBranch, a.AuthoredOnlyOnOtherRefs, a.AuthoredMerges,
		a.AuthoredAdditions, a.AuthoredDeletions, a.IssueKeys, a.PRsAuthored, a.PRsMerged}
	if want := []int{6, 4, 3, 1, 1, 16, 3, 3, 2, 1}; !reflect.DeepEqual(counts, want) {
		t.Errorf("total, authored, on default, other refs, merges, +, -, issue keys, PRs, merged = %v, want %v", counts, want)
	}
	if want := [][2]any{{"author+committer", 3}, {"author", 1}, {"committer", 1}, {"co-author", 1}}; !reflect.DeepEqual(objectEntries(a.CommitsByRoles), want) {
		t.Errorf("commits_by_roles = %v, want %v (most common first, ties as first seen)", objectEntries(a.CommitsByRoles), want)
	}
	dates := []string{deref(a.FirstAuthoredAt), deref(a.LastAuthoredAt), deref(a.FirstPRAt), deref(a.LastPRAt)}
	if want := []string{"2024-01-15T09:00:00+00:00", "2024-06-30T23:30:00+00:00", "2023-12-01T00:00:00Z", "2024-02-01T00:00:00Z"}; !reflect.DeepEqual(dates, want) {
		t.Errorf("first/last authored, first/last PR = %q, want %q", dates, want)
	}

	none := summarizeActivity(nil, nil)
	if none.FirstAuthoredAt != nil || none.FirstPRAt != nil || none.CommitsByRoles == nil || len(none.CommitsByRoles.Keys()) != 0 {
		t.Errorf("activity of nothing = %+v, want null dates and empty commits_by_roles", none)
	}
}

// indexGroups summarises each issues/ or wiki/ file: its records, the roles in them in role
// order, and the dates they span, the biggest file first.
func TestIndexGroups(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("OPS.jsonl", `{"project":{"key":"OPS","name":"Operations"},"roles":["assignee"],"created_at":"2024-01-01"}`+"\n")
	write("ENG.jsonl", strings.Join([]string{
		`{"project":{"key":"ENG","name":"Engineering"},"roles":["referenced","assignee"],"created_at":"2024-02-01","updated_at":"2024-03-01"}`,
		`{"project":{"key":"ENG","name":"Engineering"},"roles":["assignee"],"created_at":"2023-05-01","updated_at":"2024-04-01"}`,
		`{"project":{"key":"ENG","name":"Engineering"},"roles":["ancestor"]}`,
	}, "\n"))
	write("EMPTY.jsonl", "")
	write("unresolved.json", "[]")
	groups, err := indexGroups(dir, "issues/", issueRoles, issueGroup)
	if err != nil {
		t.Fatal(err)
	}
	type summary struct {
		key, name, file string
		records         int
		byRole          [][2]any
		first, last     string
	}
	var got []summary
	for _, g := range groups {
		got = append(got, summary{g.Key, g.Name, g.File, g.Records, objectEntries(g.ByRole), deref(g.FirstCreatedAt), deref(g.LastUpdatedAt)})
	}
	want := []summary{
		{"ENG", "Engineering", "issues/ENG.jsonl", 3, [][2]any{{"assignee", 2}, {"referenced", 1}, {"ancestor", 1}}, "2023-05-01", "2024-04-01"},
		{"OPS", "Operations", "issues/OPS.jsonl", 1, [][2]any{{"assignee", 1}}, "2024-01-01", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("indexGroups =\n %+v\nwant\n %+v", got, want)
	}
	if n := records(groups); n != 4 {
		t.Errorf("records = %d, want 4", n)
	}
	if groups, err := indexGroups(filepath.Join(dir, "missing"), "wiki/", pageRoles, spaceGroup); err != nil || groups == nil || len(groups) != 0 {
		t.Errorf("indexGroups(missing dir) = %#v, %v, want [] (not null)", groups, err)
	}
}

// The issues and pages stages refresh their part of index.json, once the projects stage has
// written one.
func TestRefreshIndex(t *testing.T) {
	h := newTestHarness(t, Tools{})
	path := filepath.Join(h.raw, "index.json")
	called := false
	if err := h.refreshIndex(func(*Index) { called = true }); err != nil || called || fileExists(path) {
		t.Errorf("refreshIndex before projects: err %v, updated %v, written %v; want nothing done", err, called, fileExists(path))
	}
	if err := writeJSON(path, Index{GeneratedAt: "2000-01-01T00:00:00+00:00", Org: "acme", Totals: IndexTotals{Commits: 9}}); err != nil {
		t.Fatal(err)
	}
	if err := h.refreshIndex(func(index *Index) { index.Totals.WikiPages = 4 }); err != nil {
		t.Fatal(err)
	}
	var index Index
	if _, err := readJSON(path, &index); err != nil || index.Org != "acme" || index.Totals.Commits != 9 || index.Totals.WikiPages != 4 ||
		index.GeneratedAt == "2000-01-01T00:00:00+00:00" {
		t.Errorf("refreshed index = %+v, %v; want the update, the rest kept, and a new generated_at", index, err)
	}
	if err := writeFileAtomic(path, []byte("{")); err != nil {
		t.Fatal(err)
	}
	if err := h.refreshIndex(func(*Index) {}); err == nil {
		t.Error("refreshIndex of a corrupt index.json should fail")
	}
}

// projectDocs keeps the files that say what a repo is (root README, agent instructions, the docs
// index, package.json, and READMEs beside catalog descriptors), and lists its top two levels.
func TestProjectDocs(t *testing.T) {
	f := newWorkRepo(t)
	big := strings.Repeat("x", maxDocBytes+1)
	for name, content := range map[string]string{
		"README.md": "# api\n", "CLAUDE.md": "bad byte: \xff\n", "AGENTS.md": big, "docs/index.md": "# Docs\n",
		"docs/guide.md": "# Guide\n", "package.json": "{}\n", "services/api/README.md": "# Service\n",
		"services/api/catalog-info.yaml": "kind: Component\n", "lib/README.md": "# Lib\n",
	} {
		f.write(name, content)
	}
	f.commit(me, "add docs")
	h := newTestHarness(t, Tools{})
	repo := testRepo("api")
	f.mirrorTo(h.mirrorPath("api"))
	stale := filepath.Join(h.repoDir("api"), "docs", "OLD.md")
	if err := writeFileAtomic(stale, nil); err != nil {
		t.Fatal(err)
	}
	docs, top, err := h.projectDocs(context.Background(), repo, map[string]bool{"services/api": true})
	if err != nil {
		t.Fatal(err)
	}
	var saved []string
	for _, d := range docs {
		saved = append(saved, d.Path)
	}
	if want := []string{"AGENTS.md", "CLAUDE.md", "README.md", "docs/index.md", "package.json", "services/api/README.md"}; !reflect.DeepEqual(saved, want) {
		t.Errorf("docs = %q, want %q", saved, want)
	}
	if want := (Doc{Path: "AGENTS.md", SavedAs: "docs/AGENTS.md", Bytes: maxDocBytes + 1, Truncated: true}); docs[0] != want {
		t.Errorf("a large doc = %+v, want %+v", docs[0], want)
	}
	if b, _ := os.ReadFile(filepath.Join(h.repoDir("api"), "docs", "AGENTS.md")); len(b) != maxDocBytes {
		t.Errorf("a large doc saved %d bytes, want %d", len(b), maxDocBytes)
	}
	if b, _ := os.ReadFile(filepath.Join(h.repoDir("api"), "docs", "CLAUDE.md")); string(b) != "bad byte: �\n" {
		t.Errorf("invalid UTF-8 saved as %q, want it replaced", b)
	}
	if fileExists(stale) {
		t.Error("a doc from an earlier run was kept")
	}
	if want := []string{"AGENTS.md", "CLAUDE.md", "README.md", "docs/", "lib/", "package.json", "services/", "services/api/"}; !reflect.DeepEqual(top, want) {
		t.Errorf("tree_top = %q, want %q", top, want)
	}

	repo.DefaultBranch = nil
	if docs, top, err := h.projectDocs(context.Background(), repo, nil); err != nil || docs == nil || top == nil || len(docs)+len(top) != 0 {
		t.Errorf("projectDocs without a default branch = %v, %v, %v; want empty lists", docs, top, err)
	}
}

// historyInfo describes the whole mirror: every root (an orphan branch, an imported history), the
// earliest of them, and the commits on all refs.
func TestHistoryInfo(t *testing.T) {
	f := newWorkRepo(t)
	f.write("a.txt", "1\n")
	root := f.commit(other, "root")
	f.write("b.txt", "2\n")
	f.commit(me, "second")
	f.git(me, "checkout", "-q", "--orphan", "gh-pages")
	f.write("index.html", "<p>site</p>\n")
	f.commit(me, "a later root")
	h := newTestHarness(t, Tools{})
	f.mirrorTo(h.mirrorPath("api"))
	status := MirrorStatus{Branches: ptr(2), Tags: ptr(0), PullHeads: ptr(1), FetchedAt: "2024-05-01T00:00:00+00:00"}
	hist, err := h.historyInfo(context.Background(), testRepo("api"), status)
	if err != nil {
		t.Fatal(err)
	}
	want := History{FirstCommitDate: ptr(f.git(me, "log", "-1", "--format=%aI", root)), RootCommits: 2, CommitsAllRefs: 3,
		Branches: ptr(2), Tags: ptr(0), PullRequestRefs: ptr(1), MirroredAt: ptr("2024-05-01T00:00:00+00:00")}
	if !reflect.DeepEqual(*hist, want) {
		t.Errorf("historyInfo = %s, want %s", mustJSON(t, hist), mustJSON(t, want))
	}
	if hist, err := h.historyInfo(context.Background(), testRepo("api"), MirrorStatus{}); err != nil || hist.MirroredAt != nil {
		t.Errorf("historyInfo without a mirror status: mirrored_at %v, %v; want null", hist.MirroredAt, err)
	}
}

// The projects stage indexes the repos with the user's activity, the most active first, and
// drops repo directories nothing of the user's is left in.
func TestProjects(t *testing.T) {
	h := newTestHarness(t, Tools{})
	api, web := testRepo("api"), testRepo("web")
	api.Description = ptr("The API")
	writeInventory(t, h, api, web, testRepo("idle"))
	authored := func(sha, date string) Commit {
		return Commit{Repo: "api", SHA: sha, Roles: []string{"author"}, OnDefaultBranch: true, Author: Signature{Date: date}}
	}
	writeRows(t, h, "repos/api/commits.jsonl", authored("a1", "2024-01-01T00:00:00Z"), authored("a2", "2024-03-01T00:00:00Z"),
		Commit{Repo: "api", SHA: "a3", Roles: []string{"committer"}})
	writeRows(t, h, "repos/api/prs.jsonl", PullRequestRecord{PullRequest: codehost.PullRequest{Repo: "api", Number: 1}})
	pr := func(n int) PullRequestRecord {
		return PullRequestRecord{PullRequest: codehost.PullRequest{Repo: "web", Number: n}}
	}
	writeRows(t, h, "repos/web/prs.jsonl", pr(1), pr(2), pr(3), pr(4))
	writeRows(t, h, "repos/renamed/commits.jsonl", authored("r1", "2023-01-01T00:00:00Z")) // not in the inventory
	if err := writeJSON(filepath.Join(h.repoDir("idle"), "project.json"), Project{}); err != nil {
		t.Fatal(err)
	}
	writeRows(t, h, "catalog/entities.jsonl",
		catalog.Entity{Ref: "component:api", Category: catalog.Component, SourceRepo: ptr("api"), Title: ptr("API")},
		catalog.Entity{Ref: "group:otters", Category: catalog.Group, SourceRepo: ptr("api")},
		catalog.Entity{Ref: "component:${{ values.name }}", Category: catalog.Component, SourceRepo: ptr("api"), IsTemplateSkeleton: true})
	stale := filepath.Join(h.raw, "org", "issue_projects.json")
	if err := writeJSON(stale, []string{}); err != nil {
		t.Fatal(err)
	}
	if err := h.projects(context.Background()); err != nil {
		t.Fatal(err)
	}

	var index Index
	if ok, err := readJSON(filepath.Join(h.raw, "index.json"), &index); !ok || err != nil {
		t.Fatalf("index.json: %v %v", ok, err)
	}
	var names []string
	for _, r := range index.Repos {
		names = append(names, r.Name)
	}
	if want := []string{"web", "api"}; !reflect.DeepEqual(names, want) {
		t.Errorf("index repos = %q, want %q (by authored commits plus PRs)", names, want)
	}
	totals := []any{index.Totals.Repos, index.Totals.Commits, index.Totals.AuthoredCommits, index.Totals.PRsAuthored,
		deref(index.Totals.FirstAuthoredAt), deref(index.Totals.LastAuthoredAt)}
	if want := []any{2, 3, 2, 5, "2024-01-01T00:00:00+00:00", "2024-03-01T00:00:00+00:00"}; !reflect.DeepEqual(totals, want) {
		t.Errorf("totals = %v, want %v", totals, want)
	}
	apiItem := index.Repos[1]
	if want := map[string]string{"commits": "repos/api/commits.jsonl", "prs": "repos/api/prs.jsonl"}; !reflect.DeepEqual(apiItem.Files, want) {
		t.Errorf("api files = %v, want %v", apiItem.Files, want)
	}
	// The index lists the entities that are software; project.json all but template placeholders.
	if want := []IndexEntity{{Ref: "component:api", Category: catalog.Component, Title: ptr("API")}}; !reflect.DeepEqual(apiItem.Catalog, want) ||
		deref(apiItem.Description) != "The API" {
		t.Errorf("api catalog %+v, description %v", apiItem.Catalog, apiItem.Description)
	}
	var project Project
	if ok, err := readJSON(filepath.Join(h.repoDir("api"), "project.json"), &project); !ok || err != nil || len(project.Catalog.Entities) != 2 {
		t.Errorf("api project.json entities = %+v, %v %v; want the component and the group", project.Catalog.Entities, ok, err)
	}
	if project.History != nil || len(project.Docs) != 0 {
		t.Errorf("an unmirrored repo has history %+v, docs %+v; want none", project.History, project.Docs)
	}
	if fileExists(h.repoDir("idle")) {
		t.Error("a repo directory without the user's activity was kept")
	}
	if !fileExists(filepath.Join(h.repoDir("renamed"), "commits.jsonl")) || fileExists(filepath.Join(h.repoDir("renamed"), "project.json")) {
		t.Error("a repo missing from the inventory should be skipped, its data left alone")
	}
	if fileExists(stale) || index.Sources.IssueTracker != nil {
		t.Error("without an issue tracker, issue_projects.json should go and sources.issue_tracker be null")
	}
	if !fileExists(filepath.Join(h.raw, "README.md")) {
		t.Error("the schema doc was not written")
	}
}
