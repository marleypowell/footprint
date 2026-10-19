package facts

import (
	"cmp"
	"slices"
	"strings"

	"github.com/marleypowell/footprint/internal/collect"
)

// substantialWords is the length from which a page the user mostly wrote counts as a document of
// theirs (a design, a runbook, a write-up), rather than a stub or a list.
const substantialWords = 300

// Doc is one line of docs.jsonl: wiki content the user created or edited.
type Doc struct {
	Ref         string   `json:"ref"`  // "doc+<id>", as drafts cite it
	ID          string   `json:"id"`   // the wiki's
	Item        string   `json:"item"` // its work item, when substantial
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Type        string   `json:"type"`
	Live        bool     `json:"live"`
	Status      string   `json:"status"`
	Space       string   `json:"space"`
	Ancestors   []string `json:"ancestors"` // the page tree above it, root first
	Labels      []string `json:"labels"`
	CreatedAt   string   `json:"created_at"`
	CreatedByMe bool     `json:"created_by_me"`
	Versions    int      `json:"versions"`
	MyVersions  int      `json:"my_versions"`
	Share       int      `json:"share"` // the user's share of its versions, in whole percent
	Words       int      `json:"words"`
	Substantial bool     `json:"substantial"` // substantialWords or more, and mostly the user's: created by them or 3+ versions, and half the versions or more
	Keys        []string `json:"keys"`        // issues it names, by current key
	Items       []string `json:"items"`       // their work items
	First       string   `json:"first"`       // the first and last day the user saved a version
	Last        string   `json:"last"`

	rec *collect.PageRecord
	evs []event
}

func (f *Facts) buildDocs() {
	for i := range f.raw.Pages {
		p := &f.raw.Pages[i]
		d := &Doc{
			Ref: "doc+" + itemPath(p.ID), ID: p.ID, Title: p.Title, URL: p.URL, Type: p.Type, Live: deref(p.Subtype) == "live",
			Status: p.Status, Space: p.Space.Name, Ancestors: []string{}, Labels: orEmpty(p.Labels), CreatedAt: p.CreatedAt,
			CreatedByMe: slices.Contains(p.Roles, "creator"), Versions: len(p.Versions), MyVersions: p.UserVersions,
			Words: len(strings.Fields(deref(p.Body))), Keys: f.knownKeys(p.IssueKeys), Items: []string{}, rec: p,
		}
		for _, a := range p.Ancestors {
			d.Ancestors = append(d.Ancestors, a.Title)
		}
		if d.Versions > 0 {
			d.Share = d.MyVersions * 100 / d.Versions
		}
		d.Substantial = d.Words >= substantialWords && d.Share >= 50 && (d.CreatedByMe || d.MyVersions >= 3)
		if at, ok := parseTime(p.CreatedAt); ok && d.CreatedByMe {
			d.evs = append(d.evs, event{at: at, kind: evPageCreated, key: d.Ref})
		}
		var days []string
		for _, v := range p.Versions {
			if at, ok := parseTime(v.CreatedAt); ok && v.ByUser {
				d.evs = append(d.evs, event{at: at, kind: evPageVersion, key: d.Ref}, event{at: at, kind: evActive, key: d.Ref})
				days = append(days, day(at))
			}
		}
		if len(days) > 0 {
			slices.Sort(days)
			d.First, d.Last = days[0], days[len(days)-1]
		}
		f.docs[p.ID] = d
		f.Docs = append(f.Docs, d)
	}
	slices.SortFunc(f.Docs, func(a, b *Doc) int { return cmp.Or(cmp.Compare(a.CreatedAt, b.CreatedAt), cmp.Compare(a.ID, b.ID)) })
}

// linkDocs fills each doc's items, once the items exist.
func (f *Facts) linkDocs() {
	for _, d := range f.Docs {
		set := map[string]bool{}
		for _, k := range d.Keys {
			if id, _ := f.itemOf(k); f.items[id] != nil {
				set[id] = true
			}
		}
		d.Items = sortedSet(set)
	}
}
