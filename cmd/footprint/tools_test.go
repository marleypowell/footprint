package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/marleypowell/footprint"
	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/doctor"
)

// tool is a config section of the given kind, settings and all, as LoadConfig makes it.
func tool(kind string) collect.Tool {
	return collect.Tool{Kind: kind, Settings: json.RawMessage(`{"kind":"` + kind + `"}`)}
}

// Each seam builds its one adapter, and names the kinds it supports for any other.
func TestAdapterConstructors(t *testing.T) {
	tests := []struct {
		name    string
		build   func(kind string) (any, error)
		kind    string
		wantErr string // "" for an adapter
	}{
		{"code host", func(k string) (any, error) { return newCodeHost(k, "acme", nil) }, "github", ""},
		{"code host", func(k string) (any, error) { return newCodeHost(k, "acme", nil) }, "gitlab",
			`code_host.kind "gitlab" is not supported (supported: [github])`},
		{"catalog", func(k string) (any, error) { return newCatalog(k, nil) }, "backstage", ""},
		{"catalog", func(k string) (any, error) { return newCatalog(k, nil) }, "cortex",
			`catalog.kind "cortex" is not supported (supported: [backstage])`},
		{"issue tracker", func(k string) (any, error) { return newTracker(k, nil) }, "jira", ""},
		{"issue tracker", func(k string) (any, error) { return newTracker(k, nil) }, "linear",
			`issue_tracker.kind "linear" is not supported (supported: [jira])`},
		{"wiki", func(k string) (any, error) { return newWiki(k, nil) }, "confluence", ""},
		{"wiki", func(k string) (any, error) { return newWiki(k, nil) }, "notion",
			`wiki.kind "notion" is not supported (supported: [confluence])`},
		// Kinds are matched exactly.
		{"code host", func(k string) (any, error) { return newCodeHost(k, "acme", nil) }, "GitHub",
			`code_host.kind "GitHub" is not supported (supported: [github])`},
		{"wiki", func(k string) (any, error) { return newWiki(k, nil) }, "",
			`wiki.kind "" is not supported (supported: [confluence])`},
	}
	for _, tt := range tests {
		got, err := tt.build(tt.kind)
		switch {
		case tt.wantErr == "" && err != nil:
			t.Errorf("%s %q: error %v, want an adapter", tt.name, tt.kind, err)
		case tt.wantErr != "" && (err == nil || err.Error() != tt.wantErr):
			t.Errorf("%s %q: error %v, want %s", tt.name, tt.kind, err, tt.wantErr)
		case tt.wantErr != "" && got != nil:
			t.Errorf("%s %q: an adapter %v beside the error", tt.name, tt.kind, got)
		}
	}
}

// The adapters' own errors come through: settings they cannot read, and a code host without an org.
func TestAdapterConstructorsPassOnAdapterErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"github without an org", second(newCodeHost("github", "", nil)), "github: org is required"},
		{"backstage hub_repos not a list", second(newCatalog("backstage", json.RawMessage(`{"kind":"backstage","hub_repos":"x"}`))), "backstage settings"},
		{"jira site not a string", second(newTracker("jira", json.RawMessage(`{"kind":"jira","site":1}`))), "jira settings"},
		{"confluence site not a string", second(newWiki("confluence", json.RawMessage(`{"kind":"confluence","site":1}`))), "confluence settings"},
	}
	for _, tt := range tests {
		if tt.err == nil || !strings.Contains(tt.err.Error(), tt.want) {
			t.Errorf("%s: error %v, want one with %q", tt.name, tt.err, tt.want)
		}
	}
}

func second[T any](_ T, err error) error { return err }

