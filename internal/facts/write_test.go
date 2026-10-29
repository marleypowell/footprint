package facts

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// What the dataset leaves out depends on whether the reviews stage ran.
func TestNotCollected(t *testing.T) {
	f := &Facts{raw: &Raw{}}
	check(t, "before the reviews stage", f.notCollected(), notCollected)
	f.raw.ReviewsRan = true
	got := f.notCollected()
	check(t, "after it", []string{got[0], got[1]}, []string{reviewsNotCollected, notCollected[1]})
	check(t, "the list itself is left as it was", notCollected[0] != reviewsNotCollected, true)
}

func TestSummary(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "GONE-1.md", "---\nitem: GONE-1\ninputs: x\n---\n")
	writeFile(t, dir, "ENG-1.md", "---\nitem: ENG-1\ninputs: old\n---\n")
	f := build(t, fixture(), Inputs{NotesDir: dir})
	s := f.Summary(All)
	check(t, "a summary", []any{s.PackVersion, s.Window, s.Coverage.First, s.Coverage.Last, s.Coverage.PRsFrom, s.Framework == nil, len(s.ByYear)},
		[]any{PackVersion, "all", "2024-01-02", "2024-05-01", "2024-01-03", true, 1})
	check(t, "notes", s.Notes, NoteCounts{Required: 4, Invalid: 1, Missing: 3, Optional: 3, Orphans: 1})
	w, _ := NewWindow("2024-04-01", "2024-06-30")
	s = f.Summary(w)
	check(t, "a window's", []any{s.Window, s.Coverage.First, s.Coverage.PRsFrom, s.Notes.Required, s.Totals.PRsOpened},
		[]any{"2024-04-01..2024-06-30", "2024-04-01", "2024-01-03", 0, 2})
}

// An item's view in a window has the window's totals, days and span, and keeps its all-time
// effort; the item itself is unchanged.
func TestWindowItems(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	check(t, "all time: the items", len(f.windowItems(All)), len(f.Items))
	w, _ := NewWindow("2024-01-01", "2024-01-31")
	var view *Item
	for _, it := range f.windowItems(w) {
		if it.ID == "ENG-1" {
			view = it
		}
	}
	it := f.items["ENG-1"]
	check(t, "the view", []any{view.First, view.Last, view.ActiveDays, view.Effort, view.Totals.Commits},
		[]any{"2024-01-02", "2024-01-11", 4, it.Effort, 3}) // its span ends with a review naming it
	check(t, "the item", []any{it.First, it.Last, it.ActiveDays, it.Totals.Commits}, []any{"2024-01-02", "2024-03-02", 5, 4})
}

// Write replaces the directory whole, through a temporary one, and keeps a view of each window the
// directory had.
func TestWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "facts")
	writeFile(t, dir, "stale.txt", "from an earlier run")
	writeFile(t, dir, "windows/2023-01-01_2023-12-31/overview.md", "old")
	writeFile(t, dir, "windows/notes/x.md", "not a window")
	writeFile(t, dir, "windows/2024-13-01_2024-12-31/overview.md", "not a window either")
	f := build(t, fixture(), Inputs{})
	w, _ := NewWindow("2024-01-01", "2024-03-31")
	if err := f.Write(dir, w, w); err != nil {
		t.Fatal(err)
	}
	var files []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return err
	})
	for _, want := range []string{"README.md", "facts.json", "items.jsonl", "changes.jsonl", "issues.jsonl", "docs.jsonl", "repos.jsonl",
		"reviews.jsonl", "technologies.jsonl", "overview.md", "catalogue.md", "todo.txt", "packs/ENG-1.md", "packs/repo+app+2024q1.md",
		"windows/2024-01-01_2024-03-31/facts.json", "windows/2024-01-01_2024-03-31/items.jsonl", "windows/2024-01-01_2024-03-31/overview.md",
		"windows/2024-01-01_2024-03-31/todo.txt", "windows/2023-01-01_2023-12-31/facts.json"} {
		if !slices.Contains(files, want) {
			t.Errorf("Write wrote no %s: %v", want, files)
		}
	}
	for _, name := range files {
		if name == "stale.txt" || strings.HasPrefix(name, "windows/notes") || strings.HasPrefix(name, "windows/2024-13") ||
			strings.HasSuffix(name, "criteria.md") || name == "tags.txt" {
			t.Errorf("Write left or wrote %s", name)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "windows/2023-01-01_2023-12-31/overview.md")); !strings.HasPrefix(string(b), "# Your work: 2023-01-01..2023-12-31") {
		t.Errorf("a window the directory had is not rewritten: %q", b)
	}
	for _, aside := range []string{dir + ".tmp", dir + ".old"} {
		if _, err := os.Stat(aside); !os.IsNotExist(err) {
			t.Errorf("Write left %s", aside)
		}
	}
	if err := build(t, fixture(), Inputs{}).Write(filepath.Join(t.TempDir(), "new")); err != nil {
		t.Errorf("Write to a directory that is not there yet: %v", err)
	}
}

// Output is JSON as written, not escaped for HTML; jsonl has one row per line.
func TestMarshal(t *testing.T) {
	b, err := marshal(map[string]string{"a": "<b> & c"})
	check(t, "marshal", []any{string(b), err}, []any{"{\n  \"a\": \"<b> & c\"\n}\n", nil})
	b, err = jsonl([]Ref{{ID: "x", Title: "<1>"}, {ID: "y"}})
	check(t, "jsonl", []any{string(b), err}, []any{"{\"id\":\"x\",\"title\":\"<1>\"}\n{\"id\":\"y\",\"title\":\"\"}\n", nil})
	b, _ = jsonl([]Ref{})
	check(t, "jsonl of nothing", string(b), "")
	if _, err := marshal(func() {}); err == nil || !strings.Contains(err.Error(), "encoding facts") {
		t.Errorf("marshal(a func) error = %v", err)
	}
	if _, err := jsonl([]any{func() {}}); err == nil {
		t.Error("jsonl(a func) = nil error")
	}
}
