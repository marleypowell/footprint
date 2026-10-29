package facts

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/marleypowell/footprint/internal/collect"
)

// The kinds of record a search finds, in the order a day's hits list them.
var searchKinds = []string{"item", "issue", "change", "doc", "review"}

// Query is what footprint search looks for: records whose text has every term, within a filter.
type Query struct {
	Terms  []string // each a word or phrase, matched case-insensitively anywhere in the record's text
	Kinds  []string // of searchKinds; none for all
	Filter Filter   // a window keeps records with the user's activity in it; a repo or item, records in it
}

// Hit is one record a search found.
type Hit struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`   // as a citation names it
	Item  string `json:"item"` // the work item whose note and pack tell its story: an item's own id; "" for none
	First string `json:"first"`
	Last  string `json:"last"` // the user's activity on it in the window; "" for none
	Title string `json:"title"`
	URL   string `json:"url"`
}

// String is the hit as footprint search lists it: kind, id, item, first..last, title and link,
// tab-separated.
func (h Hit) String() string {
	span := ""
	if h.First != "" {
		span = h.First + ".." + h.Last
	}
	return strings.Join([]string{h.Kind, h.ID, h.Item, span, oneLine(h.Title), h.URL}, "\t")
}

// searchable is a record as a search sees it.
type searchable struct {
	hit   Hit
	text  []string // ids, titles and bodies
	repos []string // where it is; none for a record outside the code host
	evs   []event
}

// Search finds the records matching q, by when the user's activity on them began (records with
// none last), then kind, then id. A record's text is its ids and title and: an item's note and the
// user's context for it; an issue's description; a change's description (a PR's, or a commit's
// message); a doc's labels and text; and the user's words on a reviewed PR.
func (f *Facts) Search(q Query) ([]Hit, error) {
	for _, k := range q.Kinds {
		if !slices.Contains(searchKinds, k) {
			return nil, fmt.Errorf("search: kind %q: want one of %s", k, strings.Join(searchKinds, ", "))
		}
	}
	if q.Filter.Repo != "" && f.project(q.Filter.Repo) == nil {
		return nil, fmt.Errorf("no repo %q in the dataset", q.Filter.Repo)
	}
	var in map[string]bool // the records of the item the filter names
	if q.Filter.Item != "" {
		it := f.itemFor(q.Filter.Item)
		if it == nil {
			return nil, fmt.Errorf("no work item %q: see items.jsonl", q.Filter.Item)
		}
		in = f.recordsOf(it)
	}
	terms := make([]string, 0, len(q.Terms))
	for _, t := range q.Terms {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			terms = append(terms, t)
		}
	}
	out := []Hit{}
	for _, r := range f.searchables() {
		if len(q.Kinds) > 0 && !slices.Contains(q.Kinds, r.hit.Kind) {
			continue
		}
		if in != nil && !in[r.hit.Kind+" "+r.hit.ID] {
			continue
		}
		if q.Filter.Repo != "" && !slices.Contains(r.repos, q.Filter.Repo) {
			continue
		}
		if r.hit.First, r.hit.Last = span(r.evs, q.Filter.Window); r.hit.First == "" && !q.Filter.Window.IsAll() {
			continue
		}
		if matchesAll(r.text, terms) {
			out = append(out, r.hit)
		}
	}
	none := func(h Hit) int { // records without activity go last
		if h.First == "" {
			return 1
		}
		return 0
	}
	slices.SortFunc(out, func(a, b Hit) int {
		return cmp.Or(cmp.Compare(none(a), none(b)), cmp.Compare(a.First, b.First),
			cmp.Compare(slices.Index(searchKinds, a.Kind), slices.Index(searchKinds, b.Kind)), collect.NaturalCompare(a.ID, b.ID))
	})
	return out, nil
}

// matchesAll says whether every term is in the text, ignoring case.
func matchesAll(text []string, terms []string) bool {
	all := strings.ToLower(strings.Join(text, "\n"))
	for _, t := range terms {
		if !strings.Contains(all, t) {
			return false
		}
	}
	return true
}

// recordsOf are the item's records, as "<kind> <id>": the item, the items under an initiative, and
// what each holds.
func (f *Facts) recordsOf(it *Item) map[string]bool {
	set := map[string]bool{}
	for _, it := range append([]*Item{it}, f.featuresOf(it)...) {
		set["item "+it.ID] = true
		for _, k := range it.Issues {
			set["issue "+k] = true
		}
		for _, id := range it.Changes {
			set["change "+id] = true
		}
		for _, ref := range it.Docs {
			set["doc "+ref] = true
		}
		if it.doc != nil {
			set["doc "+it.doc.Ref] = true
		}
		for _, ref := range it.Reviews {
			set["review "+ref] = true
		}
	}
	return set
}

func (f *Facts) featuresOf(it *Item) []*Item {
	var out []*Item
	for _, id := range it.Features {
		out = append(out, f.items[id])
	}
	return out
}

// searchables are every record a search can find.
func (f *Facts) searchables() []searchable {
	issueEvents := map[string][]event{}
	for _, e := range f.events {
		if f.issues[e.key] != nil {
			issueEvents[e.key] = append(issueEvents[e.key], e)
		}
	}
	var out []searchable
	for _, it := range f.Items {
		text := []string{it.ID, it.Title, f.context[itemPath(it.ID)]}
		if n := f.notes[itemPath(it.ID)]; n != nil {
			text = append(text, n.body)
		}
		out = append(out, searchable{hit: Hit{Kind: "item", ID: it.ID, Item: it.ID, Title: it.Title, URL: it.URL},
			text: text, repos: it.Repos, evs: f.rollup(it)})
	}
	for _, is := range f.Issues {
		repos := map[string]bool{}
		for _, id := range is.Changes {
			repos[f.changes[id].Repo] = true
		}
		item := is.Item
		if item == "" && f.items[is.Key] != nil { // an initiative above the user's features
			item = is.Key
		}
		text := append([]string{is.Key, is.Title, is.rec.Description}, is.PreviousKeys...)
		out = append(out, searchable{hit: Hit{Kind: "issue", ID: is.Key, Item: item, Title: is.Title, URL: is.URL},
			text: text, repos: sortedSet(repos), evs: issueEvents[is.Key]})
	}
	for _, c := range f.Changes {
		out = append(out, searchable{hit: Hit{Kind: "change", ID: c.ID, Item: c.Item, Title: c.Title, URL: c.URL},
			text: []string{c.ID, c.Title, c.body}, repos: []string{c.Repo}, evs: c.evs})
	}
	for _, d := range f.Docs {
		text := append([]string{d.Ref, d.Title}, d.Labels...)
		var repos []string
		switch {
		case d.rec != nil:
			text = append(text, deref(d.rec.Body))
		case d.adr != nil:
			text, repos = append(text, d.adr.Body), []string{d.Repo}
		}
		out = append(out, searchable{hit: Hit{Kind: "doc", ID: d.Ref, Item: d.Item, Title: d.Title, URL: d.URL},
			text: text, repos: repos, evs: d.evs})
	}
	for _, r := range f.Reviews {
		text := []string{r.Ref, r.Title}
		for _, t := range r.texts {
			text = append(text, t.Text)
		}
		out = append(out, searchable{hit: Hit{Kind: "review", ID: r.Ref, Item: r.Item, Title: r.Title, URL: r.URL},
			text: text, repos: []string{r.Repo}, evs: r.evs})
	}
	return out
}
