package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

const entityGrep = `^apiVersion:[[:space:]]*['"]?(backstage\.io|scaffolder\.backstage\.io)/`

// Entity is one Backstage entity document found on a default branch (<data>/backstage/entities.jsonl).
type Entity struct {
	Ref                string          `json:"ref"`
	Kind               any             `json:"kind"`
	FoundInRepo        string          `json:"found_in_repo"`
	Path               string          `json:"path"`
	DocIndex           int             `json:"doc_index"`
	IsTemplateSkeleton bool            `json:"is_template_skeleton"`
	SourceRepo         *string         `json:"source_repo"`
	SourceRepoBasis    *string         `json:"source_repo_basis"`
	RepoSignals        RepoSignals     `json:"repo_signals"`
	Entity             json.RawMessage `json:"entity"`
}

// RepoSignals is the evidence for which repo an entity describes.
type RepoSignals struct {
	Slug           *string  `json:"slug"`
	SourceLocation *string  `json:"source_location"`
	Links          []string `json:"links"`
}

type parseError struct {
	Repo  string `json:"repo"`
	Path  string `json:"path"`
	Error string `json:"error"`
}

type catalogDoc struct {
	path  string
	index int
	doc   *orderedMap
}

// catalog collects every Backstage entity on every default branch, keeping the raw YAML.
func (h *harness) catalog(ctx context.Context) error {
	repos, err := h.mirroredRepos()
	if err != nil {
		return err
	}
	namesLower := map[string]string{}
	for _, r := range repos {
		namesLower[strings.ToLower(r.Name)] = r.Name
	}
	links := newRepoLinks(h.cfg.Org, namesLower)
	hubs := map[string]bool{}
	for _, name := range h.cfg.CatalogHubRepos {
		hubs[name] = true
	}
	if err := os.RemoveAll(filepath.Join(h.raw, "backstage", "files")); err != nil {
		return err
	}
	type scanned struct {
		docs []catalogDoc
		errs []parseError
	}
	results := make([]scanned, len(repos))
	err = forEach(ctx, 8, repos, func(ctx context.Context, i int, r Repo) error {
		docs, errs, err := h.scanCatalog(ctx, r)
		results[i] = scanned{docs, errs}
		return err
	})
	if err != nil {
		return err
	}
	entities, parseErrors := []Entity{}, []parseError{}
	for i, r := range repos {
		parseErrors = append(parseErrors, results[i].errs...)
		for _, d := range results[i].docs {
			raw, err := marshal(d.doc)
			if err != nil {
				return fmt.Errorf("%s %s: %w", r.Name, d.path, err)
			}
			signals := links.signals(d.doc)
			source, basis := resolveSourceRepo(signals, r.Name, hubs)
			entities = append(entities, Entity{
				Ref:                entityRef(field(d.doc, "kind"), field(d.doc, "metadata")),
				Kind:               field(d.doc, "kind"),
				FoundInRepo:        r.Name,
				Path:               d.path,
				DocIndex:           d.index,
				IsTemplateSkeleton: strings.Contains("/"+d.path, "/skeleton/"),
				SourceRepo:         source,
				SourceRepoBasis:    basis,
				RepoSignals:        signals,
				Entity:             raw,
			})
		}
	}
	sort.SliceStable(entities, func(i, j int) bool {
		a, b := entities[i], entities[j]
		if a.FoundInRepo != b.FoundInRepo {
			return a.FoundInRepo < b.FoundInRepo
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.DocIndex < b.DocIndex
	})
	if err := writeJSONL(filepath.Join(h.raw, "backstage", "entities.jsonl"), entities); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(h.raw, "backstage", "parse_errors.json"), parseErrors); err != nil {
		return err
	}
	var kinds []string
	counts := map[string]int{}
	for _, e := range entities {
		k := asString(e.Kind)
		if counts[k] == 0 {
			kinds = append(kinds, k)
		}
		counts[k]++
	}
	summary := make([]string, len(kinds))
	for i, k := range kinds {
		summary[i] = fmt.Sprintf("%s=%d", k, counts[k])
	}
	logf("catalog: %d entities (%s), %d unparseable files", len(entities), strings.Join(summary, " "), len(parseErrors))
	return nil
}

