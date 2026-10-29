package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marleypowell/footprint"
	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/facts"
)

// The one change in the dataset newDataset writes, and its link.
const (
	changeID  = "pay@abc1234def56"
	changeURL = "https://code.example/acme/pay/commit/abc1234def56"
)

// newDataset writes a dataset of one repo with one of the user's commits, and returns its directory.
func newDataset(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "raw")
	me := collect.Signature{Name: "Jane Doe", Email: "jdoe@acme.test", Date: "2024-03-04T10:00:00Z"}
	added, deleted := 12, 3
	putJSON(t, filepath.Join(dir, "index.json"), collect.Index{
		Org: "acme", Login: "jdoe",
		Identity: collect.IdentityConfig{Emails: []string{me.Email}, Names: []string{me.Name}},
		Repos: []collect.IndexRepo{{Name: "pay", URL: "https://code.example/acme/pay", Dir: "repos/pay",
			Files: map[string]string{"commits": "repos/pay/commits.jsonl"}}},
	})
	putJSON(t, filepath.Join(dir, "repos", "pay", "commits.jsonl"), collect.Commit{
		Repo: "pay", SHA: "abc1234def56", URL: changeURL, Roles: []string{"author", "committer"},
		Author: me, Committer: me, Subject: "feat: retry declined payments", Message: "feat: retry declined payments",
		Parents: []string{"parent"}, OnDefaultBranch: true, PatchID: ptr("patch-1"), Stats: &collect.CommitStats{},
		Files: []collect.FileChange{{Path: "client/retry.go", Status: "M", Additions: &added, Deletions: &deleted}},
	})
	return dir
}

func ptr[T any](v T) *T { return &v }

func putJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// putFile writes a file under dir and returns its path.
func putFile(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// captureStdout runs fn with os.Stdout swapped for a pipe, and returns what fn wrote to it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = saved }()
	out := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		out <- string(b)
	}()
	fn()
	w.Close()
	return <-out
}

// factsOpts are the facts options with every directory in a fresh temporary directory.
func factsOpts(t *testing.T) factsOptions {
	dir := t.TempDir()
	return factsOptions{dir: filepath.Join(dir, "facts"), notes: filepath.Join(dir, "notes"), context: filepath.Join(dir, "context")}
}

func TestRunFactsWindowNeedsBothDays(t *testing.T) {
	tests := []struct {
		from, to string
		want     string // in the error
	}{
		{"2024-01-01", "", "give both -from and -to"},
		{"", "2024-12-31", "give both -from and -to"},
		{"2024-13-01", "2024-12-31", `window start "2024-13-01"`},
		{"2024-01-01", "end of year", `window end "end of year"`},
	}
	for _, tt := range tests {
		o := factsOpts(t)
		o.from, o.to = tt.from, tt.to
		// The window is checked before the data is read: there is none here.
		err := runFacts(filepath.Join(t.TempDir(), "missing"), o)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("runFacts(-from %q -to %q) = %v, want an error with %q", tt.from, tt.to, err, tt.want)
		}
	}
}

func TestRunFactsNeedsADataset(t *testing.T) {
	err := runFacts(t.TempDir(), factsOpts(t))
	if err == nil || !strings.Contains(err.Error(), "no index.json") {
		t.Errorf("runFacts(empty dir) = %v, want an error naming the missing index.json", err)
	}
}

func TestRunFactsWritesTheFactsAndTheWindow(t *testing.T) {
	o := factsOpts(t)
	o.from, o.to = "2024-01-01", "2024-12-31"
	if err := runFacts(newDataset(t), o); err != nil {
		t.Fatalf("runFacts = %v", err)
	}
	readme, err := os.ReadFile(filepath.Join(o.dir, "README.md"))
	if err != nil || !bytes.Equal(readme, footprint.FactsSchema) {
		t.Errorf("README.md is not the facts schema (err %v)", err)
	}
	changes, err := os.ReadFile(filepath.Join(o.dir, "changes.jsonl"))
	if err != nil || !strings.Contains(string(changes), `"`+changeID+`"`) {
		t.Errorf("changes.jsonl = %q (err %v), want the change %s", changes, err, changeID)
	}
	w, err := facts.NewWindow(o.from, o.to)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"todo.txt", filepath.Join("windows", w.Dir(), "todo.txt")} {
		if _, err := os.Stat(filepath.Join(o.dir, name)); err != nil {
			t.Errorf("runFacts wrote no %s: %v", name, err)
		}
	}
}

