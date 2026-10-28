package facts

import (
	"cmp"
	"path"
	"slices"
	"strings"

	"github.com/marleypowell/footprint/internal/catalog"
)

// Repo is one line of repos.jsonl: a repository the user worked in.
type Repo struct {
	Name        string      `json:"name"`
	URL         string      `json:"url"`
	Description string      `json:"description"`
	Archived    bool        `json:"archived"`
	Systems     []string    `json:"systems"` // the catalog systems it belongs to
	Domains     []string    `json:"domains"`
	First       string      `json:"first"` // the first and last day of the user's activity in it
	Last        string      `json:"last"`
	StartedByMe bool        `json:"started_by_me"` // the user made its first commit
	Totals      Totals      `json:"totals"`
	Extensions  []Extension `json:"extensions"` // of the files the user's landed changes touched, most lines added first
	// Technologies are its landed changes', most lines first
	Technologies []string `json:"technologies"`
	Items        []string `json:"items"` // the items with most of the user's days in it
}

// Extension is a kind of file the user changed, by extension (or name, for files without one).
type Extension struct {
	Ext   string `json:"ext"`
	Files int    `json:"files"`
	Added int    `json:"added"`
}

const (
	maxRepoExtensions = 12
	maxRepoItems      = 10
)

func (f *Facts) buildRepos() {
	byRepo := map[string][]event{}
	for _, e := range f.events {
		if e.repo != "" {
			byRepo[e.repo] = append(byRepo[e.repo], e)
		}
	}
	firstAuthored := map[string]string{}
	for i := range f.raw.Commits {
		c := &f.raw.Commits[i]
		if d := day(commitTime(c)); hasRole(c, "author") && (firstAuthored[c.Repo] == "" || d < firstAuthored[c.Repo]) {
			firstAuthored[c.Repo] = d
		}
	}
	for i, p := range f.raw.Projects {
		evs := byRepo[p.Name]
		r := &Repo{Name: p.Name, URL: p.URL, Description: deref(p.CodeHost.Description), Archived: p.CodeHost.IsArchived,
			Systems: []string{}, Domains: []string{}, Totals: totalsOf(evs, All), Extensions: []Extension{}, Items: []string{}}
		if days := activeDays(evs, All); len(days) > 0 {
			r.First, r.Last = days[0], days[len(days)-1]
		}
		if ix := f.raw.Index.Repos; i < len(ix) && ix[i].FirstCommitInRepo != nil {
			if t, ok := parseTime(*ix[i].FirstCommitInRepo); ok && firstAuthored[p.Name] == day(t) {
				r.StartedByMe = true
			}
		}
		systems, domains := map[string]bool{}, map[string]bool{}
		note := func(e *catalog.Entity) {
			if e == nil {
				return
			}
			name := deref(e.Title)
			if name == "" {
				name = e.Ref
			}
			switch e.Category {
			case catalog.System:
				systems[name] = true
			case catalog.Domain:
				domains[name] = true
			}
		}
		for j := range p.Catalog.Entities {
			note(&p.Catalog.Entities[j])
		}
		for _, ref := range sortedKeys(p.Catalog.Context) {
			note(p.Catalog.Context[ref])
		}
		r.Systems, r.Domains = sortedSet(systems), sortedSet(domains)
		r.Extensions = f.extensions(p.Name)
		var ids []string
		for _, c := range f.Changes {
			if c.Repo == p.Name {
				ids = append(ids, c.ID)
			}
		}
		r.Technologies = f.techsOf(ids)
		r.Items = f.repoItems(evs)
		f.Repos = append(f.Repos, r)
	}
	slices.SortFunc(f.Repos, func(a, b *Repo) int { return cmp.Compare(a.Name, b.Name) })
}

// extensions are the kinds of file the user's landed changes in repo touched, generated files and
// bulk changes left out.
func (f *Facts) extensions(repo string) []Extension {
	files, added := map[string]map[string]bool{}, map[string]int{}
	for _, c := range f.Changes {
		if c.Repo != repo || !c.Landed || c.Class == CoAuthor {
			continue
		}
		for _, fc := range c.files {
			if fc.generated || fc.bulky {
				continue
			}
			ext := strings.ToLower(path.Ext(fc.path))
			if ext == "" {
				ext = path.Base(fc.path)
			}
			if files[ext] == nil {
				files[ext] = map[string]bool{}
			}
			files[ext][fc.path] = true
			added[ext] += fc.added
		}
	}
	exts := []Extension{}
	for ext, paths := range files {
		exts = append(exts, Extension{Ext: ext, Files: len(paths), Added: added[ext]})
	}
	slices.SortFunc(exts, func(a, b Extension) int {
		return cmp.Or(cmp.Compare(b.Added, a.Added), cmp.Compare(b.Files, a.Files), cmp.Compare(a.Ext, b.Ext))
	})
	return exts[:min(len(exts), maxRepoExtensions)]
}

// repoItems are the items with the most of the user's active days among evs.
func (f *Facts) repoItems(evs []event) []string {
	days := map[string]map[string]bool{}
	for _, e := range evs {
		if e.kind == evActive && e.item != "" {
			if days[e.item] == nil {
				days[e.item] = map[string]bool{}
			}
			days[e.item][day(e.at)] = true
		}
	}
	ids := sortedKeys(days)
	slices.SortStableFunc(ids, func(a, b string) int { return cmp.Compare(len(days[b]), len(days[a])) })
	return orEmpty(ids[:min(len(ids), maxRepoItems)])
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
