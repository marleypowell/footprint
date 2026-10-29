package collect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
