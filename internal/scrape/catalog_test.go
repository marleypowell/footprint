package scrape

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseYAMLDocs(t *testing.T) {
	text := `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: checkout-ui
  description: Takes orders
  annotations:
    github.com/project-slug: acme/web-ui
spec:
  type: website
  lifecycle: production
  created: 2023-01-01
  replicas: 2
  ratio: 0.5
  enabled: true
  missing: ~
---
- not
- a mapping
---
base: &base {a: 1, b: 2}
child:
  <<: *base
  b: 3
`
	docs, err := parseYAMLDocs(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("got %d mapping documents, want 2", len(docs))
	}
	want := `{"apiVersion":"backstage.io/v1alpha1","kind":"Component","metadata":{"name":"checkout-ui",` +
		`"description":"Takes orders","annotations":{"github.com/project-slug":"acme/web-ui"}},` +
		`"spec":{"type":"website","lifecycle":"production","created":"2023-01-01","replicas":2,"ratio":0.5,"enabled":true,"missing":null}}`
	if got := mustJSON(t, docs[0]); got != want {
		t.Errorf("first document:\n got %s\nwant %s", got, want)
	}
	if got := mustJSON(t, field(docs[1], "child")); got != `{"a":1,"b":3}` {
		t.Errorf("merge key: got %s", got)
	}
	if _, err := parseYAMLDocs("key: [unclosed"); err == nil {
		t.Error("invalid YAML should fail")
	}
}

func TestEntityRefs(t *testing.T) {
	doc, err := parseYAMLDocs(`kind: Component
metadata: {name: Checkout-UI}
spec:
  system: storefront
  owner: group:platform/Falcon
  subcomponentOf: component:shell
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := entityRef(field(doc[0], "kind"), field(doc[0], "metadata")); got != "component:default/checkout-ui" {
		t.Errorf("entityRef = %q", got)
	}
	want := []string{"system:default/storefront", "group:platform/falcon", "component:default/shell"}
	if got := specRefs(doc[0]); !reflect.DeepEqual(got, want) {
		t.Errorf("specRefs = %v, want %v", got, want)
	}
}

func TestResolveSourceRepo(t *testing.T) {
	links := newRepoLinks("acme", map[string]string{"web-ui": "web-ui", "pay": "pay"})
	hubs := map[string]bool{"catalog-hub": true}
	doc := func(yaml string) *orderedMap {
		t.Helper()
		docs, err := parseYAMLDocs(yaml)
		if err != nil {
			t.Fatal(err)
		}
		return docs[0]
	}
	tests := []struct {
		name, yaml, foundIn, repo, basis string
	}{
		{"slug wins", "metadata: {annotations: {github.com/project-slug: acme/PAY}}", "catalog-hub", "pay",
			"annotation:github.com/project-slug"},
		{"source location", "metadata: {annotations: {backstage.io/source-location: 'url:https://github.com/acme/pay/tree/main'}}",
			"catalog-hub", "pay", "annotation:backstage.io/source-location"},
		{"file location outside hubs", "metadata: {links: [{url: 'https://github.com/acme/pay'}]}", "own-repo", "own-repo",
			"file_location"},
		{"links inside hubs", "metadata: {links: [{url: 'https://github.com/acme/web-ui.git'}]}", "catalog-hub",
			"web-ui", "metadata.links"},
		{"nothing to go on", "metadata: {name: x}", "catalog-hub", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, basis := resolveSourceRepo(links.signals(doc(tt.yaml)), tt.foundIn, hubs)
			if deref(repo) != tt.repo || deref(basis) != tt.basis {
				t.Errorf("got (%q, %q), want (%q, %q)", deref(repo), deref(basis), tt.repo, tt.basis)
			}
		})
	}
}

func TestWriteUnderRefusesEscapes(t *testing.T) {
	base := filepath.Join(t.TempDir(), "base")
	for _, p := range []string{"../escape.txt", "/tmp/absolute.txt", "a/../../b.txt"} {
		if err := writeUnder(base, p, []byte("x")); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	if entries, _ := os.ReadDir(filepath.Dir(base)); len(entries) != 0 {
		t.Fatalf("refused paths still wrote %v", entries)
	}
	if err := writeUnder(base, "docs/index.md", []byte("x")); err != nil || !fileExists(filepath.Join(base, "docs", "index.md")) {
		t.Errorf("normal path not written: %v", err)
	}
}
