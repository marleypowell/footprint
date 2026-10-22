package facts

import (
	"cmp"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/marleypowell/footprint/internal/collect"
)

// Item kinds.
const (
	KindFeature    = "feature"    // a story, task or bug (an issue that is not a sub-task), with its sub-tasks
	KindInitiative = "initiative" // an epic or above, with the features under it
	KindBucket     = "bucket"     // a repo's changes that name no issue, per quarter
	KindDoc        = "doc"        // a substantial wiki page or decision record (ADR) the user wrote
)

// effortTiers size an item by the distinct days of the user's activity on it.
var effortTiers = []struct {
	name    string
	minDays int
}{{"XL", 31}, {"L", 11}, {"M", 4}, {"S", 2}, {"XS", 1}}

func effortOf(days int) string {
	for _, t := range effortTiers {
		if days >= t.minDays {
			return t.name
		}
	}
	return "XS"
}

func effortRank(effort string) int {
	for i, t := range effortTiers {
		if t.name == effort {
			return i
		}
	}
	return len(effortTiers)
}

// Item is one line of items.jsonl: a unit of the user's work, which an agent writes one note about.
type Item struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Type        string    `json:"type"`        // the issue's type; a doc's wiki type, or "adr"
	Status      string    `json:"status"`      // the issue's status category
	Involvement string    `json:"involvement"` // in the item's own issue; "contributed" when only through its sub-tasks or changes
	Initiative  *Ref      `json:"initiative"`  // a feature's epic; an initiative's own parent
	Repos       []string  `json:"repos"`
	First       string    `json:"first"` // the first and last day of the user's activity on it
	Last        string    `json:"last"`
	ActiveDays  int       `json:"active_days"`
	Effort      string    `json:"effort"`
	Totals      Totals    `json:"totals"` // an initiative's include its features'
	Subtasks    *Subtasks `json:"subtasks"`
	Issues      []string  `json:"issues"`       // its own issue first, then the others in it
	Features    []string  `json:"features"`     // an initiative's
	Changes     []string  `json:"changes"`      // the changes counted toward it
	AlsoChanges []string  `json:"also_changes"` // changes naming it that count toward another item; an ADR's, the changes that wrote it
	Docs        []string  `json:"docs"`         // docs naming it, and ADRs written in its changes
	Pack        string    `json:"pack"`         // the input hash of packs/<id>.md
	Note        string    `json:"note"`         // current, stale, missing, invalid, or optional (not required, none yet)

	own    *Issue
	pack   *packInput
	repo   string // a bucket's
	doc    *Doc
	events []event // counted toward it directly
	days   []string
	first  time.Time
}

// Subtasks are a feature's sub-tasks: how many it has, and how many the user finished.
type Subtasks struct {
	Total    int `json:"total"`
	DoneByMe int `json:"done_by_me"`
}

// itemPath encodes an id for a file name: letters, digits, '.', '_', '-' and '+' stay, anything
// else is percent-encoded.
func itemPath(id string) string {
	var b strings.Builder
	for _, r := range []byte(id) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-', r == '+':
			b.WriteByte(r)
		default:
			b.WriteString(url.PathEscape(string(r)))
		}
	}
	return b.String()
}

func bucketID(repo string, at time.Time) string {
	return "repo+" + refPart(repo) + "+" + quarter(at)
}

// refPart encodes one part of an id whose parts '+' separates.
func refPart(s string) string { return strings.ReplaceAll(itemPath(s), "+", "%2B") }

func (f *Facts) item(id, kind string) *Item {
	if id == "" {
		panic("facts: an item without an id")
	}
	if it := f.items[id]; it != nil {
		return it
	}
	it := &Item{ID: id, Kind: kind, Repos: []string{}, Issues: []string{}, Features: []string{}, Changes: []string{},
		AlsoChanges: []string{}, Docs: []string{}}
	if is := f.issues[id]; is != nil && (kind == KindFeature || kind == KindInitiative) {
		it.own, it.Title, it.URL, it.Type, it.Status = is, is.Title, is.URL, is.Type, is.StatusCategory
	}
	f.items[id] = it
	return it
}

