// Package catalog is the seam to the service catalog that says what each repository is. Backstage
// is the adapter today (catalog/backstage); Cortex is the expected second. Entity is also the
// output record (catalog/entities.jsonl), so its JSON names are part of docs/data-schema.md.
package catalog

import (
	"context"
	"encoding/json"
)

// Catalog reads one kind of catalog descriptor from repositories.
type Catalog interface {
	// Scan reads the catalog descriptors on one repository's default branch and works out which
	// repository each entity describes, using resolve to recognise repository references.
	Scan(ctx context.Context, src Source, resolve RepoResolver) (Result, error)
}

// Source is read access to one repository's default branch.
type Source interface {
	// Repo is the repository's name.
	Repo() string
	// List returns every file path.
	List(ctx context.Context) ([]string, error)
	// Grep returns the paths, among those matching the git pathspec globs, with a line matching
	// the POSIX extended regex pattern.
	Grep(ctx context.Context, pattern string, globs ...string) ([]string, error)
	// Read returns a file's content.
	Read(ctx context.Context, path string) ([]byte, error)
}

// RepoResolver maps a repository reference found in catalog data (an owner/name slug, or a web or
// clone URL) to the name of an org repository.
type RepoResolver func(ref string) (repo string, ok bool)

// Result is what one repository declares.
type Result struct {
	Files    []File       // descriptor files as read, kept as raw data
	Entities []Entity     // in file order
	Errors   []ParseError // descriptor files that could not be parsed
}

type File struct {
	Path    string
	Content []byte
}

type ParseError struct {
	Path  string
	Error string
}

// Categories are the catalog-neutral classes of entity. Adapters map their own kinds onto them.
const (
	System    = "system"
	Component = "component"
	API       = "api"
	Resource  = "resource"
	Domain    = "domain"
	Group     = "group"
	Other     = "other" // the catalog's bookkeeping: locations, templates, users...
)

// DescribesSoftware reports whether a category is a piece of software (rather than an
// organisational or bookkeeping entity).
func DescribesSoftware(category string) bool {
	switch category {
	case System, Component, API, Resource:
		return true
	}
	return false
}

// Entity is one catalog entity, with the catalog-neutral fields the scraper and the aggregation
// phase use, and the descriptor as written.
type Entity struct {
	Ref         string  `json:"ref"`      // unique in the catalog
	Kind        string  `json:"kind"`     // the catalog's own kind
	Category    string  `json:"category"` // one of the categories above
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Type        *string `json:"type"` // e.g. service, website, library
	FoundInRepo string  `json:"found_in_repo"`
	Path        string  `json:"path"`      // descriptor file
	DocIndex    int     `json:"doc_index"` // position among the file's documents
	// IsTemplateSkeleton marks placeholder entities inside scaffolding templates.
	IsTemplateSkeleton bool    `json:"is_template_skeleton"`
	SourceRepo         *string `json:"source_repo"` // the repository the entity describes
	SourceRepoBasis    *string `json:"source_repo_basis"`
	RepoSignals        Signals `json:"repo_signals"`
	// PartOf are the refs of the entities this one belongs to: system, domain, owner, parent.
	PartOf []string        `json:"part_of"`
	Raw    json.RawMessage `json:"entity"`
}

// Signals is the evidence for which repository an entity describes, as repository names.
type Signals struct {
	Slug           *string  `json:"slug"`            // named by an explicit owner/name reference
	SourceLocation *string  `json:"source_location"` // named by a source-location URL
	Links          []string `json:"links"`           // found among the entity's links
}
