// Package scrape collects raw data about one person's work across a GitHub org: their commits on
// every ref, the pull requests they authored, and what each repo is. Every stage is idempotent and
// overwrites only its own output; the output contract is docs/data-schema.md.
package scrape

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Options are the command-line settings. Relative paths resolve against the working directory.
type Options struct {
	ConfigPath string
	DataDir    string
	CacheDir   string
	Only       string // comma-separated repo names, for the mirror and commits stages
	Org        string // init only
	Force      bool   // init only
}

// StageOrder is the order `all` runs the stages in. commits reads prs (for the pr-author role).
var StageOrder = []string{"inventory", "mirror", "identities", "prs", "commits", "catalog", "projects", "verify"}

// Run runs init on its own, or the named stages in order ("all" expands to StageOrder).
func Run(ctx context.Context, opts Options, stages []string) error {
	if slices.Contains(stages, "init") {
		if len(stages) > 1 {
			return errors.New("run init on its own, review the config it writes, then run the stages")
		}
		return runInit(ctx, opts)
	}
	if slices.Contains(stages, "all") {
		stages = StageOrder
	}
	for _, name := range stages {
		if !slices.Contains(StageOrder, name) {
			return fmt.Errorf("unknown stage %q (want init, all, or one of: %s)", name, strings.Join(StageOrder, ", "))
		}
	}
	cfg, err := LoadConfig(opts.ConfigPath)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s not found: run `footprint init --org <github-org>` first", opts.ConfigPath)
	}
	if err != nil {
		return err
	}
	h, err := newHarness(cfg, opts)
	if err != nil {
		return err
	}
	run := map[string]func(context.Context) error{
		"inventory":  h.inventory,
		"mirror":     h.mirror,
		"identities": h.identities,
		"prs":        h.prs,
		"commits":    h.commits,
		"catalog":    h.catalog,
		"projects":   h.projects,
		"verify":     h.verify,
	}
	for _, name := range stages {
		logf("=== stage %s", name)
		if err := run[name](ctx); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// harness is one run's settings and shared state.
type harness struct {
	cfg     *Config
	opts    Options
	raw     string // output directory
	cache   string
	mirrors string
	id      *identity
	jira    *jiraMatcher

	ghOnce sync.Once
	gh     *githubClient
	ghErr  error
}

func newHarness(cfg *Config, opts Options) (*harness, error) {
	id, err := newIdentity(cfg)
	if err != nil {
		return nil, err
	}
	jira, err := loadJira(cfg)
	if err != nil {
		return nil, err
	}
	return &harness{
		cfg: cfg, opts: opts, raw: opts.DataDir, cache: opts.CacheDir,
		mirrors: filepath.Join(opts.CacheDir, "mirrors"), id: id, jira: jira,
	}, nil
}

// github returns the API client, created on first use so local-only stages need no token.
func (h *harness) github(ctx context.Context) (*githubClient, error) {
	h.ghOnce.Do(func() { h.gh, h.ghErr = newGitHubClient(ctx) })
	return h.gh, h.ghErr
}

func (h *harness) mirrorPath(name string) string { return filepath.Join(h.mirrors, name+".git") }

func (h *harness) isMirrored(name string) bool {
	return fileExists(filepath.Join(h.mirrorPath(name), "HEAD"))
}

func (h *harness) repoDir(name string) string { return filepath.Join(h.raw, "repos", name) }

func (h *harness) loadRepos() ([]Repo, error) {
	var repos []Repo
	ok, err := readJSON(filepath.Join(h.raw, "org", "repos.json"), &repos)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("no repo inventory yet: run the inventory stage first")
	}
	return repos, nil
}

func (h *harness) mirroredRepos() ([]Repo, error) {
	repos, err := h.loadRepos()
	if err != nil {
		return nil, err
	}
	var out []Repo
	for _, r := range repos {
		if h.isMirrored(r.Name) {
			out = append(out, r)
		}
	}
	return out, nil
}

// selected narrows repos to --only, when given.
func (h *harness) selected(repos []Repo) []Repo {
	if h.opts.Only == "" {
		return repos
	}
	wanted := map[string]bool{}
	for _, name := range strings.Split(h.opts.Only, ",") {
		wanted[strings.TrimSpace(name)] = true
	}
	var out []Repo
	for _, r := range repos {
		if wanted[r.Name] {
			out = append(out, r)
		}
	}
	return out
}
