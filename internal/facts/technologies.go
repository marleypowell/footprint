package facts

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/marleypowell/footprint/internal/tech"
)

// Technology is one line of technologies.jsonl: a language, tool or platform the user's landed
// changes touched, by the paths of the files they changed.
type Technology struct {
	Name        string       `json:"name"`
	Category    string       `json:"category"`    // language, tooling or platform
	Significant bool         `json:"significant"` // used enough to name in a CV, all time
	First       string       `json:"first"`       // the first and last of the user's active days on changes touching it
	Last        string       `json:"last"`
	Totals      TechTotals   `json:"totals"`
	ByYear      []TechPeriod `json:"by_year"`
	Repos       []string     `json:"repos"` // most of the user's days with it first
	Items       []string     `json:"items"`
}

// TechTotals count one technology's part of the user's landed changes, over a window.
type TechTotals struct {
	Changes      int `json:"changes"`       // landed in the window
	AgentChanges int `json:"agent_changes"` // those an AI agent wrote most of
	LinesAdded   int `json:"lines_added"`   // in its files, excluded files left out
	LinesDeleted int `json:"lines_deleted"`
	Files        int `json:"files"`       // distinct paths
	ActiveDays   int `json:"active_days"` // distinct days of the user's activity on those changes, in the window
	Repos        int `json:"repos"`
}

// TechPeriod is a year's TechTotals.
type TechPeriod struct {
	Period string     `json:"period"`
	Totals TechTotals `json:"totals"`
}

// TechSummary is a technology in facts.json: its window totals.
type TechSummary struct {
	Name        string     `json:"name"`
	Category    string     `json:"category"`
	Significant bool       `json:"significant"`
	First       string     `json:"first"`
	Last        string     `json:"last"`
	Totals      TechTotals `json:"totals"`
}

// A technology is significant with this many active days, and (a language) this many lines added or
// changes; a tool or platform needs the days alone.
const (
	techMinDays    = 3
	techMinLines   = 50
	techMinChanges = 3
	maxTechRepos   = 10
	maxTechItems   = 10
)

// techUse is one change's part in one technology.
type techUse struct {
	name           string
	added, deleted int
	paths          []string
}

// techDays are the days of the user's own activity on a change.
func techDays(c *Change) []string {
	set := map[string]bool{}
	for _, e := range c.evs {
		if e.kind == evActive {
			set[day(e.at)] = true
		}
	}
	return sortedKeys(set)
}

// buildTechnologies names each landed change's technologies, and totals each technology.
func (f *Facts) buildTechnologies() {
	byName := map[string]*Technology{}
	for _, c := range f.Changes {
		c.Technologies = []string{}
		if !c.Landed || c.Class == CoAuthor {
			continue
		}
		uses := map[string]*techUse{}
		for _, fc := range c.files {
			if fc.generated || fc.bulky {
				continue
			}
			for _, t := range tech.Classify(fc.path) {
				u := uses[t.Name]
				if u == nil {
					u = &techUse{name: t.Name}
					uses[t.Name] = u
					if byName[t.Name] == nil {
						byName[t.Name] = &Technology{Name: t.Name, Category: t.Category}
					}
				}
				u.added, u.deleted, u.paths = u.added+fc.added, u.deleted+fc.deleted, append(u.paths, fc.path)
			}
		}
		for _, name := range sortedKeys(uses) {
			c.techs = append(c.techs, *uses[name])
		}
		slices.SortStableFunc(c.techs, func(a, b techUse) int { return cmp.Compare(b.added+b.deleted, a.added+a.deleted) })
		for _, u := range c.techs {
			c.Technologies = append(c.Technologies, u.name)
		}
		c.techDays = techDays(c)
	}
	for _, name := range sortedKeys(byName) {
		t := byName[name]
		t.Totals = f.techTotals(name, All)
		days := f.techActiveDays(name, All)
		t.First, t.Last = days[0], days[len(days)-1]
		t.Significant = t.Totals.ActiveDays >= techMinDays &&
			(t.Category != tech.Language || t.Totals.LinesAdded >= techMinLines || t.Totals.Changes >= techMinChanges)
		t.ByYear = []TechPeriod{}
		first, _ := strconv.Atoi(t.First[:4])
		last, _ := strconv.Atoi(t.Last[:4])
		for y := first; y <= last; y++ {
			if tt := f.techTotals(name, yearWindow(y)); tt != (TechTotals{}) {
				t.ByYear = append(t.ByYear, TechPeriod{Period: itoa(y), Totals: tt})
			}
		}
		t.Repos, t.Items = f.techTop(name)
		f.Technologies = append(f.Technologies, t)
	}
	slices.SortStableFunc(f.Technologies, func(a, b *Technology) int {
		return cmp.Or(cmp.Compare(b.Totals.ActiveDays, a.Totals.ActiveDays), cmp.Compare(b.Totals.LinesAdded, a.Totals.LinesAdded),
			cmp.Compare(a.Name, b.Name))
	})
	f.technologies = map[string]*Technology{}
	for _, t := range f.Technologies {
		f.technologies[t.Name] = t
	}
	for _, it := range f.Items {
		changes := slices.Clone(it.Changes)
		for _, id := range it.Features {
			changes = append(changes, f.items[id].Changes...)
		}
		it.Technologies = f.techsOf(changes)
	}
}

