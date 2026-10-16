package scrape

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config is the personal config file (config.json), written by init from config.example.json.
type Config struct {
	Org             string         `json:"org"`
	GitHubLogin     string         `json:"github_login"`
	GitHubUserID    int64          `json:"github_user_id"`
	Identity        IdentityConfig `json:"identity"`
	Mirror          MirrorConfig   `json:"mirror"`
	SearchStartYear int            `json:"search_start_year"`
	// CatalogHubRepos hold Backstage entities for other repos: entities found there are tied to a
	// repo through annotations or links rather than by where the file lives.
	CatalogHubRepos []string   `json:"catalog_hub_repos"`
	Jira            JiraConfig `json:"jira"`

	dir string // relative paths inside the config resolve against its directory
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

// JiraConfig lists real Jira project keys, so that jira_keys skips look-alikes such as UTF-8.
type JiraConfig struct {
	ProjectsFile     *string  `json:"projects_file"` // JSON list of {"key": ...}; null for none
	ExtraProjectKeys []string `json:"extra_project_keys"`
}

// LoadConfig reads a config file, filling in defaults for missing settings.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := Config{Mirror: MirrorConfig{Parallelism: 6, FetchPullRefs: true}, SearchStartYear: 2008}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if cfg.Org == "" || cfg.GitHubLogin == "" {
		return nil, fmt.Errorf("%s: org and github_login are required", path)
	}
	cfg.Mirror.Parallelism = max(cfg.Mirror.Parallelism, 1)
	cfg.dir = filepath.Dir(path)
	return &cfg, nil
}

func (c *Config) resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(c.dir, path)
}
