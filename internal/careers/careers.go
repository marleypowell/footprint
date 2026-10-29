// Package careers reads careers.json: the user's datasets, one per job (or per
// org at one job), so that the CLI can run on any of them and a career view can take them
// together. Without the file nothing changes: today's single dataset under data/ is a career of one.
package careers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Reserved is the id of the career-wide view; no dataset may take it.
const Reserved = "career"

// Kinds of dataset, by what is on disk.
const (
	KindFull    = "full"    // raw/ exists: the facts can be built again
	KindFacts   = "facts"   // facts/ alone: a frozen snapshot of what counted
	KindContext = "context" // no dataset: the user's own words in the career context only
)

// DefaultRoot is today's dataset, whose files keep the paths they have without careers.json.
const DefaultRoot = "data"

// File is careers.json.
type File struct {
	Datasets []Dataset `json:"datasets"`
}

// Dataset is one employer's work (or one org's, at an employer), with the months the user was there
// and where its files are; or, marked personal, the user's own projects, which no months bound and
// no employer owns.
type Dataset struct {
	ID       string `json:"id"`                 // lower case, digits and -: the prefix of its citations (acme:PAY-62)
	Employer string `json:"employer"`           // the name documents use: the employer, or "Personal projects"
	Personal bool   `json:"personal,omitempty"` // the user's own projects, not a job
	From     string `json:"from,omitempty"`     // YYYY-MM; a personal dataset's may be left out
	To       string `json:"to,omitempty"`       // YYYY-MM; empty while the user is still there
	Root     string `json:"root,omitempty"`     // the dataset's directory (raw/, facts/, notes/, context/, drafts/); empty for none
	Config   string `json:"config,omitempty"`   // its config file; defaults below
	Cache    string `json:"cache,omitempty"`    // its mirrors and run state
	Env      string `json:"env,omitempty"`      // its tokens file
	Frozen   bool   `json:"frozen,omitempty"`   // a past job's record: never collected again; never a personal dataset
}

// Paths are the files of a dataset.
type Paths struct {
	Config, Env, Data, Cache, Facts, Notes, Context, Drafts string
}

var idRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Load reads and checks a careers file. A missing file is fs.ErrNotExist.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// Exists says whether there is a careers file at path.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !errors.Is(err, fs.ErrNotExist)
}

