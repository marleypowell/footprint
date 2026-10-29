package facts

import (
	"cmp"
	"math"
	"slices"
	"strings"

	"github.com/marleypowell/footprint/internal/catalog"
	"github.com/marleypowell/footprint/internal/collect"
)

// Area roles: the user's share of an area's text lines.
const (
	RoleTop         = "top"         // half or more
	RoleMajor       = "major"       // a fifth or more
	RoleContributor = "contributor" // less, or an area too small to say
)

const (
	topShare, majorShare = 50, 20
	minAreaLines         = 200 // below it, no role: a few lines are too small to own
	maxAreaOthers        = 3
)

// Area is one line of ownership.jsonl: a repo, one of its catalog entities, or a directory, in
// which the user has lines the default branch still has. Everything is as of the repos' heads.
type Area struct {
	ID           string       `json:"id"`   // own+<repo> or own+<repo>+<path>, as drafts cite it
	Kind         string       `json:"kind"` // repo, entity or dir
	Repo         string       `json:"repo"`
	Path         string       `json:"path"`   // "" for a repo; a directory ends in "/"
	Entity       *Ref         `json:"entity"` // an entity area's catalog entity: {id: ref, title}
	Archived     bool         `json:"archived"`
	AsOf         string       `json:"as_of"` // the day of the head blamed
	Lines        int          `json:"lines"` // text lines in the area
	Living       int          `json:"living"`
	SharePercent int          `json:"share_percent"`
	Role         string       `json:"role"`
	Others       []AreaPerson `json:"others"`  // who else wrote lines of the files the user changed, most first
	Changes      int          `json:"changes"` // the user's changes with lines in it
	URL          string       `json:"url"`
}

// AreaPerson is a colleague's lines in an area.
type AreaPerson struct {
	Name  string `json:"name"`
	Lines int    `json:"lines"`
	Last  string `json:"last"` // the newest of their lines' dates
}

// RepoOwnership is a repos.jsonl record's blame of the repo.
type RepoOwnership struct {
	AsOf            string `json:"as_of"`
	Lines           int    `json:"lines"`
	Living          int    `json:"living"`
	SharePercent    int    `json:"share_percent"`
	SurvivalPercent int    `json:"survival_percent"` // living over the lines the user's landed changes added there
}

// OwnershipSummary is facts.json's ownership: the blame of the repos the user committed to.
type OwnershipSummary struct {
	AsOfFirst          string `json:"as_of_first"` // the first and last head blamed
	AsOfLast           string `json:"as_of_last"`
	Repos              int    `json:"repos"`
	Files              int    `json:"files"`
	LinesLiving        int    `json:"lines_living"` // of the window's landed changes (all time: every one's)
	LinesAdded         int    `json:"lines_added"`  // ... what those changes added, in the repos blamed
	SurvivalPercent    int    `json:"survival_percent"`
	CoAuthoredLiving   int    `json:"co_authored_living"`   // lines of commits that only name the user as co-author
	UnattributedLines  int    `json:"unattributed_lines"`   // of commits collection took for the user's, that no change of theirs explains
	UnmatchedSelfLines int    `json:"unmatched_self_lines"` // lines blamed to an identity the config matches as the user: it was missed
	AreasTop           int    `json:"areas_top"`
	AreasMajor         int    `json:"areas_major"`
}

