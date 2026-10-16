// Package jira is the issue tracker adapter for Jira: PROJ-123 keys of known projects, found
// case-insensitively in commit messages, branch names and pull requests.
package jira

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/marleypowell/footprint/internal/tracker"
)

// Tracker finds keys of known Jira projects. With no known projects it finds nothing: a bare
// KEY-123 pattern matches too much (UTF-8, ISO-8601, dotnet-8...).
type Tracker struct {
	projects []tracker.Project
	keys     map[string]bool
}

var _ tracker.Tracker = (*Tracker)(nil)

type settings struct {
	// ProjectsFile lists the projects, as Jira's project search API returns them (relative to the
	// config file). Export it with the Atlassian connector.
	ProjectsFile string `json:"projects_file"`
	// ExtraProjectKeys are projects missing from the file: archived ones still seen in branch names.
	ExtraProjectKeys []string `json:"extra_project_keys"`
}

// New returns the adapter. settings is the issue_tracker config section; relative paths in it
// resolve against baseDir.
func New(raw json.RawMessage, baseDir string) (*Tracker, error) {
	var s settings
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("jira settings: %w", err)
		}
	}
	t := &Tracker{projects: []tracker.Project{}, keys: map[string]bool{}}
	if s.ProjectsFile != "" {
		file := s.ProjectsFile
		if !filepath.IsAbs(file) {
			file = filepath.Join(baseDir, file)
		}
		projects, err := readProjects(file)
		if err != nil {
			return nil, err
		}
		t.projects = projects
	}
	for _, p := range t.projects {
		t.keys[strings.ToUpper(p.Key)] = true
	}
	for _, k := range s.ExtraProjectKeys {
		t.keys[strings.ToUpper(k)] = true
	}
	return t, nil
}

// readProjects reads Jira's project fields (key, name, projectTypeKey, archived). A missing file
// reads as no projects.
func readProjects(file string) ([]tracker.Project, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return []tracker.Project{}, nil
	}
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Key            string `json:"key"`
		Name           string `json:"name"`
		ProjectTypeKey string `json:"projectTypeKey"`
		Archived       bool   `json:"archived"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	projects := make([]tracker.Project, 0, len(raw))
	for _, p := range raw {
		projects = append(projects, tracker.Project{Key: p.Key, Name: p.Name, Type: p.ProjectTypeKey, Archived: p.Archived})
	}
	return projects, nil
}

func (t *Tracker) Projects() []tracker.Project { return t.projects }

var (
	// Jira issue numbers never have leading zeros, which also rules out analyzer IDs like ACME0901.
	keyRE       = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]{1,9}-[1-9][0-9]{0,5}`)
	keyNoDashRE = regexp.MustCompile(`[A-Za-z]{4,10}[1-9][0-9]{2,5}`) // "SHOP3481" typos
	guidRE      = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
)

func (t *Tracker) Keys(texts ...string) []string {
	found := map[string]bool{}
	add := func(project, number string) {
		if project = strings.ToUpper(project); t.keys[project] {
			found[project+"-"+number] = true
		}
	}
	for _, text := range texts {
		text = guidRE.ReplaceAllString(text, " ")
		for _, m := range findBounded(keyRE, text) {
			i := strings.IndexByte(m, '-')
			add(m[:i], m[i+1:])
		}
		for _, m := range findBounded(keyNoDashRE, text) {
			i := strings.IndexAny(m, "123456789")
			add(m[:i], m[i:])
		}
	}
	keys := make([]string, 0, len(found))
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// findBounded returns the matches of re that are not preceded by an ASCII letter or digit and not
// followed by a digit: the lookarounds RE2 lacks. A rejected match resumes the search one byte
// later, as a backtracking engine would.
func findBounded(re *regexp.Regexp, text string) []string {
	var out []string
	for pos := 0; pos < len(text); {
		loc := re.FindStringIndex(text[pos:])
		if loc == nil {
			break
		}
		start, end := pos+loc[0], pos+loc[1]
		if (start > 0 && isASCIIAlnum(text[start-1])) || (end < len(text) && isDigit(text[end])) {
			pos = start + 1
			continue
		}
		out = append(out, text[start:end])
		pos = end
	}
	return out
}

func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isASCIIAlnum(c byte) bool { return isDigit(c) || (c|0x20 >= 'a' && c|0x20 <= 'z') }
