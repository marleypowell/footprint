package facts

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/marleypowell/footprint/internal/collect"
)

// Count groupings.
const (
	ByYear    = "year"
	ByQuarter = "quarter"
	ByRepo    = "repo"
	ByItem    = "item"
)

// Filter picks the events a count or search includes.
type Filter struct {
	Window Window
	Repo   string // in this repo; "" for any
	Item   string // counted toward this item, an initiative's including its features'; "" for any
}

// Count is what footprint count writes: the totals of what a filter picks (the user's Totals, or a
// technology's TechTotals), and, when asked, the same broken down by year, quarter, repo or item.
type Count[T Totals | TechTotals] struct {
	Window string     `json:"window"`
	Tech   string     `json:"tech"` // the technology counted; "" for the user's totals
	Repo   string     `json:"repo"`
	Item   string     `json:"item"`
	Totals T          `json:"totals"`
	By     string     `json:"by"`     // "" for no breakdown
	Groups []Group[T] `json:"groups"` // [] without a breakdown
}

// Group is the totals of one year, quarter, repo or item.
type Group[T Totals | TechTotals] struct {
	Name   string `json:"name"`
	Totals T      `json:"totals"`
}

// Count counts the events fl picks, by the rules that count the totals everywhere else, and breaks
// them down by by (one of the By* groupings, or "").
func (f *Facts) Count(fl Filter, by string) (Count[Totals], error) {
	it, err := f.checkFilter(fl, by)
	if err != nil {
		return Count[Totals]{}, err
	}
	evs := f.events
	if it != nil {
		evs = f.rollup(it)
	}
	if fl.Repo != "" {
		var in []event
		for _, e := range evs {
			if e.repo == fl.Repo {
				in = append(in, e)
			}
		}
		evs = in
	}
	c := newCount[Totals](fl, it, by, totalsOf(evs, fl.Window))
	switch by {
	case ByYear, ByQuarter:
		years, quarters := periods(evs, fl.Window)
		if by == ByQuarter {
			years = quarters
		}
		for _, p := range years {
			c.Groups = append(c.Groups, Group[Totals]{Name: p.Period, Totals: p.Totals})
		}
	case ByRepo, ByItem:
		by := map[string][]event{}
		for _, e := range evs {
			n := e.item
			if c.By == ByRepo {
				n = e.repo
			}
			if n != "" && fl.Window.Contains(e.at) { // events with no repo or item are in no group
				by[n] = append(by[n], e)
			}
		}
		for n, evs := range by {
			c.Groups = append(c.Groups, Group[Totals]{Name: n, Totals: totalsOf(evs, fl.Window)})
		}
		sortGroups(c.Groups, func(t Totals) (int, int) { return t.ActiveDays, t.ChangesLanded })
	}
	return c, nil
}

// CountTech counts a technology (by any of its names, ignoring case) in the changes fl picks, by the
// rules technologies.jsonl counts it by, and breaks the totals down by by.
func (f *Facts) CountTech(name string, fl Filter, by string) (Count[TechTotals], error) {
	t := f.techNamed(name)
	if t == nil {
		return Count[TechTotals]{}, fmt.Errorf("no technology %q in the user's landed changes: see technologies.jsonl", name)
	}
	it, err := f.checkFilter(fl, by)
	if err != nil {
		return Count[TechTotals]{}, err
	}
	var items map[string]bool
	if it != nil {
		items = map[string]bool{it.ID: true}
		for _, id := range it.Features {
			items[id] = true
		}
	}
	var changes []*Change
	for _, c := range f.Changes {
		if c.use(t.Name) != nil && (fl.Repo == "" || c.Repo == fl.Repo) && (items == nil || items[c.Item]) {
			changes = append(changes, c)
		}
	}
	c := newCount[TechTotals](fl, it, by, techTotalsOf(changes, t.Name, fl.Window))
	c.Tech = t.Name
	add := func(name string, changes []*Change, w Window) {
		if tt := techTotalsOf(changes, t.Name, w); tt != (TechTotals{}) {
			c.Groups = append(c.Groups, Group[TechTotals]{Name: name, Totals: tt})
		}
	}
	switch by {
	case ByYear, ByQuarter:
		var first, last time.Time
		for _, ch := range changes {
			for _, at := range append(dayTimes(ch.techDays), ch.at) {
				if first.IsZero() || at.Before(first) {
					first = at
				}
				last = maxTime(last, at)
			}
		}
		for y := first.Year(); !first.IsZero() && y <= last.Year(); y++ {
			if by == ByYear {
				add(itoa(y), changes, intersect(yearWindow(y), fl.Window))
				continue
			}
			for q := 1; q <= 4; q++ {
				add(itoa(y)+"q"+itoa(q), changes, intersect(quarterWindow(y, q), fl.Window))
			}
		}
	case ByRepo, ByItem:
		by := map[string][]*Change{}
		for _, ch := range changes {
			n := ch.Item
			if c.By == ByRepo {
				n = ch.Repo
			}
			by[n] = append(by[n], ch)
		}
		for _, n := range sortedKeys(by) {
			add(n, by[n], fl.Window)
		}
		sortGroups(c.Groups, func(t TechTotals) (int, int) { return t.ActiveDays, t.Changes })
	}
	return c, nil
}

// checkFilter checks that fl's repo and item, and by, are ones the facts have, and returns the item.
func (f *Facts) checkFilter(fl Filter, by string) (*Item, error) {
	if !slices.Contains([]string{"", ByYear, ByQuarter, ByRepo, ByItem}, by) {
		return nil, fmt.Errorf("count: by %q: want %s, %s, %s or %s", by, ByYear, ByQuarter, ByRepo, ByItem)
	}
	if fl.Repo != "" && f.project(fl.Repo) == nil {
		return nil, fmt.Errorf("no repo %q in the dataset", fl.Repo)
	}
	if fl.Item == "" {
		return nil, nil
	}
	it := f.itemFor(fl.Item)
	if it == nil {
		return nil, fmt.Errorf("no work item %q: see items.jsonl", fl.Item)
	}
	return it, nil
}

func newCount[T Totals | TechTotals](fl Filter, it *Item, by string, totals T) Count[T] {
	c := Count[T]{Window: fl.Window.String(), Repo: fl.Repo, Totals: totals, By: by, Groups: []Group[T]{}}
	if it != nil {
		c.Item = it.ID
	}
	return c
}

// itemFor finds an item by its id, or by any key its issue has had; nil for none.
func (f *Facts) itemFor(id string) *Item {
	if it := f.items[id]; it != nil {
		return it
	}
	return f.items[f.currentKey(id)]
}

// sortGroups puts the most active days first, then the most changes landed, then by name.
func sortGroups[T Totals | TechTotals](gs []Group[T], rank func(T) (days, landed int)) {
	slices.SortFunc(gs, func(a, b Group[T]) int {
		ad, al := rank(a.Totals)
		bd, bl := rank(b.Totals)
		return cmp.Or(cmp.Compare(bd, ad), cmp.Compare(bl, al), collect.NaturalCompare(a.Name, b.Name))
	})
}

func dayTimes(days []string) []time.Time {
	out := make([]time.Time, 0, len(days))
	for _, d := range days {
		if at, ok := parseTime(d); ok {
			out = append(out, at)
		}
	}
	return out
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
