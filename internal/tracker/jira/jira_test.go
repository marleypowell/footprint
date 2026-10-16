package jira

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/marleypowell/footprint/internal/tracker"
)

func TestKeys(t *testing.T) {
	j, err := New([]byte(`{"kind": "jira", "extra_project_keys": ["SHOP", "APP", "TK", "UI", "CORE", "ACME", "CAFE", "GRO"]}`), "")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, text string
		want       []string
	}{
		{"conventional commit scope", "feat(SHOP-4435): move the shell", []string{"SHOP-4435"}},
		{"lowercase branch name", "jdoe/tk-473", []string{"TK-473"}},
		{"suffix of a feature-flag name", "flag `storefront-enable-order-search-ui-17`", []string{"UI-17"}},
		{"inside a word", "upgrade aspnetcore-8 packages", []string{}},
		{"unknown project", "UTF-8 and ISO-8601", []string{}},
		{"missing dash typo", "SHOP3481 tidy up", []string{"SHOP-3481"}},
		{"leading zero is an analyzer id", "suppress ACME0901 and ACME-0001", []string{}},
		{"seven digits", "SHOP-1234567", []string{}},
		{"adjacent keys", "SHOP-1,SHOP-2 SHOP-3", []string{"SHOP-1", "SHOP-2", "SHOP-3"}},
		{"guid fragments are not keys", "id deadbeef-cafe-1234-abcd-0123456789ab", []string{}},
		{"duplicates collapse", "GRO-12 then GRO-12 again", []string{"GRO-12"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := j.Keys(tt.text); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Keys(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
	if got := j.Keys("APP-2", "app-1"); !reflect.DeepEqual(got, []string{"APP-1", "APP-2"}) {
		t.Errorf("several texts: got %v", got)
	}
}

func TestProjectsFile(t *testing.T) {
	dir := t.TempDir()
	projects := `[{"key": "PAY", "name": "Payments", "projectTypeKey": "software", "archived": false}]`
	if err := os.WriteFile(filepath.Join(dir, "projects.json"), []byte(projects), 0o644); err != nil {
		t.Fatal(err)
	}
	j, err := New([]byte(`{"kind": "jira", "projects_file": "projects.json"}`), dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []tracker.Project{{Key: "PAY", Name: "Payments", Type: "software"}}
	if got := j.Projects(); !reflect.DeepEqual(got, want) {
		t.Errorf("Projects() = %+v, want %+v", got, want)
	}
	if got := j.Keys("fix(PAY-111): timeouts"); !reflect.DeepEqual(got, []string{"PAY-111"}) {
		t.Errorf("keys from the projects file: %v", got)
	}

	none, err := New([]byte(`{"kind": "jira", "projects_file": "missing.json"}`), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(none.Projects()) != 0 || len(none.Keys("PAY-1 SHOP-2")) != 0 {
		t.Error("with no known projects, nothing is a key")
	}
}
