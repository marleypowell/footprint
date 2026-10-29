package careers

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const good = `{"employers": [
  {"id": "globex", "employer": "Globex", "from": "2022-09", "root": "data"},
  {"id": "acme", "employer": "Acme Ltd", "from": "2019-03", "to": "2022-08", "root": "data/acme", "frozen": true},
  {"id": "initech", "employer": "Initech", "from": "2015-01", "to": "2019-02"}
]}`

func TestParse(t *testing.T) {
	f, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Employers) != 3 || f.Employers[1].ID != "acme" || !f.Employers[1].Frozen {
		t.Errorf("Parse() = %+v", f.Employers)
	}
}

// A careers file is refused, with every problem named at once, when an id is malformed, reserved
// or taken twice, an employer has no name, a month is malformed or out of order, two datasets
// would share a directory, config, cache or tokens file, or an entry without a dataset sets paths.
func TestParseRefuses(t *testing.T) {
	entry := func(fields string) string { return `{"employers": [` + fields + `]}` }
	tests := []struct {
		name, text string
		want       []string // in the error
	}{
		{"not JSON", `{`, []string{"decoding"}},
		{"an unknown field", entry(`{"id": "a", "employer": "A", "from": "2020-01", "rot": "x"}`), []string{`unknown field "rot"`}},
		{"no employers", `{"employers": []}`, []string{"no employers"}},
		{"a malformed id", entry(`{"id": "Acme Ltd", "employer": "A", "from": "2020-01"}`), []string{`id "Acme Ltd"`}},
		{"the reserved id", entry(`{"id": "career", "employer": "A", "from": "2020-01"}`), []string{"reserved"}},
		{"an id twice", entry(`{"id": "a", "employer": "A", "from": "2020-01"}, {"id": "a", "employer": "B", "from": "2021-01"}`), []string{"share the id a"}},
		{"no name", entry(`{"id": "a", "employer": " ", "from": "2020-01"}`), []string{"employer is missing"}},
		{"a day, not a month", entry(`{"id": "a", "employer": "A", "from": "2020-01-05"}`), []string{`from "2020-01-05"`}},
		{"months out of order", entry(`{"id": "a", "employer": "A", "from": "2020-05", "to": "2020-01"}`), []string{"before from"}},
		{"a shared root", entry(`{"id": "a", "employer": "A", "from": "2020-01", "root": "data/x"}, {"id": "b", "employer": "B", "from": "2021-01", "root": "data/x/"}`),
			[]string{"share the root data/x"}},
		{"a shared cache", entry(`{"id": "a", "employer": "A", "from": "2020-01", "root": "data"}, {"id": "b", "employer": "B", "from": "2021-01", "root": "data/b", "cache": "cache"}`),
			[]string{"share the cache cache"}},
		{"paths without a dataset", entry(`{"id": "a", "employer": "A", "from": "2020-01", "frozen": true}`), []string{"need a root"}},
		{"two problems, both named", entry(`{"id": "Bad", "employer": "", "from": "2020-01"}`), []string{`id "Bad"`, "employer is missing"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.text))
			if err == nil {
				t.Fatal("Parse() accepted it")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("Parse() error %q has no %q", err, w)
				}
			}
		})
	}
}

// The dataset at data/ keeps today's paths; any other root holds its own config and tokens, with
// its mirrors under cache/<id>; set paths win; an entry without a root has none.
func TestPaths(t *testing.T) {
	tests := []struct {
		name string
		e    Employer
		want Paths
	}{
		{"today's dataset", Employer{ID: "globex", Root: "data"}, Paths{Config: "config.json", Env: ".env", Cache: "cache",
			Data: filepath.Join("data", "raw"), Facts: filepath.Join("data", "facts"), Notes: filepath.Join("data", "notes"),
			Context: filepath.Join("data", "context"), Drafts: filepath.Join("data", "drafts")}},
		{"another root", Employer{ID: "acme", Root: filepath.Join("data", "acme")}, Paths{
			Config: filepath.Join("data", "acme", "config.json"), Env: filepath.Join("data", "acme", ".env"), Cache: filepath.Join("cache", "acme"),
			Data: filepath.Join("data", "acme", "raw"), Facts: filepath.Join("data", "acme", "facts"), Notes: filepath.Join("data", "acme", "notes"),
			Context: filepath.Join("data", "acme", "context"), Drafts: filepath.Join("data", "acme", "drafts")}},
		{"set paths win", Employer{ID: "acme", Root: "old", Config: "acme.json", Cache: "/mnt/mirrors", Env: "acme.env"}, Paths{
			Config: "acme.json", Env: "acme.env", Cache: "/mnt/mirrors",
			Data: filepath.Join("old", "raw"), Facts: filepath.Join("old", "facts"), Notes: filepath.Join("old", "notes"),
			Context: filepath.Join("old", "context"), Drafts: filepath.Join("old", "drafts")}},
		{"no dataset", Employer{ID: "initech"}, Paths{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.e.Paths(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Paths() = %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// A dataset is full while it has raw data, facts-only once only its facts are kept, and context
// only without either (or without a root).
func TestKind(t *testing.T) {
	root := t.TempDir()
	mk := func(parts ...string) {
		if err := os.MkdirAll(filepath.Join(append([]string{root}, parts...)...), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk("full", "raw")
	mk("full", "facts")
	mk("frozen", "facts")
	mk("new")
	tests := map[string]string{"full": KindFull, "frozen": KindFacts, "new": KindContext, "missing": KindContext}
	for dir, want := range tests {
		if got := (Employer{ID: "x", Root: filepath.Join(root, dir)}).Kind(); got != want {
			t.Errorf("Kind(%s) = %s, want %s", dir, got, want)
		}
	}
	if got := (Employer{ID: "x"}).Kind(); got != KindContext {
		t.Errorf("Kind(no root) = %s", got)
	}
}

func TestGetAndFrozenAt(t *testing.T) {
	f, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if e, err := f.Get("acme"); err != nil || e.Employer != "Acme Ltd" {
		t.Errorf("Get(acme) = %+v, %v", e, err)
	}
	if _, err := f.Get("hooli"); err == nil || !strings.Contains(err.Error(), "globex, acme, initech") {
		t.Errorf("Get(hooli) = %v, want an error listing the ids", err)
	}
	for dir, want := range map[string]string{
		filepath.Join("data", "acme"): "acme", filepath.Join("data", "acme", "raw"): "acme", filepath.Join("data", "acme", "raw") + "/": "acme",
		filepath.Join("data", "raw"): "", "elsewhere": "",
	} {
		got := ""
		if e := f.FrozenAt(dir); e != nil {
			got = e.ID
		}
		if got != want {
			t.Errorf("FrozenAt(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestOrdered(t *testing.T) {
	f, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range f.Ordered() {
		ids = append(ids, e.ID)
	}
	if want := []string{"initech", "acme", "globex"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("Ordered() = %v, want %v, by start", ids, want)
	}
	if f.Employers[0].ID != "globex" {
		t.Error("Ordered() reordered the file's own list")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(filepath.Join(dir, "careers.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load(missing) = %v, want fs.ErrNotExist", err)
	}
	if Exists(filepath.Join(dir, "careers.json")) {
		t.Error("Exists(missing) = true")
	}
	p := filepath.Join(dir, "careers.json")
	if err := os.WriteFile(p, []byte(`{"employers": [{"id": "x"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !Exists(p) {
		t.Error("Exists() = false")
	}
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), p) {
		t.Errorf("Load(bad) = %v, want an error naming the file", err)
	}
}