// ownAgg is what buildOwnership learns across repos, for the summary.
type ownAgg struct {
	heads                         []string
	files                         int
	coAuthored, unattributed, own int
	living                        map[string]int // change id -> lines living
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func pct(part, whole int) int {
	if whole <= 0 {
		return 0
	}
	return int(math.Round(100 * float64(part) / float64(whole)))
}

// buildOwnership credits the lines blame names to the user's changes, and makes the areas. It runs
// before the events are emitted, so that each landed change carries its living lines.
func (f *Facts) buildOwnership() {
	f.own = &ownAgg{living: map[string]int{}}
	f.ownRepos = map[string]*RepoOwnership{}
	for i := range f.raw.Ownership {
		o := &f.raw.Ownership[i]
		f.ownRepo(o)
	}
	for _, c := range f.Changes {
		if _, blamed := f.ownRepos[c.Repo]; !blamed || c.Class == CoAuthor {
			continue
		}
		n := f.own.living[c.ID]
		c.Lines.Living = &n
		for i := range c.evs {
			if c.evs[i].kind == evLanded {
				c.evs[i].living = n
			}
		}
	}
	added := map[string]int{} // what the user's landed changes added, by repo
	for _, c := range f.Changes {
		if c.Class != CoAuthor && c.Landed {
			added[c.Repo] += c.Lines.Added
		}
	}
	for name, r := range f.ownRepos {
		r.SurvivalPercent = pct(r.Living, added[name])
	}
	slices.SortFunc(f.Areas, func(a, b *Area) int { // by repo, the repo's own area first, then by path
		return cmp.Or(cmp.Compare(a.Repo, b.Repo), cmp.Compare(b2i(a.Kind != "repo"), b2i(b.Kind != "repo")), cmp.Compare(a.Path, b.Path))
	})
	f.ownBase = f.ownershipBase()
}

// ownRepo reads one repo's blame.
func (f *Facts) ownRepo(o *collect.Ownership) {
	type fileOwn struct {
		file    *collect.OwnedFile
		living  int
		changes map[string]bool
	}
	var files []fileOwn
	repoLiving := 0
	for i := range o.Files {
		file := &o.Files[i]
		fo := fileOwn{file: file, changes: map[string]bool{}}
		for _, c := range file.Commits {
			id := f.changeOf[o.Repo+"@"+c.SHA]
			ch := f.changes[id]
			switch {
			case ch == nil:
				f.own.unattributed += c.Lines
			case ch.Class == CoAuthor:
				f.own.coAuthored += c.Lines
			default:
				f.own.living[id] += c.Lines
				fo.living += c.Lines
				fo.changes[id] = true
			}
		}
		for _, a := range file.Others {
			if f.people.class(a.Name, a.Email) == Self {
				f.own.own += a.Lines
			}
		}
		repoLiving += fo.living
		files = append(files, fo)
	}
	f.own.files += len(o.Files)
	asOf := dayOf(o.HeadAt)
	f.own.heads = append(f.own.heads, asOf)
	f.ownRepos[o.Repo] = &RepoOwnership{AsOf: asOf, Lines: o.Lines, Living: repoLiving, SharePercent: pct(repoLiving, o.Lines)}

	proj := f.project(o.Repo)
	archived, url := false, ""
	entities := map[string]*catalog.Entity{} // by directory, with a trailing slash
	if proj != nil {
		archived, url = proj.CodeHost.IsArchived, proj.URL
		for i := range proj.Catalog.Entities {
			e := &proj.Catalog.Entities[i]
			dir := e.Path[:max(strings.LastIndex(e.Path, "/"), 0)]
			if dir != "" && catalog.DescribesSoftware(e.Category) && entities[dir+"/"] == nil {
				entities[dir+"/"] = e
			}
		}
	}
	people := func(fs []fileOwn) []AreaPerson {
		type acc struct {
			name  string
			best  int
			lines int
			last  string
		}
		byKey := map[string]*acc{}
		for _, fo := range fs {
			for _, a := range fo.file.Others {
				if class := f.people.class(a.Name, a.Email); class != Colleague {
					continue
				}
				key := strings.ToLower(a.Email)
				if key == "" {
					key = strings.ToLower(a.Name)
				}
				p := byKey[key]
				if p == nil {
					p = &acc{}
					byKey[key] = p
				}
				p.lines += a.Lines
				if a.Lines > p.best {
					p.name, p.best = a.Name, a.Lines
				}
				p.last = max(p.last, dayOf(a.LastAt))
			}
		}
		out := make([]AreaPerson, 0, len(byKey))
		for _, p := range byKey {
			out = append(out, AreaPerson{Name: p.name, Lines: p.lines, Last: p.last})
		}
		slices.SortFunc(out, func(a, b AreaPerson) int { return cmp.Or(cmp.Compare(b.Lines, a.Lines), cmp.Compare(a.Name, b.Name)) })
		return out[:min(len(out), maxAreaOthers)]
	}
	add := func(kind, path string, lines int, in func(string) bool, entity *catalog.Entity) {
		var fs []fileOwn
		living := 0
		changes := map[string]bool{}
		for _, fo := range files {
			if in(fo.file.Path) {
				fs = append(fs, fo)
				living += fo.living
				for id := range fo.changes {
					changes[id] = true
				}
			}
		}
		if living == 0 {
			return
		}
		a := &Area{ID: "own+" + o.Repo, Kind: kind, Repo: o.Repo, Path: path, Archived: archived, AsOf: asOf, Lines: lines, Living: living,
			SharePercent: pct(living, lines), Role: RoleContributor, Others: people(fs), Changes: len(changes), URL: url}
		if path != "" {
			a.ID += "+" + path
		}
		if entity != nil {
			title := deref(entity.Title)
			if title == "" {
				title = entity.Ref
			}
			a.Entity = &Ref{ID: entity.Ref, Title: title}
		}
		if lines >= minAreaLines {
			switch {
			case a.SharePercent >= topShare:
				a.Role = RoleTop
			case a.SharePercent >= majorShare:
				a.Role = RoleMajor
			}
		}
		f.Areas = append(f.Areas, a)
	}
	add("repo", "", o.Lines, func(string) bool { return true }, nil)
	for _, d := range o.Dirs {
		d := d
		in := func(p string) bool { return strings.HasPrefix(p, d.Path) }
		if d.Path == "(root)" {
			in = func(p string) bool { return !strings.Contains(p, "/") }
		}
		kind, entity := "dir", entities[d.Path]
		if entity != nil {
			kind = "entity"
		}
		add(kind, d.Path, d.Lines, in, entity)
	}
}

// ownershipSummary is the window's: the blame as of the repos' heads, and the lines living of the
// changes that landed in the window.
func (f *Facts) ownershipSummary(w Window) *OwnershipSummary {
	if f.ownBase == nil {
		return nil
	}
	s := *f.ownBase
	s.LinesAdded, s.LinesLiving = 0, 0
	for _, c := range f.Changes {
		if _, blamed := f.ownRepos[c.Repo]; !blamed || c.Class == CoAuthor || !c.Landed || !w.Contains(c.at) {
			continue
		}
		s.LinesAdded += c.Lines.Added
		s.LinesLiving += deref(c.Lines.Living)
	}
	s.SurvivalPercent = pct(s.LinesLiving, s.LinesAdded)
	return &s
}

// ownershipBase is the summary's part that does not depend on a window: the blame as of the heads.
func (f *Facts) ownershipBase() *OwnershipSummary {
	if len(f.raw.Ownership) == 0 {
		return nil
	}
	s := &OwnershipSummary{Repos: len(f.raw.Ownership), Files: f.own.files, CoAuthoredLiving: f.own.coAuthored,
		UnattributedLines: f.own.unattributed, UnmatchedSelfLines: f.own.own}
	for _, h := range f.own.heads {
		if s.AsOfFirst == "" || h < s.AsOfFirst {
			s.AsOfFirst = h
		}
		s.AsOfLast = max(s.AsOfLast, h)
	}
	for _, a := range f.Areas {
		if a.Kind == "repo" {
			continue
		}
		switch a.Role {
		case RoleTop:
			s.AreasTop++
		case RoleMajor:
			s.AreasMajor++
		}
	}
	return s
}

// ownershipValues are the numbers of an area a paragraph cites, in scope for check.
func (f *Facts) areaValues(id string) []int {
	for _, a := range f.Areas {
		if a.ID == id {
			vals := []int{a.Lines, a.Living, a.SharePercent, a.Changes}
			for _, p := range a.Others {
				vals = append(vals, p.Lines)
			}
			return vals
		}
	}
	return nil
}

func (f *Facts) areaByRef(ref string) *Area {
	for _, a := range f.Areas {
		if a.ID == ref {
			return a
		}
	}
	return nil
}
