package backstage

import (
	"context"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/catalog"
)

// memSource is an in-memory catalog.Source.
type memSource struct {
	repo  string
	files map[string]string
}

func (m memSource) Repo() string { return m.repo }

func (m memSource) List(context.Context) ([]string, error) {
	var paths []string
	for p := range m.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, nil
}

// Grep approximates git grep: an ERE per line, and "*.ext" pathspecs matching at any depth.
func (m memSource) Grep(_ context.Context, pattern string, globs ...string) ([]string, error) {
	re, err := regexp.Compile("(?m)" + pattern)
	if err != nil {
		return nil, err
	}
	var paths []string
	for p, content := range m.files {
		for _, g := range globs {
			if strings.HasSuffix(p, strings.TrimPrefix(g, "*")) && re.MatchString(content) {
				paths = append(paths, p)
				break
			}
		}
	}
	return paths, nil
}

func (m memSource) Read(_ context.Context, path string) ([]byte, error) {
	return []byte(m.files[path]), nil
}

// resolver knows two repos, by slug or GitHub URL.
func resolver(ref string) (string, bool) {
	for _, repo := range []string{"pay", "web-ui"} {
		if strings.EqualFold(ref, "acme/"+repo) || strings.Contains(ref, "github.com/acme/"+repo) {
			return repo, true
		}
	}
	return "", false
}

func scan(t *testing.T, hubs string, src memSource) catalog.Result {
	t.Helper()
	c, err := New([]byte(`{"kind": "backstage", "hub_repos": ` + hubs + `}`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Scan(context.Background(), src, resolver)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestScan(t *testing.T) {
	res := scan(t, `[]`, memSource{repo: "pay", files: map[string]string{
		"catalog-info.yaml": `apiVersion: backstage.io/v1alpha1
kind: System
metadata: {name: PAY, title: Payments, description: Takes payments}
spec: {owner: pay, domain: billing}
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata: {name: pay-api, description: Handles card callbacks}
spec: {type: service, owner: group:platform/Falcon, system: pay}
`,
		"deploy/values.yaml":            "replicas: 2\n",
		"docs/api.yml":                  "apiVersion: backstage.io/v1alpha1\nkind: API\nmetadata: {name: receiver-api}\n",
		"templates/svc/skeleton/x.yaml": "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: '${{ values.name }}'}\n",
		"broken/catalog-info.yaml":      "apiVersion: backstage.io/v1alpha1\nkind: [unclosed\n",
	}})
	var paths []string
	for _, f := range res.Files {
		paths = append(paths, f.Path)
	}
	wantPaths := []string{"broken/catalog-info.yaml", "catalog-info.yaml", "docs/api.yml", "templates/svc/skeleton/x.yaml"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Errorf("descriptor files %v, want %v (deploy/values.yaml is not a descriptor)", paths, wantPaths)
	}
	if len(res.Errors) != 1 || res.Errors[0].Path != "broken/catalog-info.yaml" {
		t.Errorf("parse errors: %+v", res.Errors)
	}
	if len(res.Entities) != 4 {
		t.Fatalf("got %d entities, want 4", len(res.Entities))
	}
	sys, comp, api, skeleton := res.Entities[0], res.Entities[1], res.Entities[2], res.Entities[3]
	check := func(what string, got, want any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", what, got, want)
		}
	}
	check("system ref", sys.Ref, "system:default/pay")
	check("system category", sys.Category, catalog.System)
	check("system title", *sys.Title, "Payments")
	check("system part_of", sys.PartOf, []string{"domain:default/billing", "group:default/pay"})
	check("component ref", comp.Ref, "component:default/pay-api")
	check("component doc index", comp.DocIndex, 1)
	check("component type", *comp.Type, "service")
	check("component title", comp.Title, (*string)(nil))
	check("component part_of", comp.PartOf, []string{"system:default/pay", "group:platform/falcon"})
	check("api category", api.Category, catalog.API)
	check("skeleton", skeleton.IsTemplateSkeleton, true)
	check("found in", comp.FoundInRepo, "pay")
	check("attribution", *comp.SourceRepo+" "+*comp.SourceRepoBasis, "pay file_location")
	if !strings.HasPrefix(string(sys.Raw), `{"apiVersion":"backstage.io/v1alpha1","kind":"System","metadata":{"name":"PAY"`) {
		t.Errorf("raw entity should keep the document's key order: %s", sys.Raw)
	}
}

func TestAttribution(t *testing.T) {
	descriptor := func(metadata string) string {
		return "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: " + metadata + "\n"
	}
	tests := []struct {
		name, metadata, foundIn, repo, basis string
	}{
		{"slug wins", "{name: x, annotations: {github.com/project-slug: acme/PAY}}", "catalog-hub", "pay",
			"annotation:github.com/project-slug"},
		{"gitlab slug", "{name: x, annotations: {gitlab.com/project-slug: acme/pay}}", "catalog-hub", "pay",
			"annotation:gitlab.com/project-slug"},
		{"source location", "{name: x, annotations: {backstage.io/source-location: 'url:https://github.com/acme/pay/tree/main'}}",
			"catalog-hub", "pay", "annotation:backstage.io/source-location"},
		{"file location outside hubs", "{name: x, links: [{url: 'https://github.com/acme/pay'}]}", "own-repo", "own-repo",
			"file_location"},
		{"links inside hubs", "{name: x, links: [{url: 'https://github.com/acme/web-ui.git'}]}", "catalog-hub",
			"web-ui", "metadata.links"},
		{"nothing to go on", "{name: x}", "catalog-hub", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := scan(t, `["catalog-hub"]`, memSource{repo: tt.foundIn, files: map[string]string{"catalog-info.yaml": descriptor(tt.metadata)}})
			e := res.Entities[0]
			if deref(e.SourceRepo) != tt.repo || deref(e.SourceRepoBasis) != tt.basis {
				t.Errorf("got (%q, %q), want (%q, %q)", deref(e.SourceRepo), deref(e.SourceRepoBasis), tt.repo, tt.basis)
			}
		})
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