// scanCatalog reads catalog-info.y(a)ml files and any YAML declaring a backstage.io apiVersion
// on the default branch, saving each raw file under <data>/backstage/files/<repo>/.
func (h *harness) scanCatalog(ctx context.Context, repo Repo) ([]catalogDoc, []parseError, error) {
	repoPath, ref := h.mirrorPath(repo.Name), "refs/heads/"+repo.defaultBranch()
	if repo.defaultBranch() == "" || !hasRef(ctx, repoPath, ref) {
		return nil, nil, nil
	}
	tree, err := lsTree(ctx, repoPath, ref)
	if err != nil {
		return nil, nil, err
	}
	files := map[string]bool{}
	for _, p := range tree {
		if base := strings.ToLower(path.Base(p)); base == "catalog-info.yaml" || base == "catalog-info.yml" {
			files[p] = true
		}
	}
	out, err := git(ctx, repoPath, nil, "grep", "-l", "-z", "-I", "-E", entityGrep, ref, "--", "*.yaml", "*.yml")
	if err != nil && exitCode(err) != 1 { // 1 = no matches
		return nil, nil, err
	}
	for _, item := range strings.Split(string(out), "\x00") {
		if _, p, ok := strings.Cut(item, ":"); ok { // "<ref>:<path>"
			files[p] = true
		}
	}
	var docs []catalogDoc
	var errs []parseError
	for _, file := range sortedKeys(files) {
		blob, err := git(ctx, repoPath, nil, "cat-file", "blob", ref+":"+file)
		if err != nil {
			return nil, nil, err
		}
		text := strings.ToValidUTF8(string(blob), "�")
		if err := writeUnder(filepath.Join(h.raw, "backstage", "files", repo.Name), file, []byte(text)); err != nil {
			return nil, nil, err
		}
		parsed, err := parseYAMLDocs(text)
		if err != nil {
			errs = append(errs, parseError{Repo: repo.Name, Path: file, Error: truncate(err.Error(), 500)})
			continue
		}
		for i, doc := range parsed {
			if _, hasKind := doc.values["kind"]; hasKind && strings.Contains(asString(field(doc, "apiVersion")), "backstage.io") {
				docs = append(docs, catalogDoc{path: file, index: i, doc: doc})
			}
		}
	}
	return docs, errs, nil
}

// entityRef is Backstage's kind:namespace/name, lower-cased.
func entityRef(kind, metadata any) string {
	namespace := asString(field(metadata, "namespace"))
	if namespace == "" {
		namespace = "default"
	}
	return strings.ToLower(asString(kind)) + ":" + strings.ToLower(namespace) + "/" + strings.ToLower(asString(field(metadata, "name")))
}

// repoLinks finds org repo names in GitHub URLs.
type repoLinks struct {
	org        string
	re         *regexp.Regexp
	namesLower map[string]string
}

func newRepoLinks(org string, namesLower map[string]string) *repoLinks {
	re := regexp.MustCompile(`(?i)github\.com[/:]` + regexp.QuoteMeta(org) + `/([A-Za-z0-9._-]+)`)
	return &repoLinks{org: org, re: re, namesLower: namesLower}
}

func (l *repoLinks) reposIn(text string) []string {
	found := []string{}
	for _, m := range l.re.FindAllStringSubmatch(text, -1) {
		name, ok := l.namesLower[strings.ToLower(strings.TrimSuffix(m[1], ".git"))]
		if ok && !slices.Contains(found, name) {
			found = append(found, name)
		}
	}
	return found
}

func (l *repoLinks) signals(doc any) RepoSignals {
	meta := field(doc, "metadata")
	annotations := field(meta, "annotations")
	s := RepoSignals{Links: []string{}}
	slug := asString(field(annotations, "github.com/project-slug"))
	if prefix := strings.ToLower(l.org) + "/"; strings.HasPrefix(strings.ToLower(slug), prefix) {
		if name, ok := l.namesLower[strings.ToLower(slug[len(prefix):])]; ok {
			s.Slug = &name
		}
	}
	if found := l.reposIn(asString(field(annotations, "backstage.io/source-location"))); len(found) > 0 {
		s.SourceLocation = &found[0]
	}
	if list, ok := field(meta, "links").([]any); ok {
		for _, link := range list {
			if isObject(link) {
				for _, name := range l.reposIn(asString(field(link, "url"))) {
					if !slices.Contains(s.Links, name) {
						s.Links = append(s.Links, name)
					}
				}
			}
		}
	}
	return s
}

// resolveSourceRepo decides which repo an entity describes: explicit annotations first, then the
// repo the file lives in, except in catalog hubs (which hold entities for other repos), where
// the entity's links are used instead.
func resolveSourceRepo(s RepoSignals, foundIn string, hubs map[string]bool) (repo, basis *string) {
	switch {
	case s.Slug != nil:
		return s.Slug, ptr("annotation:github.com/project-slug")
	case s.SourceLocation != nil:
		return s.SourceLocation, ptr("annotation:backstage.io/source-location")
	case !hubs[foundIn]:
		return &foundIn, ptr("file_location")
	case len(s.Links) > 0:
		return &s.Links[0], ptr("metadata.links")
	}
	return nil, nil
}
