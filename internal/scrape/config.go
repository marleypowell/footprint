package scrape

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config is the personal config file (config.json), written by init from config.example.json.
type Config struct {
	Org      string         `json:"org"`   // the org (GitHub) or group (GitLab) to collect from
	Login    string         `json:"login"` // the user's account on the code host
	Identity IdentityConfig `json:"identity"`
	// Each tool section names its adapter ("kind") and holds that adapter's settings.
	CodeHost     Tool         `json:"code_host"`
	Catalog      Tool         `json:"catalog"`
	IssueTracker Tool         `json:"issue_tracker"`
	Mirror       MirrorConfig `json:"mirror"`

	dir string
}

// Tool is a config section selecting an adapter. Settings is the whole section, kind included,
// for the adapter to read its own fields from.
type Tool struct {
	Kind     string
	Settings json.RawMessage
}

func (t *Tool) UnmarshalJSON(b []byte) error {
	var head struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, b); err != nil {
		return err
	}
	t.Kind, t.Settings = head.Kind, compact.Bytes()
	return nil
}

func (t Tool) MarshalJSON() ([]byte, error) {
	if len(t.Settings) > 0 {
		return t.Settings, nil
	}
	return json.Marshal(map[string]string{"kind": t.Kind})
}

// IdentityConfig says who counts as the user.
type IdentityConfig struct {
	Emails           []string `json:"emails"`            // matched case-insensitively
	Names            []string `json:"names"`             // matched exactly (case-insensitively)
	DiscoveryPattern string   `json:"discovery_pattern"` // regex for the identities report and message grep
}

type MirrorConfig struct {
	Parallelism   int  `json:"parallelism"`
	FetchPullRefs bool `json:"fetch_pull_refs"`
}

// LoadConfig reads a config file, filling in defaults for missing settings.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := Config{Mirror: MirrorConfig{Parallelism: 6, FetchPullRefs: true}}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, required := range []struct{ name, value string }{
		{"org", cfg.Org}, {"login", cfg.Login}, {"code_host.kind", cfg.CodeHost.Kind},
		{"catalog.kind", cfg.Catalog.Kind}, {"issue_tracker.kind", cfg.IssueTracker.Kind},
	} {
		if required.value == "" {
			return nil, fmt.Errorf("%s: %s is missing (regenerate with `footprint init --force --org <org>`)", path, required.name)
		}
	}
	cfg.Mirror.Parallelism = max(cfg.Mirror.Parallelism, 1)
	cfg.dir = filepath.Dir(path)
	return &cfg, nil
}

// Dir is the directory relative paths inside the config resolve against.
func (c *Config) Dir() string { return c.dir }
