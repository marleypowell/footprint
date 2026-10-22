package facts

import (
	"cmp"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/marleypowell/footprint/internal/collect"
)

// substantialWords is the length from which a page the user mostly wrote counts as a document of
// theirs (a design, a runbook, a write-up), rather than a stub or a list.
const substantialWords = 300

// decisionWords is that length for a decision record, which is short by design: what made it a
// document is the decision, not the length.
const decisionWords = 200

// Doc is one line of docs.jsonl: wiki content the user created or edited, or a decision record (an
// ADR) in a repo that the user committed to.
type Doc struct {
	Ref         string   `json:"ref"`  // "doc+<id>" for a page, "adr+<repo>+<file name>" for an ADR, as drafts cite it
	ID          string   `json:"id"`   // the wiki's; an ADR's repo and path
	Item        string   `json:"item"` // its work item, when substantial
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Type        string   `json:"type"` // the wiki's, or "adr"
	Live        bool     `json:"live"`
	Status      string   `json:"status"`
	Space       string   `json:"space"`
	Repo        string   `json:"repo"` // an ADR's
	Path        string   `json:"path"`
	Ancestors   []string `json:"ancestors"` // the page tree above it, root first
	Labels      []string `json:"labels"`
	CreatedAt   string   `json:"created_at"`
	CreatedByMe bool     `json:"created_by_me"`
	Versions    int      `json:"versions"` // an ADR's are the commits that changed it
	MyVersions  int      `json:"my_versions"`
	Share       int      `json:"share"` // the user's share of its versions, in whole percent
	Words       int      `json:"words"`
	Substantial bool     `json:"substantial"` // long enough, and mostly the user's: created by them or 3+ versions, and half the versions or more
	Keys        []string `json:"keys"`        // issues it names, by current key
	Items       []string `json:"items"`       // their work items, and an ADR's changes'
	Changes     []string `json:"changes"`     // an ADR's: the user's changes that wrote it
	First       string   `json:"first"`       // the first and last day the user saved a version
	Last        string   `json:"last"`

	rec  *collect.PageRecord
	adr  *collect.Decision
	evs  []event
	days []string
}

func (f *Facts) buildDocs() {
	for i := range f.raw.Pages {
		p := &f.raw.Pages[i]
		d := &Doc{
			Ref: "doc+" + itemPath(p.ID), ID: p.ID, Title: p.Title, URL: p.URL, Type: p.Type, Live: deref(p.Subtype) == "live",
			Status: p.Status, Space: p.Space.Name, Ancestors: []string{}, Labels: orEmpty(p.Labels), CreatedAt: p.CreatedAt,
			CreatedByMe: slices.Contains(p.Roles, "creator"), Versions: len(p.Versions), MyVersions: p.UserVersions,
			Words: len(strings.Fields(deref(p.Body))), Keys: f.knownKeys(p.IssueKeys), Items: []string{}, Changes: []string{}, rec: p,
		}
		for _, a := range p.Ancestors {
			d.Ancestors = append(d.Ancestors, a.Title)
		}
		if at, ok := parseTime(p.CreatedAt); ok && d.CreatedByMe {
			d.evs = append(d.evs, event{at: at, kind: evPageCreated, key: d.Ref})
		}
		for _, v := range p.Versions {
			if at, ok := parseTime(v.CreatedAt); ok && v.ByUser {
				d.version(at, evPageVersion)
			}
		}
		f.addDoc(d, substantialWords)
	}
	refs := f.decisionRefs()
	for i := range f.raw.Decisions {
		r := &f.raw.Decisions[i]
		d := &Doc{
			Ref: refs[i], ID: r.Repo + "/" + r.Path, Title: r.Title, URL: r.URL, Type: "adr", Status: "current", Repo: r.Repo,
			Path: r.Path, Ancestors: []string{}, Labels: []string{}, Versions: len(r.Versions), Words: len(strings.Fields(r.Body)),
			Keys: f.knownKeys(r.IssueKeys), Items: []string{}, Changes: []string{}, adr: r,
		}
		changes := map[string]bool{}
		for j, v := range r.Versions {
			at, ok := parseTime(v.Author.Date)
			if j == 0 && ok {
				d.CreatedAt = at.UTC().Format(time.RFC3339)
			}
			if !ok || !f.decisionVersionMine(v) {
				continue
			}
			d.MyVersions++
			d.CreatedByMe = d.CreatedByMe || j == 0
			d.version(at, evADRVersion)
			if id := f.changeOf[r.Repo+"@"+v.SHA]; f.changes[id] != nil {
				changes[id] = true
			}
		}
		for _, c := range f.Changes {
			if changes[c.ID] {
				d.Changes = append(d.Changes, c.ID)
			}
		}
		f.addDoc(d, decisionWords)
	}
	slices.SortFunc(f.Docs, func(a, b *Doc) int { return cmp.Or(cmp.Compare(a.CreatedAt, b.CreatedAt), cmp.Compare(a.ID, b.ID)) })
}

