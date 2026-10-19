package main

import (
	"encoding/json"
	"fmt"

	"github.com/marleypowell/footprint/internal/catalog"
	"github.com/marleypowell/footprint/internal/catalog/backstage"
	"github.com/marleypowell/footprint/internal/codehost"
	"github.com/marleypowell/footprint/internal/codehost/github"
	"github.com/marleypowell/footprint/internal/scrape"
	"github.com/marleypowell/footprint/internal/tracker"
	"github.com/marleypowell/footprint/internal/tracker/jira"
	"github.com/marleypowell/footprint/internal/wiki"
	"github.com/marleypowell/footprint/internal/wiki/confluence"
)

// The adapters for each seam, selected by the "kind" of its config section. Supporting another
// tool (gitlab, cortex, linear, notion) means a package beside the existing adapter and a case here.

func newCodeHost(kind, org string, settings json.RawMessage) (codehost.Host, error) {
	switch kind {
	case "github":
		return github.New(org, settings)
	}
	return nil, unsupported("code_host", kind, "github")
}

func newCatalog(kind string, settings json.RawMessage) (catalog.Catalog, error) {
	switch kind {
	case "backstage":
		return backstage.New(settings)
	}
	return nil, unsupported("catalog", kind, "backstage")
}

func newTracker(kind string, settings json.RawMessage, configDir string) (tracker.Tracker, error) {
	switch kind {
	case "jira":
		return jira.New(settings, configDir)
	}
	return nil, unsupported("issue_tracker", kind, "jira")
}

func newWiki(kind string, settings json.RawMessage) (wiki.Wiki, error) {
	switch kind {
	case "confluence":
		return confluence.New(settings)
	}
	return nil, unsupported("wiki", kind, "confluence")
}

func newTools(cfg *scrape.Config) (scrape.Tools, error) {
	host, err := newCodeHost(cfg.CodeHost.Kind, cfg.Org, cfg.CodeHost.Settings)
	if err != nil {
		return scrape.Tools{}, err
	}
	cat, err := newCatalog(cfg.Catalog.Kind, cfg.Catalog.Settings)
	if err != nil {
		return scrape.Tools{}, err
	}
	issues, err := newTracker(cfg.IssueTracker.Kind, cfg.IssueTracker.Settings, cfg.Dir())
	if err != nil {
		return scrape.Tools{}, err
	}
	tools := scrape.Tools{Host: host, Catalog: cat, Tracker: issues}
	if cfg.Wiki != nil { // optional; a nil adapter would be a non-nil interface
		if tools.Wiki, err = newWiki(cfg.Wiki.Kind, cfg.Wiki.Settings); err != nil {
			return scrape.Tools{}, err
		}
	}
	return tools, nil
}

func unsupported(section, kind string, supported ...string) error {
	return fmt.Errorf("%s.kind %q is not supported (supported: %v)", section, kind, supported)
}
