package facts

import (
	"path/filepath"
	"strings"
	"testing"
)

// A search finds every kind of record by its ids, title and the text it carries, needs every term,
// ignores case, and narrows by kind, window, repo and item.
func TestSearch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "notes/ENG-2.md", "---\nitem: ENG-2\n---\n\n## Summary\n\nMoved the queue to Kafka.\n")
	writeFile(t, dir, "context/items/ENG-2.md", "We rolled it back once.\n")
	f := build(t, fixture(), Inputs{NotesDir: filepath.Join(dir, "notes"), ContextDir: filepath.Join(dir, "context")})
	tests := []struct {
		name string
		q    Query
		want []string // "<kind> <id>"
	}{
		{"a title, in every kind of record that has it; records with no activity last",
			Query{Terms: []string{"search"}},
			[]string{"item ENG-1", "item ENG-10", "item doc+100", "issue ENG-1", "doc doc+100", "issue ENG-10"}},
		{"every term, in any order and case",
			Query{Terms: []string{"SEARCH", "fast"}},
			[]string{"item ENG-1", "issue ENG-1"}},
		{"a phrase is one term",
			Query{Terms: []string{"search fast"}},
			[]string{"issue ENG-1"}}, // its description: "Make search fast."
		{"an id", Query{Terms: []string{"app#5"}}, []string{"change app#5"}},
		{"a change's description",
			Query{Terms: []string{"users who asked"}},
			[]string{"change app#5", "change app#6", "change app#7"}},
		{"a decision record's text", Query{Terms: []string{"for eng-2."}}, []string{"doc adr+app+0001-use-queues"}},
		{"the user's words on a reviewed PR", Query{Terms: []string{"backoff"}}, []string{"review review+app#21"}},
		{"an item's note", Query{Terms: []string{"kafka"}}, []string{"item ENG-2"}},
		{"the user's own words on an item", Query{Terms: []string{"rolled it back"}}, []string{"item ENG-2"}},
		{"a kind", Query{Terms: []string{"search"}, Kinds: []string{"issue"}}, []string{"issue ENG-1", "issue ENG-10"}},
		{"no terms list every record the filter keeps",
			Query{Kinds: []string{"review"}},
			[]string{"review review+app#21", "review review+app#22", "review review+app#23", "review review+app#24"}},
		{"a window keeps the records with the user's activity in it",
			Query{Terms: []string{"search"}, Filter: Filter{Window: mustWindow(t, "2024-01-05..2024-01-31")}},
			[]string{"item ENG-1", "item ENG-10", "issue ENG-1", "item doc+100", "doc doc+100"}},
		{"a repo keeps the records in it: no wiki pages",
			Query{Terms: []string{"search"}, Filter: Filter{Repo: "app"}},
			[]string{"item ENG-1", "item ENG-10", "issue ENG-1"}},
		{"an item keeps its records (docs naming it, and ADRs its changes wrote), an initiative's features' too",
			Query{Filter: Filter{Item: "ENG-10"}},
			[]string{"item ENG-1", "item ENG-10", "issue ENG-1", "change app@c1", "doc adr+app+0001-use-queues", "doc doc+100",
				"issue ENG-4", "change app#5",
				"review review+app#21", "change app@c12", "issue ENG-10"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits, err := f.Search(tt.q)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, h := range hits {
				got = append(got, h.Kind+" "+h.ID)
			}
			check(t, "hits", got, tt.want)
		})
	}
}

// A hit names the item whose note and pack tell its story, and the user's activity on it in the
// window.
func TestSearchHits(t *testing.T) {
	f := build(t, blamed(fixture()), Inputs{})
	tests := []struct {
		name string
		q    Query
		want Hit
	}{
		{"a change, by its item",
			Query{Terms: []string{"app#5"}},
			Hit{Kind: "change", ID: "app#5", Item: "ENG-1", First: "2024-01-03", Last: "2024-01-05", Title: "ENG-1: feature",
				URL: "https://code.example/org/app/pull/5"}},
		{"its span is the window's",
			Query{Terms: []string{"app#5"}, Filter: Filter{Window: mustWindow(t, "2024-01-04..2024-01-31")}},
			Hit{Kind: "change", ID: "app#5", Item: "ENG-1", First: "2024-01-04", Last: "2024-01-05", Title: "ENG-1: feature",
				URL: "https://code.example/org/app/pull/5"}},
		{"an epic is its own initiative's",
			Query{Terms: []string{"search"}, Kinds: []string{"issue"}, Filter: Filter{Item: "ENG-10"}},
			Hit{Kind: "issue", ID: "ENG-10", Item: "ENG-10", Title: "Search", URL: "https://tracker.example/browse/ENG-10"}},
		{"an area, with no item or activity",
			Query{Terms: []string{"the api"}, Kinds: []string{"area"}},
			Hit{Kind: "area", ID: "own+app+src/", Title: "The API in app/src/, top", URL: "https://code.example/org/app"}},
		{"a review naming none of the user's items has none",
			Query{Terms: []string{"theirs 22"}},
			Hit{Kind: "review", ID: "review+app#22", First: "2024-02-10", Last: "2024-02-10", Title: "theirs 22",
				URL: "https://code.example/org/app/pull/22"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits, err := f.Search(tt.q)
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) == 0 {
				t.Fatal("no hits")
			}
			check(t, "hit", hits[len(hits)-1], tt.want)
		})
	}
}

