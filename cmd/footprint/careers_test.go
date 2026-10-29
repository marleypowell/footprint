package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marleypowell/footprint/internal/careers"
	"github.com/marleypowell/footprint/internal/collect"
)

const careersFile = `{"datasets": [
  {"id": "globex", "employer": "Globex", "from": "2022-09", "root": "data"},
  {"id": "acme", "employer": "Acme Ltd", "from": "2019-03", "to": "2022-08", "root": "old/acme", "frozen": true},
  {"id": "initech", "employer": "Initech", "from": "2015-01", "to": "2019-02"}
]}`

func writeCareers(t *testing.T) string {
	t.Helper()
	return putFile(t, t.TempDir(), "careers.json", careersFile)
}

// -dataset points every path flag the command line did not set at the dataset; a flag
// given wins. Without -dataset nothing changes.
func TestApplyDataset(t *testing.T) {
	path := writeCareers(t)
	defaults := func() (collect.Options, factsOptions, string) {
		return collect.Options{ConfigPath: "config.json", DataDir: "data/raw", CacheDir: "cache"},
			factsOptions{dir: "data/facts", notes: "data/notes", context: "data/context"}, ".env"
	}

	opts, fo, env := defaults()
	e, err := applyDataset(path, "acme", map[string]bool{"notes": true}, &opts, &fo, &env)
	if err != nil || e == nil || e.ID != "acme" {
		t.Fatalf("applyDataset(acme) = %+v, %v", e, err)
	}
	acme := filepath.Join("old", "acme")
	got := []string{opts.ConfigPath, opts.DataDir, opts.CacheDir, env, fo.dir, fo.notes, fo.context}
	want := []string{filepath.Join(acme, "config.json"), filepath.Join(acme, "raw"), filepath.Join("cache", "acme"), filepath.Join(acme, ".env"),
		filepath.Join(acme, "facts"), "data/notes", filepath.Join(acme, "context")}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("path %d = %q, want %q (notes was given, so it stays)", i, got[i], want[i])
		}
	}

	opts, fo, env = defaults()
	if e, err := applyDataset(path, "", nil, &opts, &fo, &env); e != nil || err != nil || opts.DataDir != "data/raw" {
		t.Errorf("without -dataset: %+v, %v, data %q", e, err, opts.DataDir)
	}

	tests := []struct {
		name, careers, id, want string
	}{
		{"no careers file", filepath.Join(t.TempDir(), "careers.json"), "acme", "needs"},
		{"an unknown dataset", path, "hooli", `no dataset "hooli"`},
		{"an employer with no dataset on disk", path, "initech", "initech has no dataset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, fo, env := defaults()
			if _, err := applyDataset(tt.careers, tt.id, nil, &opts, &fo, &env); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("applyDataset(%s) = %v, want an error with %q", tt.id, err, tt.want)
			}
		})
	}
}

// Collection refuses a frozen dataset, whether named with -dataset or by its
// directory with -data; any other dataset, or no careers file, is collected as before.
func TestRefuseFrozen(t *testing.T) {
	path := writeCareers(t)
	opts, fo, env := collect.Options{}, factsOptions{}, ""
	acme, err := applyDataset(path, "acme", nil, &opts, &fo, &env)
	if err != nil {
		t.Fatal(err)
	}
	globex, err := applyDataset(path, "globex", nil, &opts, &fo, &env)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, careers, data string
		dataset             string
		refused             bool
	}{
		{"the frozen dataset", path, "", "acme", true},
		{"its raw directory, without -dataset", path, filepath.Join("old", "acme", "raw"), "", true},
		{"its root, without -dataset", path, filepath.Join("old", "acme"), "", true},
		{"the current job's", path, "", "globex", false},
		{"today's dataset", path, filepath.Join("data", "raw"), "", false},
		{"no careers file", filepath.Join(t.TempDir(), "careers.json"), filepath.Join("old", "acme", "raw"), "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := refuseFrozen(tt.careers, map[string]*careers.Dataset{"acme": acme, "globex": globex}[tt.dataset], tt.data)
			if (err != nil) != tt.refused || (err != nil && !strings.Contains(err.Error(), "acme is frozen")) {
				t.Errorf("refuseFrozen() = %v, want refused %v", err, tt.refused)
			}
		})
	}
}