func TestRunCheckRefusesBadArguments(t *testing.T) {
	tests := []struct {
		name         string
		plain, links bool
		files        []string
		want         string
	}{
		{"no files", false, false, nil, "check needs files"},
		{"both final forms", true, true, []string{"draft.md"}, "-plain or -links, not both"},
	}
	for _, tt := range tests {
		o := factsOpts(t)
		o.plain, o.links = tt.plain, tt.links
		err := runCheck(filepath.Join(t.TempDir(), "missing"), o, tt.files)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("runCheck(%s) = %v, want an error with %q", tt.name, err, tt.want)
		}
	}
}

const (
	goodDraft = "---\nwindow: all\n---\nRetried declined payments [[" + changeID + "]].\n"
	badDraft  = "---\nwindow: all\n---\nRewrote the ledger [[pay@0000000deadbeef]].\n"
)

func TestRunCheckPassesADraftWhoseCitationsResolve(t *testing.T) {
	data, dir := newDataset(t), t.TempDir()
	draft := putFile(t, dir, "draft.md", goodDraft)
	var err error
	out := captureStdout(t, func() { err = runCheck(data, factsOpts(t), []string{draft}) })
	if err != nil {
		t.Errorf("runCheck(good draft) = %v, want nil; output:\n%s", err, out)
	}
	if want := draft + ": ok\n"; out != want {
		t.Errorf("runCheck(good draft) printed %q, want %q", out, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "draft.final.md")); err == nil {
		t.Error("runCheck wrote draft.final.md without -plain or -links")
	}
}

func TestRunCheckFailsTheFilesWithErrors(t *testing.T) {
	data, dir := newDataset(t), t.TempDir()
	good := putFile(t, dir, "good.md", goodDraft)
	bad := putFile(t, dir, "bad.md", badDraft)
	o := factsOpts(t)
	o.plain = true
	var err error
	out := captureStdout(t, func() { err = runCheck(data, o, []string{bad, good}) })
	if err == nil || err.Error() != "check: 1 of 2 files have errors" {
		t.Errorf("runCheck(bad, good) = %v, want check: 1 of 2 files have errors", err)
	}
	// Each finding is a line "<file>:<line>: error: ..."; the good file is still checked.
	if !strings.Contains(out, bad+":4: error: ") || !strings.Contains(out, good+": ok") {
		t.Errorf("runCheck(bad, good) printed:\n%s\nwant an error on %s line 4, and %s ok", out, bad, good)
	}
	if _, err := os.Stat(filepath.Join(dir, "bad.final.md")); err == nil {
		t.Error("runCheck -plain wrote a final version of a file with errors")
	}
	if _, err := os.Stat(filepath.Join(dir, "good.final.md")); err != nil {
		t.Errorf("runCheck -plain wrote no final version of the good file: %v", err)
	}
}

func TestRunCheckWritesTheFinalVersion(t *testing.T) {
	tests := []struct {
		name         string
		plain, links bool
		want         string
	}{
		{"-plain removes the citations", true, false, "Retried declined payments.\n"},
		{"-links turns them into links", false, true, "Retried declined payments [" + changeID + "](" + changeURL + ").\n"},
	}
	data := newDataset(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			draft := putFile(t, dir, "draft.md", goodDraft)
			o := factsOpts(t)
			o.plain, o.links = tt.plain, tt.links
			var err error
			out := captureStdout(t, func() { err = runCheck(data, o, []string{draft}) })
			if err != nil {
				t.Fatalf("runCheck = %v; output:\n%s", err, out)
			}
			final := filepath.Join(dir, "draft.final.md")
			if !strings.Contains(out, draft+": wrote "+final) {
				t.Errorf("runCheck printed %q, want it to name %s", out, final)
			}
			got, err := os.ReadFile(final)
			if err != nil || string(got) != tt.want {
				t.Errorf("draft.final.md = %q (err %v), want %q", got, err, tt.want)
			}
		})
	}
}

func TestRunCheckMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.md")
	if err := runCheck(newDataset(t), factsOpts(t), []string{missing}); err == nil || !strings.Contains(err.Error(), "nope.md") {
		t.Errorf("runCheck(missing file) = %v, want an error naming it", err)
	}
}