// Parse reads careers.json and checks it: every id well formed, unreserved and unique, every
// dataset named, months in order, and no two datasets sharing a directory, config, cache or
// tokens file.
func Parse(data []byte) (*File, error) {
	var f File
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("decoding: %w", err)
	}
	if len(f.Datasets) == 0 {
		return nil, errors.New("no datasets")
	}
	var errs []error
	seen := map[string]map[string]string{"id": {}, "root": {}, "config": {}, "cache": {}, "env": {}}
	claim := func(what, value, id string) {
		if value == "" {
			return
		}
		if what != "id" {
			value = filepath.Clean(value)
		}
		if other, ok := seen[what][value]; ok {
			errs = append(errs, fmt.Errorf("%s and %s share the %s %s", other, id, what, value))
			return
		}
		seen[what][value] = id
	}
	for _, e := range f.Datasets {
		switch {
		case !idRE.MatchString(e.ID):
			errs = append(errs, fmt.Errorf("id %q: want lower case letters, digits and -", e.ID))
		case e.ID == Reserved:
			errs = append(errs, fmt.Errorf("id %q is reserved for the career view", e.ID))
		}
		if strings.TrimSpace(e.Employer) == "" {
			errs = append(errs, fmt.Errorf("%s: employer is missing (the name documents use)", e.ID))
		}
		if e.Personal && e.Frozen {
			errs = append(errs, fmt.Errorf("%s: a personal dataset is never frozen: it is no past job's record", e.ID))
		}
		from, errFrom := time.Parse("2006-01", e.From)
		if errFrom != nil && !(e.Personal && e.From == "") {
			errs = append(errs, fmt.Errorf("%s: from %q: want YYYY-MM", e.ID, e.From))
		}
		if e.To != "" {
			to, err := time.Parse("2006-01", e.To)
			switch {
			case err != nil:
				errs = append(errs, fmt.Errorf("%s: to %q: want YYYY-MM, or nothing while still there", e.ID, e.To))
			case errFrom == nil && to.Before(from):
				errs = append(errs, fmt.Errorf("%s: to %s is before from %s", e.ID, e.To, e.From))
			}
		}
		if e.Root == "" && (e.Config != "" || e.Cache != "" || e.Env != "" || e.Frozen) {
			errs = append(errs, fmt.Errorf("%s: config, cache, env and frozen need a root: without one it has no dataset", e.ID))
		}
		claim("id", e.ID, e.ID)
		if e.Root != "" {
			p := e.Paths()
			claim("root", e.Root, e.ID)
			claim("config", p.Config, e.ID)
			claim("cache", p.Cache, e.ID)
			claim("env", p.Env, e.ID)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return &f, nil
}

// Get is the dataset with the id.
func (f *File) Get(id string) (*Dataset, error) {
	ids := make([]string, 0, len(f.Datasets))
	for i := range f.Datasets {
		if f.Datasets[i].ID == id {
			return &f.Datasets[i], nil
		}
		ids = append(ids, f.Datasets[i].ID)
	}
	return nil, fmt.Errorf("no dataset %q in the careers file (it has %s)", id, strings.Join(ids, ", "))
}

// FrozenAt is the frozen dataset whose directory is dir (its raw/ or its root), if any.
func (f *File) FrozenAt(dir string) *Dataset {
	dir = filepath.Clean(dir)
	for i := range f.Datasets {
		e := &f.Datasets[i]
		if e.Frozen && e.Root != "" && (dir == filepath.Clean(e.Root) || dir == filepath.Clean(e.Paths().Data)) {
			return e
		}
	}
	return nil
}

// Paths are where a dataset's files are. The dataset at data/ keeps the paths it has without
// careers.json (config.json, .env and cache/ beside it); any other root holds its own config and
// tokens, and its mirrors go under cache/<id>. Set config, cache or env to put them elsewhere.
func (e Dataset) Paths() Paths {
	if e.Root == "" {
		return Paths{}
	}
	p := Paths{
		Data: filepath.Join(e.Root, "raw"), Facts: filepath.Join(e.Root, "facts"), Notes: filepath.Join(e.Root, "notes"),
		Context: filepath.Join(e.Root, "context"), Drafts: filepath.Join(e.Root, "drafts"),
		Config: filepath.Join(e.Root, "config.json"), Env: filepath.Join(e.Root, ".env"), Cache: filepath.Join("cache", e.ID),
	}
	if filepath.Clean(e.Root) == DefaultRoot {
		p.Config, p.Env, p.Cache = "config.json", ".env", "cache"
	}
	if e.Config != "" {
		p.Config = e.Config
	}
	if e.Cache != "" {
		p.Cache = e.Cache
	}
	if e.Env != "" {
		p.Env = e.Env
	}
	return p
}

// Kind is what the dataset holds, by what is on disk.
func (e Dataset) Kind() string {
	if e.Root == "" {
		return KindContext
	}
	p := e.Paths()
	if isDir(p.Data) {
		return KindFull
	}
	if isDir(p.Facts) {
		return KindFacts
	}
	return KindContext
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// SetFrozen marks a dataset frozen: it is a past job's record from now on.
func (f *File) SetFrozen(id string) error {
	e, err := f.Get(id)
	if err != nil {
		return err
	}
	if e.Root == "" {
		return fmt.Errorf("%s has no dataset to freeze", id)
	}
	if e.Personal {
		return fmt.Errorf("%s is personal: only a past job's dataset is frozen", id)
	}
	e.Frozen = true
	return nil
}

// Save writes the careers file, one dataset per line, as it reads best by hand.
func (f *File) Save(path string) error {
	var b strings.Builder
	b.WriteString("{\"datasets\": [\n")
	for i, e := range f.Datasets {
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		b.WriteString("  " + string(line))
		if i < len(f.Datasets)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("]}\n")
	if _, err := Parse([]byte(b.String())); err != nil {
		return fmt.Errorf("not saving %s: %w", path, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Ordered are the jobs by when the user started, then id, and then the personal datasets, dated
// ones first: a career's order.
func (f *File) Ordered() []Dataset {
	out := slices.Clone(f.Datasets)
	slices.SortStableFunc(out, func(a, b Dataset) int {
		if a.Personal != b.Personal {
			if a.Personal {
				return 1
			}
			return -1
		}
		if (a.From == "") != (b.From == "") { // undated last
			if a.From == "" {
				return 1
			}
			return -1
		}
		if a.From != b.From {
			return strings.Compare(a.From, b.From)
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}
