package scrape

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marleypowell/footprint/internal/codehost"
)

// Repo is a repository as the code host describes it.
type Repo = codehost.Repo

// inventory lists every repo in the org (<data>/org/repos.json).
func (h *harness) inventory(ctx context.Context) error {
	repos, err := h.tools.Host.Repos(ctx)
	if err != nil {
		return err
	}
	sort.SliceStable(repos, func(i, j int) bool { return strings.ToLower(repos[i].Name) < strings.ToLower(repos[j].Name) })
	if err := writeJSON(filepath.Join(h.raw, "org", "repos.json"), orEmpty(repos)); err != nil {
		return err
	}
	logf("inventory: wrote %d repos", len(repos))
	return nil
}
