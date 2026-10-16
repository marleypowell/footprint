// Package backstage is the catalog adapter for Backstage: catalog-info.yaml files, and any YAML
// declaring a backstage.io apiVersion, on each repository's default branch.
package backstage

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/marleypowell/footprint/internal/catalog"
	"github.com/marleypowell/footprint/internal/doc"
)

const entityGrep = `^apiVersion:[[:space:]]*['"]?(backstage\.io|scaffolder\.backstage\.io)/`

// slugAnnotations name a repository explicitly, one per code host Backstage integrates with.
var slugAnnotations = []string{"github.com/project-slug", "gitlab.com/project-slug"}

const sourceLocation = "backstage.io/source-location"

// Catalog reads Backstage descriptors.
type Catalog struct {
	hubs map[string]bool
}

var _ catalog.Catalog = (*Catalog)(nil)

type settings struct {
	// HubRepos hold entities for other repositories (a central catalog repo, say): entities found
	// there are tied to a repo by their annotations or links, not by where the file lives.
	HubRepos []string `json:"hub_repos"`
}

// New returns the adapter. settings is the catalog config section.
func New(raw json.RawMessage) (*Catalog, error) {
	var s settings
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("backstage settings: %w", err)
		}
	}
	c := &Catalog{hubs: map[string]bool{}}
	for _, repo := range s.HubRepos {
		c.hubs[repo] = true
	}
	return c, nil
}

func (c *Catalog) Scan(ctx context.Context, src catalog.Source, resolve catalog.RepoResolver) (catalog.Result, error) {
	var result catalog.Result
	paths, err := src.List(ctx)
	if err != nil {
		return result, err
	}
	files := map[string]bool{}
	for _, p := range paths {
		if base := strings.ToLower(path.Base(p)); base == "catalog-info.yaml" || base == "catalog-info.yml" {
			files[p] = true
		}
	}
	declared, err := src.Grep(ctx, entityGrep, "*.yaml", "*.yml")
	if err != nil {
		return result, err
	}
	for _, p := range declared {
		files[p] = true
	}
	ordered := make([]string, 0, len(files))
	for p := range files {
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)
	for _, file := range ordered {
		content, err := src.Read(ctx, file)
		if err != nil {
			return result, err
		}
		text := strings.ToValidUTF8(string(content), "�")
		result.Files = append(result.Files, catalog.File{Path: file, Content: []byte(text)})
		docs, err := doc.ParseYAML(text)
		if err != nil {
			result.Errors = append(result.Errors, catalog.ParseError{Path: file, Error: truncate(err.Error(), 500)})
			continue
		}
		for i, d := range docs {
			if _, hasKind := d.Get("kind"); hasKind && strings.Contains(doc.String(doc.Field(d, "apiVersion")), "backstage.io") {
				e, err := c.entity(src.Repo(), file, i, d, resolve)
				if err != nil {
					return result, err
				}
				result.Entities = append(result.Entities, e)
			}
		}
	}
	return result, nil
}

func (c *Catalog) entity(repo, file string, index int, d *doc.Object, resolve catalog.RepoResolver) (catalog.Entity, error) {
	raw, err := doc.Marshal(d)
	if err != nil {
		return catalog.Entity{}, fmt.Errorf("%s: %w", file, err)
	}
	kind := doc.String(doc.Field(d, "kind"))
	meta := doc.Field(d, "metadata")
	signals, slugKey := repoSignals(meta, resolve)
	source, basis := c.attribute(signals, slugKey, repo)
	return catalog.Entity{
		Ref:                Ref(kind, meta),
		Kind:               kind,
		Category:           category(kind),
		Title:              optional(doc.Field(meta, "title")),
		Description:        optional(doc.Field(meta, "description")),
		Type:               optional(doc.Field(doc.Field(d, "spec"), "type")),
		FoundInRepo:        repo,
		Path:               file,
		DocIndex:           index,
		IsTemplateSkeleton: strings.Contains("/"+file, "/skeleton/"), // scaffolder template contents
		SourceRepo:         source,
		SourceRepoBasis:    basis,
		RepoSignals:        signals,
		PartOf:             partOf(d),
		Raw:                raw,
	}, nil
}