func (c *Change) use(name string) *techUse {
	for i := range c.techs {
		if c.techs[i].name == name {
			return &c.techs[i]
		}
	}
	return nil
}

// techTotals counts a technology over w: changes and lines by when the change landed, days as they
// fell.
func (f *Facts) techTotals(name string, w Window) TechTotals {
	var t TechTotals
	paths, repos, days := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, c := range f.Changes {
		u := c.use(name)
		if u == nil {
			continue
		}
		for _, d := range c.techDays {
			if at, _ := time.Parse(dayLayout, d); w.Contains(at) {
				days[d] = true
			}
		}
		if !w.Contains(c.at) {
			continue
		}
		t.Changes++
		if c.Class == Agent {
			t.AgentChanges++
		}
		t.LinesAdded, t.LinesDeleted = t.LinesAdded+u.added, t.LinesDeleted+u.deleted
		for _, p := range u.paths {
			paths[c.Repo+"/"+p] = true
		}
		repos[c.Repo] = true
	}
	t.Files, t.Repos, t.ActiveDays = len(paths), len(repos), len(days)
	return t
}

func (f *Facts) techActiveDays(name string, w Window) []string {
	days := map[string]bool{}
	for _, c := range f.Changes {
		if c.use(name) == nil {
			continue
		}
		for _, d := range c.techDays {
			if at, _ := time.Parse(dayLayout, d); w.Contains(at) {
				days[d] = true
			}
		}
	}
	return sortedKeys(days)
}

// techTop are the repos and items with most of the user's days on a technology.
func (f *Facts) techTop(name string) (repos, items []string) {
	byRepo, byItem := map[string]map[string]bool{}, map[string]map[string]bool{}
	for _, c := range f.Changes {
		if c.use(name) == nil {
			continue
		}
		for _, d := range c.techDays {
			if byRepo[c.Repo] == nil {
				byRepo[c.Repo] = map[string]bool{}
			}
			byRepo[c.Repo][d] = true
			if byItem[c.Item] == nil {
				byItem[c.Item] = map[string]bool{}
			}
			byItem[c.Item][d] = true
		}
	}
	top := func(m map[string]map[string]bool, n int) []string {
		ids := sortedKeys(m)
		slices.SortStableFunc(ids, func(a, b string) int { return cmp.Compare(len(m[b]), len(m[a])) })
		return orEmpty(ids[:min(len(ids), n)])
	}
	return top(byRepo, maxTechRepos), top(byItem, maxTechItems)
}

// techSummaries are the technologies with the user's activity in w, as of w.
func (f *Facts) techSummaries(w Window) []TechSummary {
	out := []TechSummary{}
	for _, t := range f.Technologies {
		days := f.techActiveDays(t.Name, w)
		if len(days) == 0 {
			continue
		}
		out = append(out, TechSummary{Name: t.Name, Category: t.Category, Significant: t.Significant, First: days[0],
			Last: days[len(days)-1], Totals: f.techTotals(t.Name, w)})
	}
	slices.SortStableFunc(out, func(a, b TechSummary) int {
		return cmp.Or(cmp.Compare(b.Totals.ActiveDays, a.Totals.ActiveDays), cmp.Compare(b.Totals.LinesAdded, a.Totals.LinesAdded),
			cmp.Compare(a.Name, b.Name))
	})
	return out
}

// techsOf names the technologies of some changes, most lines first.
func (f *Facts) techsOf(changes []string) []string {
	lines := map[string]int{}
	for _, id := range changes {
		for _, u := range f.changes[id].techs {
			lines[u.name] += u.added + u.deleted
		}
	}
	names := sortedKeys(lines)
	slices.SortStableFunc(names, func(a, b string) int { return cmp.Compare(lines[b], lines[a]) })
	return names
}

// techValues are the numbers of each technology a paragraph names, over w: in scope for check.
func (f *Facts) techValues(text string, ws []Window) []int {
	var vals []int
	for _, t := range f.Technologies {
		if !namesTech(text, t.Name) {
			continue
		}
		for _, w := range ws {
			tt := f.techTotals(t.Name, w)
			vals = append(vals, tt.Changes, tt.AgentChanges, tt.LinesAdded, tt.LinesDeleted, tt.Files, tt.ActiveDays, tt.Repos)
		}
	}
	return vals
}

// namesTech says whether text names a technology, by any of its names, as a whole word.
func namesTech(text, name string) bool {
	for _, n := range tech.Names(name) {
		for i := 0; ; {
			j := strings.Index(text[i:], n)
			if j < 0 {
				break
			}
			start, end := i+j, i+j+len(n)
			if (start == 0 || !isNameChar(text[start-1])) && (end == len(text) || !isNameChar(text[end])) {
				return true
			}
			i = end
		}
	}
	return false
}

func isNameChar(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '#' || b == '+' || b == '_'
}
