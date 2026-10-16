// Package scrape collects raw data about one person's work across an org: their commits on every
// ref, the pull requests they authored, and what each repo is. It owns the git mirrors and the
// output; the code host, catalog and issue tracker are adapters behind the codehost, catalog and
// tracker interfaces. Every stage is idempotent and overwrites only its own output; the output
// contract is docs/data-schema.md.
package scrape

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/marleypowell/footprint/internal/catalog"
	"github.com/marleypowell/footprint/internal/codehost"
	"github.com/marleypowell/footprint/internal/tracker"
)

// Options are the command-line settings. Relative paths resolve against the working directory.
type Options struct {
	ConfigPath string
	DataDir    string
	CacheDir   string
	Only       string // comma-separated repo names, for the mirror and commits stages
	Org        string // init only
	CodeHost   string // init only: the code host adapter's kind
	Force      bool   // init only
}

// Tools are the adapters for one run.
type Tools struct {
	Host    codehost.Host
	Catalog catalog.Catalog
	Tracker tracker.Tracker
}

// StageOrder is the order `all` runs the stages in. commits reads prs (for the pr-author role).
var StageOrder = []string{"inventory", "mirror", "identities", "prs", "commits", "catalog", "projects", "verify"}

// Stages expands "all" and checks the names, before anything runs.
func Stages(names []string) ([]string, error) {
	if slices.Contains(names, "all") {
		return StageOrder, nil
	}
	for _, name := range names {
		if !slices.Contains(StageOrder, name) {
			return nil, fmt.Errorf("unknown stage %q (want init, all, or one of: %s)", name, strings.Join(StageOrder, ", "))
		}
	}
	return names, nil
}

// Run runs the stages in order.
func Run(ctx context.Context, opts Options, cfg *Config, tools Tools, stages []string) error {
	h, err := newHarness(cfg, opts, tools)
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
	tools   Tools
	raw     string // output directory
	cache   string
	mirrors string
	id      *identity
}

func newHarness(cfg *Config, opts Options, tools Tools) (*harness, error) {
	id, err := newIdentity(cfg)
	if err != nil {
		return nil, err
	}
	return &harness{
		cfg: cfg, opts: opts, tools: tools, raw: opts.DataDir, cache: opts.CacheDir,
		mirrors: filepath.Join(opts.CacheDir, "mirrors"), id: id,
	}, nil
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
