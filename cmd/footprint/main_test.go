package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/collect"
)

// writeConfig writes a config file and returns its path.
func writeConfig(t *testing.T, text string) string {
	t.Helper()
	return putFile(t, t.TempDir(), "config.json", text)
}

// run checks its arguments, then the config and its tools, before any stage runs: every case here
// fails before reaching the network.
func TestRunRefusesBeforeAnyStage(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "config.json")
	valid := `{"org":"acme","login":"jdoe","code_host":{"kind":"github"},"catalog":{"kind":"backstage"}`
	tests := []struct {
		name   string
		opts   collect.Options
		stages []string
		want   string // the error
	}{
		{"init with other stages", collect.Options{Org: "acme", CodeHost: "github"}, []string{"init", "prs"},
			"run init on its own, review the config it writes, then run the stages"},
		{"init without an org", collect.Options{CodeHost: "github"}, []string{"init"}, "init needs --org <org>"},
		{"init on an unsupported code host", collect.Options{Org: "acme", CodeHost: "gitlab"}, []string{"init"},
			`code_host.kind "gitlab" is not supported (supported: [github])`},
		// Stage names are checked before the config is read.
		{"an unknown stage", collect.Options{ConfigPath: missing}, []string{"prs", "comits"}, `unknown stage "comits"`},
		{"no config", collect.Options{ConfigPath: missing}, []string{"prs"},
			missing + " not found: run `footprint init --org <org>` first"},
		{"a config without its login", collect.Options{ConfigPath: writeConfig(t, `{"org":"acme","code_host":{"kind":"github"},"catalog":{"kind":"backstage"}}`)},
			[]string{"all"}, "login is missing"},
		{"a config that is not JSON", collect.Options{ConfigPath: writeConfig(t, `org: acme`)}, []string{"prs"}, "config.json: invalid character"},
		{"a config naming an unsupported tool", collect.Options{ConfigPath: writeConfig(t, valid+`,"wiki":{"kind":"notion"}}`)},
			[]string{"pages"}, `wiki.kind "notion" is not supported`},
	}
	for _, tt := range tests {
		err := run(context.Background(), tt.opts, tt.stages)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("run(%s: %v) = %v, want an error with %q", tt.name, tt.stages, err, tt.want)
		}
	}
	if _, err := os.Stat(missing); err == nil {
		t.Errorf("run wrote %s", missing)
	}
}
