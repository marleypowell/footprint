package facts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/tracker"
)

// Raw is the collected dataset (data/raw), as the collection stages wrote it.
type Raw struct {
	Index         collect.Index
	Projects      []collect.Project // one per repo in the index, in its order
	Commits       []collect.Commit  // every repo's, in index order then file order
	PRs           []collect.PullRequestRecord
	Decisions     []collect.Decision    // decision records in the repos, by repo in index order
	Issues        []collect.IssueRecord // ancestors included
	Unresolved    []collect.UnresolvedKey
	Pages         []collect.PageRecord
	IssueProjects []tracker.Project // the tracker's projects; empty without an issue tracker
}

// Load reads the dataset in dir. It follows index.json rather than globbing, so repos whose
// directory starts with a dot are read too.
func Load(dir string) (*Raw, error) {
	raw := &Raw{}
	if ok, err := readJSON(filepath.Join(dir, "index.json"), &raw.Index); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("%s has no index.json: run the collection stages first", dir)
	}
	for _, repo := range raw.Index.Repos {
		var project collect.Project
		if _, err := readJSON(filepath.Join(dir, repo.Dir, "project.json"), &project); err != nil {
			return nil, err
		}
		if project.Name == "" {
			project.Name, project.URL = repo.Name, repo.URL
		}
		raw.Projects = append(raw.Projects, project)
		commits, err := readJSONL[collect.Commit](dir, repo.Files["commits"])
		if err != nil {
			return nil, err
		}
		prs, err := readJSONL[collect.PullRequestRecord](dir, repo.Files["prs"])
		if err != nil {
			return nil, err
		}
		decisions, err := readJSONL[collect.Decision](dir, repo.Files["decisions"])
		if err != nil {
			return nil, err
		}
		raw.Commits, raw.PRs, raw.Decisions = append(raw.Commits, commits...), append(raw.PRs, prs...), append(raw.Decisions, decisions...)
	}
	for _, g := range raw.Index.IssuesByProject {
		issues, err := readJSONL[collect.IssueRecord](dir, g.File)
		if err != nil {
			return nil, err
		}
		raw.Issues = append(raw.Issues, issues...)
	}
	for _, g := range raw.Index.WikiBySpace {
		pages, err := readJSONL[collect.PageRecord](dir, g.File)
		if err != nil {
			return nil, err
		}
		raw.Pages = append(raw.Pages, pages...)
	}
	if _, err := readJSON(filepath.Join(dir, "issues", "unresolved.json"), &raw.Unresolved); err != nil {
		return nil, err
	}
	if _, err := readJSON(filepath.Join(dir, "org", "issue_projects.json"), &raw.IssueProjects); err != nil {
		return nil, err
	}
	return raw, nil
}

// readJSON decodes path into v. ok is false, with no error, when the file does not exist.
func readJSON(path string, v any) (ok bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("decoding %s: %w", path, err)
	}
	return true, nil
}

// readJSONL decodes one value per line of the file name in dir. A missing file, or no name (the
// index leaves out the files a repo does not have), reads as no rows.
func readJSONL[T any](dir, name string) ([]T, error) {
	if name == "" {
		return nil, nil
	}
	path := filepath.Join(dir, name)
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rows []T
	dec := json.NewDecoder(f)
	for dec.More() {
		var row T
		if err := dec.Decode(&row); err != nil {
			return nil, fmt.Errorf("decoding %s: %w", path, err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}
