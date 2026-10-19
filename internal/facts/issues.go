package facts

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/tracker"
)

// Involvement in an issue, strongest first. Issues the user worked on, raised or discussed make work
// items; the rest are context.
const (
	Done       = "done"       // done, and the user was its assignee at resolution or made the final transition
	Worked     = "worked"     // the user moved its status, logged work on it, or a change of theirs names it
	Raised     = "raised"     // the user reported or created it
	Discussed  = "discussed"  // the user commented on it
	Assigned   = "assigned"   // assigned to the user (or edited by them), with no work on it
	Referenced = "referenced" // named only by git work that facts does not trust (branches older than it)
	Ancestor   = "ancestor"   // loaded only as the parent of the others: context
)

var involvementRank = map[string]int{Done: 0, Worked: 1, Raised: 2, Discussed: 3, Assigned: 4, Referenced: 5, Ancestor: 6}

// makesItem says whether an issue the user is involved in this way is work of theirs.
func makesItem(involvement string) bool {
	return involvementRank[involvement] <= involvementRank[Discussed]
}

// Issue is one line of issues.jsonl: an issue in the dataset, and what the user did on it.
type Issue struct {
	Key            string   `json:"key"`
	PreviousKeys   []string `json:"previous_keys"`
	Title          string   `json:"title"`
	URL            string   `json:"url"`
	Project        string   `json:"project"`
	Type           string   `json:"type"`
	Level          int      `json:"level"`
	Status         string   `json:"status"`
	StatusCategory string   `json:"status_category"`
	Roles          []string `json:"roles"`
	Involvement    string   `json:"involvement"`
	DoneByMe       bool     `json:"done_by_me"`
	CreatedAt      string   `json:"created_at"`
	ResolvedAt     *string  `json:"resolved_at"`
	Parent         string   `json:"parent"`     // current key; "" for none
	Item           string   `json:"item"`       // the work item it is part of; "" when it is not the user's work
	Initiative     string   `json:"initiative"` // the nearest issue above it at epic level or above; ""
	Changes        []string `json:"changes"`    // the user's changes naming it, trusted keys only

	rec      *collect.IssueRecord
	ancestor bool
	created  time.Time
	resolved time.Time
}

// buildIssues indexes the issues by every key they answer to, finds the user's tracker account,
// and decides done_by_me. Involvement waits for the changes (buildItems).
func (f *Facts) buildIssues() {
	for i := range f.raw.Issues {
		rec := &f.raw.Issues[i]
		for _, k := range append([]string{rec.Key}, rec.PreviousKeys...) {
			f.keyOf[strings.ToUpper(k)] = rec.Key
		}
	}
	f.userID = trackerAccount(f.raw.Issues)
	for i := range f.raw.Issues {
		rec := &f.raw.Issues[i]
		if _, dup := f.issues[rec.Key]; dup {
			continue
		}
		is := &Issue{
			Key: rec.Key, PreviousKeys: orEmpty(rec.PreviousKeys), Title: rec.Summary, URL: rec.URL, Project: rec.Project.Key,
			Type: rec.Type, Level: rec.Level, Status: rec.Status, StatusCategory: rec.StatusCategory, Roles: orEmpty(rec.Roles),
			CreatedAt: rec.CreatedAt, ResolvedAt: rec.ResolvedAt, Changes: []string{},
			rec: rec, ancestor: slices.Contains(rec.Roles, Ancestor),
		}
		is.created, _ = parseTime(rec.CreatedAt)
		if rec.Parent != nil {
			is.Parent = f.currentKey(rec.Parent.Key)
		}
		is.DoneByMe, is.resolved = doneByMe(rec, f.userID)
		if r, ok := timeOf(rec.ResolvedAt); ok {
			is.resolved = r
		}
		f.issues[rec.Key] = is
		f.Issues = append(f.Issues, is)
	}
	slices.SortFunc(f.Issues, func(a, b *Issue) int { return collect.NaturalCompare(a.Key, b.Key) })
}

// currentKey is the key an issue has now, for any key it has had; unknown keys come back as given.
func (f *Facts) currentKey(key string) string {
	if k, ok := f.keyOf[strings.ToUpper(key)]; ok {
		return k
	}
	return strings.ToUpper(key)
}

// trackerAccount is the user's account in the tracker: the author of the comments, changes and
// worklogs the collection marked as theirs (the most frequent, should there be several).
func trackerAccount(issues []collect.IssueRecord) string {
	counts := map[string]int{}
	mark := func(by bool, u *tracker.User) {
		if by && u != nil {
			counts[u.ID]++
		}
	}
	for i := range issues {
		for _, c := range issues[i].Comments {
			mark(c.ByUser, c.Author)
		}
		for _, h := range issues[i].History {
			mark(h.ByUser, h.Author)
		}
		for _, w := range issues[i].Worklogs {
			mark(w.ByUser, w.Author)
		}
	}
	best := ""
	for id, n := range counts {
		if best == "" || n > counts[best] || (n == counts[best] && id < best) {
			best = id
		}
	}
	return best
}

