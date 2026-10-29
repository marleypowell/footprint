package facts

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/collect"
)

// Load reads what the collection stages write, through index.json, dot-directories included.
func TestLoad(t *testing.T) {
	dir := t.TempDir()
	raw := fixture()
	write := func(rel string, v any, lines bool) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		if lines {
			for _, row := range reflectRows(v) {
				_ = enc.Encode(row)
			}
		} else {
			_ = enc.Encode(v)
		}
		if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw.Index.Repos[0].Dir = "repos/.app"
	raw.Index.Repos[0].Files = map[string]string{"commits": "repos/.app/commits.jsonl", "prs": "repos/.app/prs.jsonl",
		"decisions": "repos/.app/decisions.jsonl", "reviews": "repos/.app/reviews.jsonl", "ownership": "repos/.app/ownership.json"}
	// a repo without PRs: the index lists no prs file for it
	lib := commit("l1", "2024-01-02T10:00:00Z", jane, []string{"author", "committer"}, "lib work", func(c *collect.Commit) { c.Repo = "lib" })
	raw.Index.Repos = append(raw.Index.Repos, collect.IndexRepo{Name: "lib", Dir: "repos/lib", Files: map[string]string{"commits": "repos/lib/commits.jsonl"}})
	write("repos/lib/project.json", collect.Project{Name: "lib"}, false)
	write("repos/lib/commits.jsonl", []collect.Commit{lib}, true)
	raw.Index.IssuesByProject = []collect.IndexGroup{{Key: "ENG", File: "issues/ENG.jsonl"}}
	raw.Index.WikiBySpace = []collect.IndexGroup{{Key: "ENG", File: "wiki/ENG.jsonl"}}
	write("index.json", raw.Index, false)
	write("repos/.app/project.json", raw.Projects[0], false)
	write("repos/.app/commits.jsonl", raw.Commits, true)
	write("repos/.app/prs.jsonl", raw.PRs, true)
	write("repos/.app/decisions.jsonl", raw.Decisions, true)
	write("repos/.app/reviews.jsonl", raw.Reviews, true)
	write("org/reviews.json", collect.ReviewsSummary{PRs: len(raw.Reviews)}, false)
	write("repos/.app/ownership.json", blamed(fixture()).Ownership[0], false)
	write("issues/ENG.jsonl", raw.Issues, true)
	write("wiki/ENG.jsonl", raw.Pages, true)
	write("org/issue_projects.json", raw.IssueProjects, false)
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "loaded", []int{len(got.Projects), len(got.Commits), len(got.PRs), len(got.Decisions), len(got.Issues), len(got.Pages), len(got.IssueProjects), len(got.Reviews)},
		[]int{2, len(raw.Commits) + 1, len(raw.PRs), len(raw.Decisions), len(raw.Issues), len(raw.Pages), 1, len(raw.Reviews)})
	check(t, "the reviews stage ran", got.ReviewsRan, true)
	check(t, "a repo's blame is loaded, a repo without one has none", []any{len(got.Ownership), got.Ownership[0].Repo, len(got.Ownership[0].Files)}, []any{1, "app", 3})
	if _, err := Load(t.TempDir()); err == nil || !strings.Contains(err.Error(), "no index.json") {
		t.Errorf("want a clear error without a dataset, got %v", err)
	}
}

func reflectRows(v any) []any {
	rv := reflect.ValueOf(v)
	rows := make([]any, rv.Len())
	for i := range rows {
		rows[i] = rv.Index(i).Interface()
	}
	return rows
}