// Ref is Backstage's entity reference, kind:namespace/name, lower-cased.
func Ref(kind string, metadata any) string {
	namespace := doc.String(doc.Field(metadata, "namespace"))
	if namespace == "" {
		namespace = "default"
	}
	return strings.ToLower(kind) + ":" + strings.ToLower(namespace) + "/" + strings.ToLower(doc.String(doc.Field(metadata, "name")))
}

func category(kind string) string {
	switch strings.ToLower(kind) {
	case "system":
		return catalog.System
	case "component":
		return catalog.Component
	case "api":
		return catalog.API
	case "resource":
		return catalog.Resource
	case "domain":
		return catalog.Domain
	case "group":
		return catalog.Group
	}
	return catalog.Other
}

// partOf follows the relations that say what an entity belongs to. Backstage references are
// [kind:][namespace/]name, with the kind defaulting by field.
func partOf(d *doc.Object) []string {
	spec := doc.Field(d, "spec")
	refs := []string{}
	for _, f := range []struct{ field, kind string }{
		{"system", "system"}, {"domain", "domain"}, {"owner", "group"}, {"subcomponentOf", "component"}, {"parent", "group"},
	} {
		value, ok := doc.Field(spec, f.field).(string)
		if !ok || value == "" {
			continue
		}
		kind, rest := f.kind, value
		if i := strings.LastIndex(value, ":"); i >= 0 {
			kind, rest = value[:i], value[i+1:]
		}
		namespace, name := "default", rest
		if i := strings.LastIndex(rest, "/"); i >= 0 {
			namespace, name = rest[:i], rest[i+1:]
		}
		refs = append(refs, strings.ToLower(kind)+":"+strings.ToLower(namespace)+"/"+strings.ToLower(name))
	}
	return refs
}

// repoSignals resolves the entity's repository references, returning which slug annotation
// supplied the slug.
func repoSignals(meta any, resolve catalog.RepoResolver) (s catalog.Signals, slugKey string) {
	annotations := doc.Field(meta, "annotations")
	s.Links = []string{}
	for _, key := range slugAnnotations {
		if repo, ok := resolve(doc.String(doc.Field(annotations, key))); ok {
			s.Slug, slugKey = &repo, key
			break
		}
	}
	if repo, ok := resolve(doc.String(doc.Field(annotations, sourceLocation))); ok {
		s.SourceLocation = &repo
	}
	if links, ok := doc.Field(meta, "links").([]any); ok {
		for _, link := range links {
			if !doc.IsObject(link) {
				continue
			}
			if repo, ok := resolve(doc.String(doc.Field(link, "url"))); ok && !slices.Contains(s.Links, repo) {
				s.Links = append(s.Links, repo)
			}
		}
	}
	return s, slugKey
}

// attribute decides which repository an entity describes: explicit annotations first, then the
// repository the file lives in, except in hub repos, where the entity's links decide.
func (c *Catalog) attribute(s catalog.Signals, slugKey, foundIn string) (repo, basis *string) {
	switch {
	case s.Slug != nil:
		return s.Slug, ptr("annotation:" + slugKey)
	case s.SourceLocation != nil:
		return s.SourceLocation, ptr("annotation:" + sourceLocation)
	case !c.hubs[foundIn]:
		return &foundIn, ptr("file_location")
	case len(s.Links) > 0:
		return &s.Links[0], ptr("metadata.links")
	}
	return nil, nil
}

func optional(v any) *string {
	if v == nil {
		return nil
	}
	s := doc.String(v)
	return &s
}

func ptr[T any](v T) *T { return &v }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