// doneByMe says whether a done issue was the user's to finish: they made its final status change,
// or were its assignee when it was resolved. resolved is when the final status change happened.
func doneByMe(rec *collect.IssueRecord, userID string) (bool, time.Time) {
	var last *tracker.Change
	for i := range rec.History {
		if slices.ContainsFunc(rec.History[i].Items, func(it tracker.ChangeItem) bool { return it.Field == "status" }) {
			last = &rec.History[i]
		}
	}
	var resolved time.Time
	if last != nil {
		resolved, _ = parseTime(last.CreatedAt)
	}
	if r, ok := timeOf(rec.ResolvedAt); ok {
		resolved = r
	}
	if rec.StatusCategory != "done" {
		return false, resolved
	}
	if last != nil && last.ByUser {
		return true, resolved
	}
	// The assignee then: the current one, with every assignment made after the resolution undone.
	assignee := ""
	if rec.Assignee != nil {
		assignee = rec.Assignee.ID
	}
	for i := len(rec.History) - 1; i >= 0 && !resolved.IsZero(); i-- {
		h := rec.History[i]
		if at, ok := parseTime(h.CreatedAt); !ok || !at.After(resolved) {
			break
		}
		for _, it := range h.Items {
			if it.Field == "assignee" {
				assignee = deref(it.FromID)
			}
		}
	}
	return userID != "" && assignee == userID, resolved
}

// involvement is the user's strongest involvement in the issue.
func (is *Issue) involvement() string {
	has := func(role string) bool { return slices.Contains(is.Roles, role) }
	switch {
	case is.ancestor:
		return Ancestor
	case is.DoneByMe:
		return Done
	case len(is.Changes) > 0 || is.movedStatus() || slices.ContainsFunc(is.rec.Worklogs, func(w tracker.Worklog) bool { return w.ByUser }):
		return Worked
	case has("reporter") || has("creator"):
		return Raised
	case has("commenter"):
		return Discussed
	case has("assignee") || has("editor"):
		return Assigned
	}
	return Referenced
}

func (is *Issue) movedStatus() bool {
	return slices.ContainsFunc(is.rec.History, func(h tracker.Change) bool {
		return h.ByUser && slices.ContainsFunc(h.Items, func(it tracker.ChangeItem) bool { return it.Field == "status" })
	})
}

// issueEvents are what the user did on the issue, before it has an item.
func (is *Issue) issueEvents() []event {
	var evs []event
	work := func(ts string) {
		if at, ok := parseTime(ts); ok {
			if !is.ancestor {
				evs = append(evs, event{at: at, kind: evIssueWorked, key: is.Key})
			}
			evs = append(evs, event{at: at, kind: evActive, key: is.Key})
		}
	}
	for _, h := range is.rec.History {
		if h.ByUser && slices.ContainsFunc(h.Items, func(it tracker.ChangeItem) bool { return it.Field == "status" }) {
			work(h.CreatedAt)
		}
	}
	for _, c := range is.rec.Comments {
		if c.ByUser {
			work(c.CreatedAt)
		}
	}
	for _, w := range is.rec.Worklogs {
		if w.ByUser {
			work(w.StartedAt)
		}
	}
	if slices.Contains(is.Roles, "creator") || slices.Contains(is.Roles, "reporter") {
		evs = append(evs, event{at: is.created, kind: evActive, key: is.Key}) // writing it up
	}
	if !is.ancestor && (slices.Contains(is.Roles, "reporter") || slices.Contains(is.Roles, "creator")) {
		evs = append(evs, event{at: is.created, kind: evIssueRaised, key: is.Key})
	}
	if !is.ancestor && is.DoneByMe {
		evs = append(evs, event{at: is.resolved, kind: evIssueDone, key: is.Key})
	}
	return evs
}

// itemOf is the work item an issue belongs to: the nearest issue at or above it that is not a
// sub-task (a feature), or, at epic level or above, the issue itself (an initiative). A sub-task
// whose parent is unknown stands for itself.
func (f *Facts) itemOf(key string) (id, kind string) {
	is := f.issues[key]
	for depth := 0; is != nil && is.Level < tracker.StandardLevel && depth < 10; depth++ {
		p := f.issues[is.Parent]
		if p == nil {
			break
		}
		is = p
	}
	switch {
	case is == nil:
		return "", ""
	case is.Level >= tracker.EpicLevel:
		return is.Key, KindInitiative
	}
	return is.Key, KindFeature
}

// initiativeOf is the nearest issue above key at epic level or above, or "".
func (f *Facts) initiativeOf(key string) string {
	is := f.issues[key]
	for depth := 0; is != nil && depth < 10; depth++ {
		p := f.issues[is.Parent]
		if p == nil {
			return ""
		}
		if p.Level >= tracker.EpicLevel {
			return p.Key
		}
		is = p
	}
	return ""
}

// bestKey is the key a change counts toward when it names several: an issue the user is most
// involved in, then the first in natural order.
func (f *Facts) bestKey(keys []string) string {
	best := ""
	for _, k := range keys {
		if best == "" {
			best = k
			continue
		}
		a, b := f.issues[k], f.issues[best]
		if c := cmp.Compare(involvementRank[a.Involvement], involvementRank[b.Involvement]); c < 0 ||
			(c == 0 && collect.NaturalCompare(k, best) < 0) {
			best = k
		}
	}
	return best
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