// An area is found by its repo, path, catalog entity, the user's role and the colleagues in it. It
// is as of the repos' heads, with no activity and no item: a window or an item leaves it out.
func TestSearchAreas(t *testing.T) {
	f := build(t, blamed(fixture()), Inputs{})
	area := []string{"area"}
	tests := []struct {
		name string
		q    Query
		want []string
	}{
		{"by its path", Query{Terms: []string{"lib/"}, Kinds: area}, []string{"area own+app+lib/"}},
		{"by its catalog entity", Query{Terms: []string{"the api"}, Kinds: area}, []string{"area own+app+src/"}},
		{"by the user's role", Query{Terms: []string{"top"}, Kinds: area}, []string{"area own+app+src/"}},
		{"by a colleague in it", Query{Terms: []string{"sam"}, Kinds: area}, []string{"area own+app", "area own+app+src/"}},
		{"after every record with activity", Query{Terms: []string{"app"}, Kinds: []string{"review", "area"}},
			[]string{"review review+app#21", "review review+app#22", "review review+app#23", "review review+app#24",
				"area own+app", "area own+app+docs/", "area own+app+lib/", "area own+app+src/"}},
		{"a repo keeps its areas", Query{Kinds: area, Filter: Filter{Repo: "app"}},
			[]string{"area own+app", "area own+app+docs/", "area own+app+lib/", "area own+app+src/"}},
		{"a window leaves them out", Query{Kinds: area, Filter: Filter{Window: mustWindow(t, "2024-01-01..2024-12-31")}}, nil},
		{"an item leaves them out", Query{Kinds: area, Filter: Filter{Item: "ENG-1"}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits, err := f.Search(tt.q)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, h := range hits {
				got = append(got, h.Kind+" "+h.ID)
			}
			check(t, "hits", got, tt.want)
		})
	}
}

// An area's title says where it is and the user's role in it.
func TestAreaTitle(t *testing.T) {
	tests := []struct {
		name string
		area Area
		want string
	}{
		{"a repo", Area{Kind: "repo", Repo: "pay", Role: RoleMajor}, "pay, major"},
		{"a directory", Area{Kind: "dir", Repo: "pay", Path: "client/", Role: RoleTop}, "pay/client/, top"},
		{"a catalog entity", Area{Kind: "entity", Repo: "pay", Path: "api/", Role: RoleContributor, Entity: &Ref{ID: "component:default/api", Title: "The API"}},
			"The API in pay/api/, contributor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { check(t, "areaTitle", areaTitle(&tt.area), tt.want) })
	}
}

func TestHitString(t *testing.T) {
	tests := []struct {
		name string
		hit  Hit
		want string
	}{
		{"with activity", Hit{Kind: "change", ID: "app#5", Item: "ENG-1", First: "2024-01-03", Last: "2024-01-05", Title: "ENG-1:\n feature", URL: "https://x/5"},
			"change\tapp#5\tENG-1\t2024-01-03..2024-01-05\tENG-1: feature\thttps://x/5"},
		{"without activity or an item", Hit{Kind: "issue", ID: "ENG-10", Title: "Search"},
			"issue\tENG-10\t\t\tSearch\t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { check(t, "String()", tt.hit.String(), tt.want) })
	}
}

func TestSearchRejects(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	tests := []struct {
		name string
		q    Query
		want string
	}{
		{"an unknown kind", Query{Kinds: []string{"page"}}, `kind "page"`},
		{"a repo the dataset does not have", Query{Filter: Filter{Repo: "nope"}}, `no repo "nope"`},
		{"an item the facts do not have", Query{Filter: Filter{Item: "ENG-404"}}, `no work item "ENG-404"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.Search(tt.q)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to say %s", err, tt.want)
			}
		})
	}
}