// datasets lists the datasets by when the user started there, with the kind on disk.
func TestRunDatasets(t *testing.T) {
	var b strings.Builder
	if err := runDatasets(&b, writeCareers(t)); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "initech\tInitech\t2015-01..2019-02\tcontext") ||
		!strings.HasPrefix(lines[1], "acme\tAcme Ltd\t2019-03..2022-08\tcontext\tfrozen\told/acme") || !strings.HasPrefix(lines[2], "globex\tGlobex\t2022-09..now\t") {
		t.Errorf("datasets:\n%s", b.String())
	}
	if err := runDatasets(&b, filepath.Join(t.TempDir(), "careers.json")); err == nil || !strings.Contains(err.Error(), "no ") {
		t.Errorf("runDatasets(no file) = %v", err)
	}
}

// freeze builds a past job's facts a last time, hashes them, and marks the dataset frozen; then
// a rebuild or collection is refused, check passes the same draft from the raw data and, once
// -drop-raw has removed it, from the stored facts.
func TestRunFreeze(t *testing.T) {
	data := newDataset(t)
	root := filepath.Dir(data)
	careersPath := putFile(t, t.TempDir(), "careers.json", `{"datasets": [
  {"id": "acme", "employer": "Acme Ltd", "from": "2019-03", "to": "2024-08", "root": "`+filepath.ToSlash(root)+`"}
]}`)
	dataset := func() (*careers.Dataset, collect.Options, factsOptions) {
		t.Helper()
		var opts collect.Options
		var fo factsOptions
		env := ""
		e, err := applyDataset(careersPath, "acme", nil, &opts, &fo, &env)
		if err != nil {
			t.Fatal(err)
		}
		return e, opts, fo
	}
	draft := putFile(t, t.TempDir(), "draft.md", goodDraft)
	checkDraft := func(what string) {
		t.Helper()
		_, opts, fo := dataset()
		var err error
		out := captureStdout(t, func() { err = runCheck(opts.DataDir, fo, []string{draft}) })
		if err != nil || out != draft+": ok\n" {
			t.Errorf("check %s = %v:\n%s", what, err, out)
		}
	}

	if err := runFreeze(careersPath, nil, collect.Options{}, factsOptions{}, freezeOptions{}, time.Now()); err == nil || !strings.Contains(err.Error(), "needs -dataset") {
		t.Errorf("runFreeze(no dataset) = %v", err)
	}
	personal := &careers.Dataset{ID: "personal", Employer: "Personal projects", Personal: true, Root: root}
	if err := runFreeze(careersPath, personal, collect.Options{}, factsOptions{}, freezeOptions{}, time.Now()); err == nil || !strings.Contains(err.Error(), "is personal") {
		t.Errorf("runFreeze(personal) = %v, want it refused", err)
	}

	e, opts, fo := dataset()
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	if err := runFreeze(careersPath, e, opts, fo, freezeOptions{}, at); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "facts", "frozen.json")); err != nil {
		t.Errorf("no frozen.json: %v", err)
	}
	e, opts, fo = dataset()
	if !e.Frozen {
		t.Error("the careers file does not mark acme frozen")
	}
	if err := refuseFrozen(careersPath, e, opts.DataDir); err == nil {
		t.Error("a frozen dataset's facts can be rebuilt")
	}
	checkDraft("from the raw data")
	if err := runFreeze(careersPath, e, opts, fo, freezeOptions{}, at); err == nil || !strings.Contains(err.Error(), "frozen already") {
		t.Errorf("a second freeze = %v", err)
	}

	// Freeze again, from a copy not yet frozen, removing the raw data and the packs.
	if err := os.Remove(filepath.Join(root, "facts", "frozen.json")); err != nil {
		t.Fatal(err)
	}
	if err := runFreeze(careersPath, e, opts, fo, freezeOptions{dropRaw: true, dropPacks: true}, at); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(data); err == nil {
		t.Error("-drop-raw kept the raw data")
	}
	if _, err := os.Stat(filepath.Join(root, "facts", "packs")); err == nil {
		t.Error("-drop-packs kept the packs")
	}
	if e, _, _ := dataset(); e.Kind() != careers.KindFacts {
		t.Errorf("kind after -drop-raw = %s, want facts", e.Kind())
	}
	checkDraft("from the stored facts")
}
