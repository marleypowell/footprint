package collect

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/marleypowell/footprint/internal/catalog"
)

// The catalog stage asks the adapter what each mirrored repo's default branch declares. A
// descriptor's repo reference resolves to an org repo through the code host's URL convention,
// ignoring case; one naming no repo in the org resolves to none.
func TestCatalog(t *testing.T) {
	api, hub := newWorkRepo(t), newWorkRepo(t)
	api.write("cortex.json", `[{"tag": "service:api", "category": "component", "repo": "https://code.example/acme/api"},
	                          {"tag": "system:shop", "category": "system"}]`)
	api.write("sub/cortex.json", `[{"tag": `) // unparseable
	api.commit(me, "describe api")
	hub.write("cortex.json", `[{"tag": "service:api-docs", "category": "component", "repo": "https://code.example/ACME/Api"},
	                          {"tag": "service:gone", "category": "component", "repo": "https://code.example/acme/gone"},
	                          {"tag": "service:elsewhere", "category": "component", "repo": "https://other.example/acme/api"}]`)
	hub.commit(me, "describe others")

	h := newTestHarness(t, Tools{Host: &fakeHost{}, Catalog: fakeCatalog{}})
	noBranch := testRepo("no-branch")
	noBranch.DefaultBranch = nil // an empty repo, say
	writeInventory(t, h, testRepo("hub"), testRepo("api"), noBranch, testRepo("unmirrored"))
	api.mirrorTo(h.mirrorPath("api"))
	hub.mirrorTo(h.mirrorPath("hub"))
	api.mirrorTo(h.mirrorPath("no-branch"))
	stale := filepath.Join(h.raw, "catalog", "files", "old", "cortex.json")
	if err := writeFileAtomic(stale, []byte("[]")); err != nil {
		t.Fatal(err)
	}
	if err := h.catalog(context.Background()); err != nil {
		t.Fatal(err)
	}

	entities, err := readJSONL[catalog.Entity](filepath.Join(h.raw, "catalog", "entities.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ ref, foundIn, describes string }
	var got []row
	for _, e := range entities {
		got = append(got, row{e.Ref, e.FoundInRepo, deref(e.SourceRepo)})
	}
	want := []row{ // by the repo holding the descriptor, then its path, then its place in the file
		{"service:api", "api", "api"},
		{"system:shop", "api", ""},
		{"service:api-docs", "hub", "api"},
		{"service:gone", "hub", ""},
		{"service:elsewhere", "hub", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entities = %+v, want %+v", got, want)
	}
	var parseErrors []parseError
	if ok, err := readJSON(filepath.Join(h.raw, "catalog", "parse_errors.json"), &parseErrors); !ok || err != nil ||
		len(parseErrors) != 1 || parseErrors[0].Repo != "api" || parseErrors[0].Path != "sub/cortex.json" {
		t.Errorf("parse errors = %+v, %v %v; want sub/cortex.json in api", parseErrors, ok, err)
	}
	for _, kept := range []string{"api/cortex.json", "api/sub/cortex.json", "hub/cortex.json"} {
		if !fileExists(filepath.Join(h.raw, "catalog", "files", kept)) {
			t.Errorf("raw descriptor %s not kept", kept)
		}
	}
	if fileExists(filepath.Join(h.raw, "catalog", "files", "no-branch")) {
		t.Error("a repo without a default branch was scanned")
	}
	if fileExists(stale) {
		t.Error("a descriptor from an earlier run was kept")
	}
}

func TestCatalogWithNothingDeclared(t *testing.T) {
	h := newTestHarness(t, Tools{Host: &fakeHost{}, Catalog: fakeCatalog{}})
	writeInventory(t, h)
	if err := h.catalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	entities, _ := os.ReadFile(filepath.Join(h.raw, "catalog", "entities.jsonl"))
	errs, _ := os.ReadFile(filepath.Join(h.raw, "catalog", "parse_errors.json"))
	if len(entities) != 0 || string(errs) != "[]\n" {
		t.Errorf("entities.jsonl %q, parse_errors.json %q; want empty, and []", entities, errs)
	}
}
