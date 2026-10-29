// Package careers reads careers.json: the datasets of the user's employers, one per job (or per
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

// Reserved is the id of the career-wide view; no employer may take it.
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
	Employers []Employer `json:"employers"`
}

// Employer is one dataset: an employer, or one org at an employer, with the months the user was
// there and where its files are.
type Employer struct {
	ID       string `json:"id"`       // lower case, digits and -: the prefix of its citations (acme:PAY-62)
	Employer string `json:"employer"` // the name documents use
	From     string `json:"from"`     // YYYY-MM
	To       string `json:"to"`       // YYYY-MM; empty while the user is still there
	Root     string `json:"root"`     // the dataset's directory (raw/, facts/, notes/, context/, drafts/); empty for none
	Config   string `json:"config"`   // its config file; defaults below
	Cache    string `json:"cache"`    // its mirrors and run state
	Env      string `json:"env"`      // its tokens file
	Frozen   bool   `json:"frozen"`   // a past employer's: never collected again
}

// Paths are the files of an employer's dataset.
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
// employer named, months in order, and no two datasets sharing a directory, config, cache or
// tokens file.
func Parse(data []byte) (*File, error) {
	var f File
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("decoding: %w", err)
	}
	if len(f.Employers) == 0 {
		return nil, errors.New("no employers")
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
	for _, e := range f.Employers {
		switch {
		case !idRE.MatchString(e.ID):
			errs = append(errs, fmt.Errorf("id %q: want lower case letters, digits and -", e.ID))
		case e.ID == Reserved:
			errs = append(errs, fmt.Errorf("id %q is reserved for the career view", e.ID))
		}
		if strings.TrimSpace(e.Employer) == "" {
			errs = append(errs, fmt.Errorf("%s: employer is missing", e.ID))
		}
		from, errFrom := time.Parse("2006-01", e.From)
		if errFrom != nil {
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

// Get is the employer with the id.
func (f *File) Get(id string) (*Employer, error) {
	ids := make([]string, 0, len(f.Employers))
	for i := range f.Employers {
		if f.Employers[i].ID == id {
			return &f.Employers[i], nil
		}
		ids = append(ids, f.Employers[i].ID)
	}
	return nil, fmt.Errorf("no employer %q in the careers file (it has %s)", id, strings.Join(ids, ", "))
}

// FrozenAt is the frozen employer whose dataset is dir (its raw/ or its root), if any.
func (f *File) FrozenAt(dir string) *Employer {
	dir = filepath.Clean(dir)
	for i := range f.Employers {
		e := &f.Employers[i]
		if e.Frozen && e.Root != "" && (dir == filepath.Clean(e.Root) || dir == filepath.Clean(e.Paths().Data)) {
			return e
		}
	}
	return nil
}

// Paths are where an employer's files are. The dataset at data/ keeps the paths it has without
// careers.json (config.json, .env and cache/ beside it); any other root holds its own config and
// tokens, and its mirrors go under cache/<id>. Set config, cache or env to put them elsewhere.
func (e Employer) Paths() Paths {
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

// Kind is what the employer's dataset holds, by what is on disk.
func (e Employer) Kind() string {
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

// Ordered are the employers by when the user started, then id: a career's order.
func (f *File) Ordered() []Employer {
	out := slices.Clone(f.Employers)
	slices.SortStableFunc(out, func(a, b Employer) int {
		if a.From != b.From {
			return strings.Compare(a.From, b.From)
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}
