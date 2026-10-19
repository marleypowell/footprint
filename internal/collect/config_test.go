package collect

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/codehost"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"org": "o", "login": "l", "identity": {"emails": ["a@b"]},
	        "code_host": {"kind": "github"}, "catalog": {"kind": "backstage", "hub_repos": ["hub"]},
	        "issue_tracker": {"kind": "jira"}}`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		HubRepos []string `json:"hub_repos"`
	}
	if err := json.Unmarshal(cfg.Catalog.Settings, &settings); err != nil || !reflect.DeepEqual(settings.HubRepos, []string{"hub"}) {
		t.Errorf("adapter settings should reach the adapter: %s", cfg.Catalog.Settings)
	}
	if cfg.Mirror.Parallelism != 6 || !cfg.Mirror.FetchPullRefs {
		t.Errorf("defaults: %+v", cfg.Mirror)
	}

	if cfg.Wiki != nil {
		t.Errorf("the wiki is optional: %+v", cfg.Wiki)
	}

	write(`{"org": "o", "login": "l", "code_host": {"kind": "github"}, "catalog": {"kind": "backstage"}}`)
	if cfg, err := LoadConfig(path); err != nil || cfg.IssueTracker != nil {
		t.Errorf("the issue tracker is optional: %+v, %v", cfg, err)
	}

	write(`{"org": "o", "login": "l", "code_host": {"kind": "github"}, "issue_tracker": {"kind": "jira"}}`)
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "catalog.kind") {
		t.Errorf("a missing tool kind should be reported: %v", err)
	}
	for _, section := range []string{"issue_tracker", "wiki"} {
		write(`{"org": "o", "login": "l", "code_host": {"kind": "github"}, "catalog": {"kind": "backstage"},
		        "` + section + `": {"site": "x"}}`)
		if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), section+".kind") {
			t.Errorf("a %s section without a kind should be reported: %v", section, err)
		}
	}
}

func TestInit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	host := &fakeHost{attributed: []codehost.AttributedCommit{
		{AuthorName: "Jane Doe", AuthorEmail: "Jane.Doe@Example.com"},
		{AuthorName: "jane", AuthorEmail: "jane@laptop.local"},
	}}
	opts := Options{ConfigPath: path, Org: "org", CodeHost: "github"}
	if err := Init(context.Background(), opts, host); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Login != "jdoe" || cfg.Org != "org" {
		t.Errorf("login/org: %q %q", cfg.Login, cfg.Org)
	}
	if want := []string{"7-jdoe@users.noreply.code.example", "jane.doe@example.com", "jane@laptop.local"}; !reflect.DeepEqual(cfg.Identity.Emails, want) {
		t.Errorf("emails = %v, want %v", cfg.Identity.Emails, want)
	}
	if want := []string{"Jane Doe", "jdoe"}; !reflect.DeepEqual(cfg.Identity.Names, want) {
		t.Errorf("names = %v, want %v (single-word names are too ambiguous)", cfg.Identity.Names, want)
	}
	if cfg.Identity.DiscoveryPattern != "doe|jane|jdoe" {
		t.Errorf("pattern = %q", cfg.Identity.DiscoveryPattern)
	}
	// another org than the example's: keep the tool kinds, drop acme's settings
	if string(cfg.Catalog.Settings) != `{"kind":"backstage"}` || cfg.IssueTracker == nil || string(cfg.IssueTracker.Settings) != `{"kind":"jira"}` ||
		cfg.Wiki == nil || string(cfg.Wiki.Settings) != `{"kind":"confluence"}` {
		t.Errorf("example settings leaked into another org: %s %s %+v", cfg.Catalog.Settings, cfg.IssueTracker.Settings, cfg.Wiki)
	}
	if err := Init(context.Background(), opts, host); err == nil {
		t.Error("init should refuse to overwrite without --force")
	}
}

func TestWriteUnderRefusesEscapes(t *testing.T) {
	base := filepath.Join(t.TempDir(), "base")
	escapes := []string{"../escape.txt", "/tmp/absolute.txt", "a/../../b.txt"}
	if runtime.GOOS == "windows" { // ordinary file names elsewhere
		escapes = append(escapes, `..\escape.txt`, `a/..\..\b.txt`, "C:/absolute.txt", "a:stream.txt", "aux/x.txt", "nul.yaml")
	}
	for _, p := range escapes {
		if written, err := writeUnder(base, p, []byte("x")); written || err != nil {
			t.Errorf("%q: written %v, %v", p, written, err)
		}
	}
	if entries, _ := os.ReadDir(filepath.Dir(base)); len(entries) != 0 {
		t.Fatalf("refused paths still wrote %v", entries)
	}
	if written, err := writeUnder(base, "docs/index.md", []byte("x")); !written || err != nil || !fileExists(filepath.Join(base, "docs", "index.md")) {
		t.Errorf("normal path not written: %v", err)
	}
}