// version records a version the user saved at at.
func (d *Doc) version(at time.Time, kind string) {
	d.evs = append(d.evs, event{at: at, kind: kind, key: d.Ref}, event{at: at, kind: evActive, key: d.Ref})
	d.days = append(d.days, day(at))
}

// addDoc decides d's share and whether it is substantial, from minWords, and adds it.
func (f *Facts) addDoc(d *Doc, minWords int) {
	if d.Versions > 0 {
		d.Share = d.MyVersions * 100 / d.Versions
	}
	d.Substantial = d.Words >= minWords && d.Share >= 50 && (d.CreatedByMe || d.MyVersions >= 3)
	if len(d.days) > 0 {
		slices.Sort(d.days)
		d.First, d.Last = d.days[0], d.days[len(d.days)-1]
	}
	f.docs[d.ID] = d
	f.Docs = append(f.Docs, d)
}

// decisionVersionMine says whether a commit that changed a decision record is the user's work:
// their own, or an AI agent's in their PR or crediting them.
func (f *Facts) decisionVersionMine(v collect.DecisionVersion) bool {
	if slices.Contains(v.Roles, "author") {
		return true
	}
	return (slices.Contains(v.Roles, "pr-author") || slices.Contains(v.Roles, "co-author")) &&
		f.people.class(v.Author.Name, v.Author.Email) == Agent
}

// decisionRefs names each decision record adr+<repo>+<file name>, without the extension, or by
// its path where two in a repo share a file name.
func (f *Facts) decisionRefs() []string {
	stem := func(p string) string { return strings.TrimSuffix(p, path.Ext(p)) }
	names := map[string]int{}
	for _, r := range f.raw.Decisions {
		names[r.Repo+"/"+stem(path.Base(r.Path))]++
	}
	refs := make([]string, len(f.raw.Decisions))
	for i, r := range f.raw.Decisions {
		name := stem(path.Base(r.Path))
		if names[r.Repo+"/"+name] > 1 {
			name = stem(r.Path)
		}
		refs[i] = "adr+" + refPart(r.Repo) + "+" + refPart(name)
	}
	return refs
}

// body is the doc's current text.
func (d *Doc) body() string {
	if d.adr != nil {
		return d.adr.Body
	}
	return deref(d.rec.Body)
}

// linkDocs fills each doc's items, once the items exist: those of the issues it names, and an
// ADR's changes'.
func (f *Facts) linkDocs() {
	for _, d := range f.Docs {
		set := map[string]bool{}
		for _, k := range d.Keys {
			if id, _ := f.itemOf(k); f.items[id] != nil {
				set[id] = true
			}
		}
		for _, id := range d.Changes {
			set[f.changes[id].Item] = true
		}
		delete(set, d.Item)
		d.Items = sortedSet(set)
	}
}
