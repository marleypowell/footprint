package collect

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// newTestHarness is a harness for jdoe (Jane Doe, jane.doe@example.com), with the data and cache
// directories under a temp dir.
func newTestHarness(t *testing.T, tools Tools) *harness {
	t.Helper()
	cfg := &Config{
		Org: "acme", Login: "jdoe",
		Identity: IdentityConfig{Emails: []string{"jane.doe@example.com"}, Names: []string{"Jane Doe"}, DiscoveryPattern: "jane|doe"},
		CodeHost: Tool{Kind: "fakehost"}, Catalog: Tool{Kind: "fakecatalog"},
	}
	root := t.TempDir()
	h, err := newHarness(cfg, Options{DataDir: filepath.Join(root, "raw"), CacheDir: filepath.Join(root, "cache")}, tools)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// testRepo is an org repo on the fake code host, with main as its default branch.
func testRepo(name string) Repo {
	main := "main"
	return Repo{Name: name, FullName: "acme/" + name, URL: "https://code.example/acme/" + name, DefaultBranch: &main}
}

// writeInventory writes org/repos.json as the inventory stage would.
func writeInventory(t *testing.T, h *harness, repos ...Repo) {
	t.Helper()
	if err := writeJSON(filepath.Join(h.raw, "org", "repos.json"), orEmpty(repos)); err != nil {
		t.Fatal(err)
	}
}

// writeRows writes a data file below the data directory.
func writeRows[T any](t *testing.T, h *harness, rel string, rows ...T) {
	t.Helper()
	if err := writeJSONL(filepath.Join(h.raw, rel), rows); err != nil {
		t.Fatal(err)
	}
}

func TestStages(t *testing.T) {
	tests := []struct {
		names   []string
		want    []string
		wantErr string
	}{
		{[]string{"all"}, StageOrder, ""},
		{[]string{"commits", "all"}, StageOrder, ""},                           // all wins
		{[]string{"projects", "commits"}, []string{"projects", "commits"}, ""}, // run as given, not reordered
		{[]string{"commits", "comits"}, nil, `unknown stage "comits"`},
		{[]string{"init"}, nil, `unknown stage "init"`}, // init is a command of its own
	}
	for _, tt := range tests {
		got, err := Stages(tt.names)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Stages(%q) error = %v, want %q", tt.names, err, tt.wantErr)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Stages(%q) = %q, %v, want %q", tt.names, got, err, tt.want)
		}
	}
}

func TestRunNamesTheFailingStage(t *testing.T) {
	cfg := &Config{Login: "jdoe", Identity: IdentityConfig{Emails: []string{"jane.doe@example.com"}}}
	opts := Options{DataDir: t.TempDir(), CacheDir: t.TempDir()}
	err := Run(context.Background(), opts, cfg, Tools{Host: &fakeHost{}}, []string{"commits"})
	if want := "commits: no repo inventory yet: run the inventory stage first"; err == nil || err.Error() != want {
		t.Errorf("Run(commits) before inventory = %v, want %q", err, want)
	}
}

func TestRunRejectsABadDiscoveryPattern(t *testing.T) {
	cfg := &Config{Login: "jdoe", Identity: IdentityConfig{DiscoveryPattern: "jane|(doe"}}
	err := Run(context.Background(), Options{DataDir: t.TempDir()}, cfg, Tools{}, []string{"inventory"})
	if err == nil || !strings.Contains(err.Error(), "identity.discovery_pattern") {
		t.Errorf("Run with an invalid pattern = %v, want an identity.discovery_pattern error", err)
	}
}

func TestHarnessPaths(t *testing.T) {
	h := newTestHarness(t, Tools{})
	if got, want := h.mirrorPath("api"), filepath.Join(h.cache, "mirrors", "api.git"); got != want {
		t.Errorf("mirrorPath(api) = %q, want %q", got, want)
	}
	if got, want := h.repoDir("api"), filepath.Join(h.raw, "repos", "api"); got != want {
		t.Errorf("repoDir(api) = %q, want %q", got, want)
	}
}

// A repo counts as mirrored once its mirror has a HEAD: a mirror whose first clone failed does not.
func TestMirroredRepos(t *testing.T) {
	h := newTestHarness(t, Tools{})
	writeInventory(t, h, testRepo("api"), testRepo("web"), testRepo("half-cloned"))
	if err := writeFileAtomic(filepath.Join(h.mirrorPath("api"), "HEAD"), []byte("ref: refs/heads/main\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(h.mirrorPath("half-cloned"), 0o755); err != nil {
		t.Fatal(err)
	}
	repos, err := h.mirroredRepos()
	if err != nil || len(repos) != 1 || repos[0].Name != "api" {
		t.Errorf("mirroredRepos() = %+v, %v, want only api", repos, err)
	}
}

func TestLoadReposReportsACorruptInventory(t *testing.T) {
	h := newTestHarness(t, Tools{})
	if err := writeFileAtomic(filepath.Join(h.raw, "org", "repos.json"), []byte("[{")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.loadRepos(); err == nil || !strings.Contains(err.Error(), "repos.json") {
		t.Errorf("loadRepos() of a corrupt inventory = %v, want a decoding error naming the file", err)
	}
}

func TestSelected(t *testing.T) {
	repos := []Repo{testRepo("api"), testRepo("web"), testRepo("docs")}
	tests := []struct {
		only string
		want []string
	}{
		{"", []string{"api", "web", "docs"}},
		{"web", []string{"web"}},
		{"docs, api", []string{"api", "docs"}}, // inventory order, spaces trimmed
		{"API", nil},                           // names match exactly
		{"gone", nil},
	}
	for _, tt := range tests {
		h := &harness{opts: Options{Only: tt.only}}
		var got []string
		for _, r := range h.selected(repos) {
			got = append(got, r.Name)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("selected(--only %q) = %q, want %q", tt.only, got, tt.want)
		}
	}
}