// Load fills in a repo's name and URL from the index when the repo has no project.json, and says
// the reviews stage has not run when org/reviews.json is missing: no reviews then means unknown,
// not none.
func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	index := collect.Index{Repos: []collect.IndexRepo{{Name: "lib", URL: "https://code.example/org/lib", Dir: "repos/lib",
		Files: map[string]string{"commits": "repos/lib/commits.jsonl"}}}}
	b, _ := json.Marshal(index)
	writeFile(t, dir, "index.json", string(b))
	writeFile(t, dir, "repos/lib/commits.jsonl", `{"repo":"lib","sha":"l1"}`+"\n"+`{"repo":"lib","sha":"l2"}`+"\n")
	raw, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "a project without project.json", []string{raw.Projects[0].Name, raw.Projects[0].URL}, []string{"lib", "https://code.example/org/lib"})
	check(t, "commits", len(raw.Commits), 2)
	check(t, "no PRs, reviews or decisions files: none", []int{len(raw.PRs), len(raw.Reviews), len(raw.Decisions)}, []int{0, 0, 0})
	check(t, "the reviews stage ran", raw.ReviewsRan, false)

	writeFile(t, dir, "repos/lib/commits.jsonl", `{"repo":"lib"}`+"\n{not json\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), filepath.Join("repos", "lib", "commits.jsonl")) {
		t.Errorf("Load with a broken commits file: error = %v, want one naming the file", err)
	}
	writeFile(t, dir, "index.json", "{")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "index.json") {
		t.Errorf("Load with a broken index.json: error = %v, want one naming it", err)
	}
}

func TestReadJSON(t *testing.T) {
	dir := t.TempDir()
	var v map[string]int
	if ok, err := readJSON(filepath.Join(dir, "missing.json"), &v); ok || err != nil {
		t.Errorf("readJSON(missing) = %v, %v; want false, nil", ok, err)
	}
	p := writeFile(t, dir, "a.json", `{"x": 1}`)
	if ok, err := readJSON(p, &v); !ok || err != nil || v["x"] != 1 {
		t.Errorf("readJSON(a.json) = %v, %v, %v; want true, nil, x=1", ok, err, v)
	}
	p = writeFile(t, dir, "bad.json", `{"x":`)
	if ok, err := readJSON(p, &v); ok || err == nil || !strings.Contains(err.Error(), "decoding "+p) {
		t.Errorf("readJSON(bad.json) = %v, %v; want an error naming the file", ok, err)
	}
	if _, err := readJSON(dir, &v); err == nil { // a directory: not missing, unreadable
		t.Error("readJSON(a directory) = nil error, want one")
	}
}

func TestReadJSONL(t *testing.T) {
	dir := t.TempDir()
	type row struct{ N int }
	for _, name := range []string{"", "missing.jsonl"} {
		if rows, err := readJSONL[row](dir, name); rows != nil || err != nil {
			t.Errorf("readJSONL(%q) = %v, %v; want no rows", name, rows, err)
		}
	}
	writeFile(t, dir, "rows.jsonl", "{\"N\":1}\n{\"N\":2}\n\n{\"N\":3}")
	rows, err := readJSONL[row](dir, "rows.jsonl")
	check(t, "rows, blank lines and a last line without a newline read", []any{rows, err}, []any{[]row{{1}, {2}, {3}}, nil})
	writeFile(t, dir, "bad.jsonl", "{\"N\":1}\n{\"N\":\n")
	if _, err := readJSONL[row](dir, "bad.jsonl"); err == nil || !strings.Contains(err.Error(), "bad.jsonl") {
		t.Errorf("readJSONL(bad.jsonl) error = %v, want one naming the file", err)
	}
	// a failure that is not a bad row still fails, rather than ending the file early
	writeFile(t, dir, "stray.jsonl", "{\"N\":1}\n]\n{\"N\":2}\n")
	if rows, err := readJSONL[row](dir, "stray.jsonl"); err == nil {
		t.Errorf("readJSONL(stray.jsonl) = %v, nil error; want one", rows)
	}
	if err := os.Mkdir(filepath.Join(dir, "dir.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if rows, err := readJSONL[row](dir, "dir.jsonl"); err == nil { // a directory: not missing, unreadable
		t.Errorf("readJSONL(a directory) = %v, nil error; want one", rows)
	}
}
