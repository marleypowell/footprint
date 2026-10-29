package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/careers"
	"github.com/marleypowell/footprint/internal/collect"
)

const careersFile = `{"employers": [
  {"id": "globex", "employer": "Globex", "from": "2022-09", "root": "data"},
  {"id": "acme", "employer": "Acme Ltd", "from": "2019-03", "to": "2022-08", "root": "old/acme", "frozen": true},
  {"id": "initech", "employer": "Initech", "from": "2015-01", "to": "2019-02"}
]}`

func writeCareers(t *testing.T) string {
	t.Helper()
	return putFile(t, t.TempDir(), "careers.json", careersFile)
}

// -employer points every path flag the command line did not set at the employer's dataset; a flag
// given wins. Without -employer nothing changes.
func TestApplyEmployer(t *testing.T) {
	path := writeCareers(t)
	defaults := func() (collect.Options, factsOptions, string) {
		return collect.Options{ConfigPath: "config.json", DataDir: "data/raw", CacheDir: "cache"},
			factsOptions{dir: "data/facts", notes: "data/notes", context: "data/context"}, ".env"
	}

	opts, fo, env := defaults()
	e, err := applyEmployer(path, "acme", map[string]bool{"notes": true}, &opts, &fo, &env)
	if err != nil || e == nil || e.ID != "acme" {
		t.Fatalf("applyEmployer(acme) = %+v, %v", e, err)
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
	if e, err := applyEmployer(path, "", nil, &opts, &fo, &env); e != nil || err != nil || opts.DataDir != "data/raw" {
		t.Errorf("without -employer: %+v, %v, data %q", e, err, opts.DataDir)
	}

	tests := []struct {
		name, careers, id, want string
	}{
		{"no careers file", filepath.Join(t.TempDir(), "careers.json"), "acme", "needs"},
		{"an unknown employer", path, "hooli", `no employer "hooli"`},
		{"an employer without a dataset", path, "initech", "initech has no dataset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, fo, env := defaults()
			if _, err := applyEmployer(tt.careers, tt.id, nil, &opts, &fo, &env); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("applyEmployer(%s) = %v, want an error with %q", tt.id, err, tt.want)
			}
		})
	}
}

// Collection refuses a frozen employer's dataset, whether named with -employer or by its
// directory with -data; any other dataset, or no careers file, is collected as before.
func TestRefuseFrozen(t *testing.T) {
	path := writeCareers(t)
	opts, fo, env := collect.Options{}, factsOptions{}, ""
	acme, err := applyEmployer(path, "acme", nil, &opts, &fo, &env)
	if err != nil {
		t.Fatal(err)
	}
	globex, err := applyEmployer(path, "globex", nil, &opts, &fo, &env)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, careers, data string
		employer            string
		refused             bool
	}{
		{"the frozen employer", path, "", "acme", true},
		{"its raw directory, without -employer", path, filepath.Join("old", "acme", "raw"), "", true},
		{"its root, without -employer", path, filepath.Join("old", "acme"), "", true},
		{"the current employer", path, "", "globex", false},
		{"today's dataset", path, filepath.Join("data", "raw"), "", false},
		{"no careers file", filepath.Join(t.TempDir(), "careers.json"), filepath.Join("old", "acme", "raw"), "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := refuseFrozen(tt.careers, map[string]*careers.Employer{"acme": acme, "globex": globex}[tt.employer], tt.data)
			if (err != nil) != tt.refused || (err != nil && !strings.Contains(err.Error(), "acme is frozen")) {
				t.Errorf("refuseFrozen() = %v, want refused %v", err, tt.refused)
			}
		})
	}
}

// employers lists the datasets by when the user started there, with the kind on disk.
func TestRunEmployers(t *testing.T) {
	var b strings.Builder
	if err := runEmployers(&b, writeCareers(t)); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "initech\tInitech\t2015-01..2019-02\tcontext") ||
		!strings.HasPrefix(lines[1], "acme\tAcme Ltd\t2019-03..2022-08\tcontext\tfrozen\told/acme") || !strings.HasPrefix(lines[2], "globex\tGlobex\t2022-09..now\t") {
		t.Errorf("employers:\n%s", b.String())
	}
	if err := runEmployers(&b, filepath.Join(t.TempDir(), "careers.json")); err == nil || !strings.Contains(err.Error(), "no ") {
		t.Errorf("runEmployers(no file) = %v", err)
	}
}