func TestNewToolsLeavesOptionalToolsNil(t *testing.T) {
	jira, confluence := tool("jira"), tool("confluence")
	tests := []struct {
		name                string
		tracker, wiki       *collect.Tool
		wantTracker, wantWk bool
	}{
		{"no issue tracker or wiki", nil, nil, false, false},
		{"issue tracker only", &jira, nil, true, false},
		{"wiki only", nil, &confluence, false, true},
		{"both", &jira, &confluence, true, true},
	}
	for _, tt := range tests {
		cfg := &collect.Config{Org: "acme", CodeHost: tool("github"), Catalog: tool("backstage"), IssueTracker: tt.tracker, Wiki: tt.wiki}
		tools, err := newTools(cfg)
		if err != nil {
			t.Errorf("newTools(%s) = %v", tt.name, err)
			continue
		}
		if tools.Host == nil || tools.Catalog == nil {
			t.Errorf("newTools(%s): host %v, catalog %v; want both", tt.name, tools.Host, tools.Catalog)
		}
		// A missing section is a nil interface, not an interface holding a nil adapter, so the
		// stages' `Tracker == nil` checks skip it.
		if got := tools.Tracker != nil; got != tt.wantTracker {
			t.Errorf("newTools(%s): Tracker set %v, want %v", tt.name, got, tt.wantTracker)
		}
		if got := tools.Wiki != nil; got != tt.wantWk {
			t.Errorf("newTools(%s): Wiki set %v, want %v", tt.name, got, tt.wantWk)
		}
	}
}

func TestNewToolsReportsTheFirstUnsupportedSection(t *testing.T) {
	linear, notion := tool("linear"), tool("notion")
	tests := []struct {
		name string
		cfg  collect.Config
		want string
	}{
		{"code host", collect.Config{Org: "acme", CodeHost: tool("gitlab"), Catalog: tool("cortex")}, `code_host.kind "gitlab"`},
		{"catalog", collect.Config{Org: "acme", CodeHost: tool("github"), Catalog: tool("cortex")}, `catalog.kind "cortex"`},
		{"issue tracker", collect.Config{Org: "acme", CodeHost: tool("github"), Catalog: tool("backstage"), IssueTracker: &linear, Wiki: &notion},
			`issue_tracker.kind "linear"`},
		{"wiki", collect.Config{Org: "acme", CodeHost: tool("github"), Catalog: tool("backstage"), Wiki: &notion}, `wiki.kind "notion"`},
	}
	for _, tt := range tests {
		tools, err := newTools(&tt.cfg)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("newTools(unsupported %s) = %v, want an error with %s", tt.name, err, tt.want)
		}
		if !reflect.DeepEqual(tools, collect.Tools{}) {
			t.Errorf("newTools(unsupported %s) = %+v beside the error, want no tools", tt.name, tools)
		}
	}
}

// The config init writes from builds every adapter it names.
func TestNewToolsFromTheExampleConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, footprint.ExampleConfig, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := collect.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := newTools(cfg)
	if err != nil {
		t.Fatalf("newTools(example config) = %v", err)
	}
	if tools.Host == nil || tools.Catalog == nil || tools.Tracker == nil || tools.Wiki == nil {
		t.Errorf("newTools(example config) = %+v, want every adapter", tools)
	}
}

func TestEnvNeeds(t *testing.T) {
	jira, confluence, linear, notion := tool("jira"), tool("confluence"), tool("linear"), tool("notion")
	jiraNeeds := []doctor.EnvNeed{
		{Tool: "jira", AnyOf: []string{"ATLASSIAN_EMAIL"}},
		{Tool: "jira", AnyOf: []string{"JIRA_API_TOKEN", "ATLASSIAN_API_TOKEN"}},
	}
	confluenceNeeds := []doctor.EnvNeed{
		{Tool: "confluence", AnyOf: []string{"ATLASSIAN_EMAIL"}},
		{Tool: "confluence", AnyOf: []string{"CONFLUENCE_API_TOKEN", "ATLASSIAN_API_TOKEN"}},
	}
	tests := []struct {
		name          string
		tracker, wiki *collect.Tool
		want          []doctor.EnvNeed
	}{
		// The code host needs nothing: GitHub falls back to the gh CLI's token.
		{"code host and catalog only", nil, nil, nil},
		{"jira", &jira, nil, jiraNeeds},
		{"confluence", nil, &confluence, confluenceNeeds},
		{"jira and confluence", &jira, &confluence, append(append([]doctor.EnvNeed{}, jiraNeeds...), confluenceNeeds...)},
		{"other kinds need nothing known", &linear, &notion, nil},
	}
	for _, tt := range tests {
		cfg := &collect.Config{CodeHost: tool("github"), Catalog: tool("backstage"), IssueTracker: tt.tracker, Wiki: tt.wiki}
		if got := envNeeds(cfg); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("envNeeds(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