// buildItems decides each issue's involvement, puts every change, issue and doc in a work item,
// and counts each item's events.
func (f *Facts) buildItems() {
	for _, c := range f.Changes {
		for _, k := range c.Keys {
			f.issues[k].Changes = append(f.issues[k].Changes, c.ID)
		}
	}
	for _, is := range f.Issues {
		is.Involvement = is.involvement()
		if !is.ancestor && makesItem(is.Involvement) {
			f.item(f.itemOf(is.Key))
		}
	}
	for _, c := range f.Changes {
		if len(c.Keys) == 0 {
			it := f.item(bucketID(c.Repo, c.start), KindBucket) // by when the work began
			it.repo, c.Item = c.Repo, it.ID
			continue
		}
		c.Item = f.item(f.itemOf(f.bestKey(c.Keys))).ID
		also := map[string]bool{}
		for _, k := range c.Keys {
			if id, _ := f.itemOf(k); id != c.Item {
				also[id] = true
			}
		}
		c.AlsoItems = sortedSet(also)
	}
	// Every issue whose item exists is in it, however little it involves the user.
	for _, is := range f.Issues {
		if id, _ := f.itemOf(is.Key); f.items[id] != nil {
			is.Item = id
		}
	}
	// Initiatives above the features, and above other initiatives.
	for queue := sortedItemIDs(f.items); len(queue) > 0; queue = queue[1:] {
		it := f.items[queue[0]]
		if it.own == nil {
			continue
		}
		if up := f.initiativeOf(it.own.Key); up != "" {
			if f.items[up] == nil {
				queue = append(queue, up)
			}
			parent := f.item(up, KindInitiative)
			it.Initiative = &Ref{ID: parent.ID, Title: parent.Title}
			if it.Kind == KindFeature {
				parent.Features = append(parent.Features, it.ID)
			}
		}
	}
	for _, d := range f.Docs {
		if d.Substantial {
			it := f.item(d.Ref, KindDoc)
			it.doc, it.Title, it.URL, it.Type, d.Item = d, d.Title, d.URL, d.Type, it.ID
		}
	}
	f.linkDocs()

	// Events, each counted toward one item.
	for _, c := range f.Changes {
		for _, e := range c.evs {
			e.item, e.repo = c.Item, c.Repo
			f.emit(e)
		}
		for _, k := range c.Keys {
			item := f.issues[k].Item
			if item == "" {
				item = c.Item
			}
			f.emit(event{at: c.at, kind: evIssueWorked, key: k, item: item, repo: c.Repo})
		}
	}
	for _, is := range f.Issues {
		for _, e := range is.issueEvents() {
			e.item = is.Item
			f.emit(e)
		}
	}
	for _, d := range f.Docs {
		for _, e := range d.evs {
			e.item = d.Item
			f.emit(e)
		}
	}
	slices.SortStableFunc(f.events, func(a, b event) int {
		return cmp.Or(a.at.Compare(b.at), cmp.Compare(a.kind, b.kind), cmp.Compare(a.key, b.key), cmp.Compare(a.item, b.item))
	})
	for _, e := range f.events {
		if it := f.items[e.item]; it != nil {
			it.events = append(it.events, e)
		}
	}

	for _, is := range f.Issues {
		if it := f.items[is.Item]; it != nil && is != it.own {
			it.Issues = append(it.Issues, is.Key)
		}
	}
	for _, c := range f.Changes {
		f.items[c.Item].Changes = append(f.items[c.Item].Changes, c.ID)
		for _, id := range c.AlsoItems {
			if it := f.items[id]; it != nil {
				it.AlsoChanges = append(it.AlsoChanges, c.ID)
			}
		}
	}
	for _, d := range f.Docs {
		if it := f.items[d.Item]; it != nil {
			it.AlsoChanges = append(it.AlsoChanges, d.Changes...)
		}
		for _, id := range d.Items {
			if it := f.items[id]; it != nil {
				it.Docs = append(it.Docs, d.Ref)
			}
		}
	}
	for _, id := range sortedItemIDs(f.items) {
		f.finishItem(f.items[id])
	}
	for _, id := range sortedItemIDs(f.items) {
		f.Items = append(f.Items, f.items[id])
	}
	slices.SortFunc(f.Items, func(a, b *Item) int { return cmp.Or(cmp.Compare(a.First, b.First), collect.NaturalCompare(a.ID, b.ID)) })
}

// rollup is every event counted toward the item: an initiative's include its features'.
func (f *Facts) rollup(it *Item) []event {
	if it.Kind != KindInitiative {
		return it.events
	}
	evs := slices.Clone(it.events)
	for _, id := range it.Features {
		evs = append(evs, f.items[id].events...)
	}
	return evs
}

func (f *Facts) finishItem(it *Item) {
	evs := f.rollup(it)
	it.Totals = totalsOf(evs, All)
	it.days = activeDays(evs, All)
	it.ActiveDays = len(it.days)
	it.First, it.Last = span(evs, All)
	it.first, _ = parseTime(it.First)
	it.Effort = effortOf(it.ActiveDays)
	repos := map[string]bool{}
	for _, e := range evs {
		if e.repo != "" {
			repos[e.repo] = true
		}
	}
	it.Repos = sortedSet(repos)
	slices.SortFunc(it.Issues, collect.NaturalCompare)
	if it.own != nil {
		it.Issues = append([]string{it.own.Key}, it.Issues...)
		it.Involvement = it.own.Involvement
		if it.Involvement == Ancestor || !makesItem(it.Involvement) {
			it.Involvement = "contributed"
		}
	}
	if it.Kind == KindFeature && it.own != nil {
		st := &Subtasks{Total: len(it.own.rec.Subtasks)}
		for _, k := range it.Issues[1:] {
			if f.issues[k].DoneByMe {
				st.DoneByMe++
			}
		}
		if st.Total > 0 || st.DoneByMe > 0 {
			it.Subtasks = st
		}
	}
	if it.Kind == KindBucket {
		it.Title = it.repo + ": work naming no issue, " + strings.ToUpper(it.ID[strings.LastIndex(it.ID, "+")+1:])
		if p := f.project(it.repo); p != nil {
			it.URL = p.URL
		}
	}
	slices.SortFunc(it.Features, collect.NaturalCompare)
}

func (f *Facts) project(name string) *collect.Project {
	for i := range f.raw.Projects {
		if f.raw.Projects[i].Name == name {
			return &f.raw.Projects[i]
		}
	}
	return nil
}

func sortedItemIDs(items map[string]*Item) []string {
	ids := make([]string, 0, len(items))
	for id := range items {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, collect.NaturalCompare)
	return ids
}
