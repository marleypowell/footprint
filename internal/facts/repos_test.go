package facts

import (
	"testing"

	"github.com/marleypowell/footprint/internal/catalog"
	"github.com/marleypowell/footprint/internal/collect"
)

func TestRepos(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	r := f.Repos[0]
	check(t, "a repo", []any{r.Name, r.First, r.Last, r.StartedByMe, r.Totals.ChangesLanded, r.Systems, r.Domains},
		[]any{"app", "2024-01-02", "2024-05-01", true, 3, []string{}, []string{}})
	check(t, "its kinds of file: generated and bulk files left out", r.Extensions,
		[]Extension{{Ext: ".go", Files: 3, Added: 60}})
	check(t, "its items, most days first", r.Items[:2], []string{"ENG-1", "repo+app+2024q2"})
}

// The user started a repo when their first commit in it is on the day of its first commit.
func TestRepoStartedByMe(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first *string
		want  bool
	}{
		{"the user's commit is the first", ptr("2024-01-02T23:00:00Z"), true},
		{"the repo is older", ptr("2023-06-01T00:00:00Z"), false},
		{"its first commit is unknown", nil, false},
		{"unreadable", ptr("long ago"), false},
	} {
		raw := oneRepo([]collect.Commit{
			commit("c1", "2024-01-02T10:00:00Z", jane, []string{"author"}, "start", nil),
			commit("c0", "2023-06-01T00:00:00Z", other, []string{"committer"}, "theirs, committed by the user", nil),
		}, nil, nil)
		raw.Index.Repos[0].FirstCommitInRepo = tc.first
		check(t, tc.name, build(t, raw, Inputs{}).Repos[0].StartedByMe, tc.want)
	}
}

// A repo's systems and domains are the catalog's, by title (or ref), from its own entities and
// those they belong to.
func TestRepoCatalog(t *testing.T) {
	raw := oneRepo(nil, nil, nil)
	p := &raw.Projects[0]
	p.CodeHost.Description, p.CodeHost.IsArchived = ptr("The app"), true
	p.Catalog = collect.ProjectCatalog{
		Entities: []catalog.Entity{
			{Ref: "system:search", Category: catalog.System, Title: ptr("Search")},
			{Ref: "component:api", Category: catalog.Component, Title: ptr("API")},
		},
		Context: map[string]*catalog.Entity{
			"domain:discovery": {Ref: "domain:discovery", Category: catalog.Domain},
			"system:search":    {Ref: "system:search", Category: catalog.System, Title: ptr("Search")},
			"system:gone":      nil, // referenced, defined nowhere
		},
	}
	r := build(t, raw, Inputs{}).Repos[0]
	check(t, "a repo's catalog", []any{r.Description, r.Archived, r.Systems, r.Domains, r.First},
		[]any{"The app", true, []string{"Search"}, []string{"domain:discovery"}, ""})
}

// A repo's kinds of file are those its landed changes of the user's touched (not co-authored ones,
// nor generated or bulk files), by extension or, without one, by name; most lines added first, then
// most files.
func TestRepoExtensions(t *testing.T) {
	change := func(repo string, landed bool, class string, files ...fileChange) *Change {
		return &Change{Repo: repo, Landed: landed, Class: class, files: files}
	}
	f := &Facts{Changes: []*Change{
		change("app", true, Self, newFileChange("a.go", 10, 0), newFileChange("b.GO", 5, 0), newFileChange("Dockerfile", 3, 0),
			newFileChange("go.sum", 900, 0), newFileChange("data.json", 6000, 0)),
		change("app", true, Agent, newFileChange("a.go", 20, 0), newFileChange("web/x.ts", 35, 0)),
		change("app", false, Self, newFileChange("draft.rs", 500, 0)),
		change("app", true, CoAuthor, newFileChange("theirs.py", 500, 0)),
		change("lib", true, Self, newFileChange("lib.c", 500, 0)),
		change("app", true, Self, newFileChange("ci/Makefile", 3, 0), newFileChange("x.md", 1, 0), newFileChange("y.md", 1, 0)),
	}}
	check(t, "extensions", f.extensions("app"), []Extension{
		{Ext: ".go", Files: 2, Added: 35}, {Ext: ".ts", Files: 1, Added: 35}, {Ext: "Dockerfile", Files: 1, Added: 3},
		{Ext: "Makefile", Files: 1, Added: 3}, {Ext: ".md", Files: 2, Added: 2}})
	var many []fileChange
	for i := range maxRepoExtensions + 3 {
		many = append(many, newFileChange("f."+string(rune('a'+i)), 1, 0))
	}
	f.Changes = []*Change{change("app", true, Self, many...)}
	check(t, "at most a dozen", len(f.extensions("app")), maxRepoExtensions)
}

// A repo's items are those with the most of the user's active days in it.
func TestRepoItems(t *testing.T) {
	var evs []event
	add := func(item, kind string, days ...string) {
		for _, d := range days {
			evs = append(evs, event{at: mustTime(t, d+"T10:00:00Z"), kind: kind, item: item})
		}
	}
	add("ENG-2", evActive, "2024-01-01", "2024-01-02", "2024-01-02") // one day twice: two days
	add("ENG-1", evActive, "2024-01-03", "2024-01-04")
	add("ENG-3", evActive, "2024-01-05", "2024-01-06", "2024-01-07")
	add("ENG-4", evCommit, "2024-01-01", "2024-01-02", "2024-01-03", "2024-01-04") // no active days
	add("", evActive, "2024-01-08", "2024-01-09", "2024-01-10", "2024-01-11")      // counted toward no item
	f := &Facts{}
	check(t, "items, most days first, then by id", f.repoItems(evs), []string{"ENG-3", "ENG-1", "ENG-2"})
	check(t, "none", f.repoItems(nil), []string{})
	evs = nil
	for i := range maxRepoItems + 2 {
		add("I-"+itoa(i), evActive, "2024-01-01")
	}
	check(t, "at most ten", len(f.repoItems(evs)), maxRepoItems)
}
