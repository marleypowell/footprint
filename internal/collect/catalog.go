package collect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marleypowell/footprint/internal/catalog"
	"github.com/marleypowell/footprint/internal/par"
)

type parseError struct {
	Repo  string `json:"repo"`
	Path  string `json:"path"`
	Error string `json:"error"`
}

// catalog asks the catalog adapter what every mirrored repo's default branch declares, and keeps
// the raw descriptor files (<data>/catalog/).
func (h *harness) catalog(ctx context.Context) error {
	repos, err := h.mirroredRepos()
	if err != nil {
		return err
	}
	byPath := map[string]string{} // "owner/name" -> repo name
	for _, r := range repos {
		byPath[strings.ToLower(r.FullName)] = r.Name
	}
	resolve := func(ref string) (string, bool) {
		path, ok := h.tools.Host.RepoPath(ref)
		if !ok {
			return "", false
		}
		name, ok := byPath[strings.ToLower(path)]
		return name, ok
	}
	dir := filepath.Join(h.raw, "catalog")
	if err := os.RemoveAll(filepath.Join(dir, "files")); err != nil {
		return err
	}
	results := make([]catalog.Result, len(repos))
	err = par.ForEach(ctx, 8, repos, func(ctx context.Context, i int, r Repo) error {
		ref := "refs/heads/" + r.DefaultBranchName()
		if r.DefaultBranchName() == "" || !hasRef(ctx, h.mirrorPath(r.Name), ref) {
			return nil
		}
		res, err := h.tools.Catalog.Scan(ctx, &gitSource{repo: r.Name, gitDir: h.mirrorPath(r.Name), ref: ref}, resolve)
		if err != nil {
			return fmt.Errorf("%s: %w", r.Name, err)
		}
		for _, f := range res.Files {
			if _, err := writeUnder(filepath.Join(dir, "files", r.Name), f.Path, f.Content); err != nil {
				return err
			}
		}
		results[i] = res
		return nil
	})
	if err != nil {
		return err
	}
	entities, parseErrors := []catalog.Entity{}, []parseError{}
	for i, r := range repos {
		entities = append(entities, results[i].Entities...)
		for _, e := range results[i].Errors {
			parseErrors = append(parseErrors, parseError{Repo: r.Name, Path: e.Path, Error: e.Error})
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
	if err := writeJSONL(filepath.Join(dir, "entities.jsonl"), entities); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "parse_errors.json"), parseErrors); err != nil {
		return err
	}
	var kinds []string
	counts := map[string]int{}
	for _, e := range entities {
		if counts[e.Kind] == 0 {
			kinds = append(kinds, e.Kind)
		}
		counts[e.Kind]++
	}
	summary := make([]string, len(kinds))
	for i, k := range kinds {
		summary[i] = fmt.Sprintf("%s=%d", k, counts[k])
	}
	logf("catalog: %d entities (%s), %d unparseable files", len(entities), strings.Join(summary, " "), len(parseErrors))
	return nil
}
