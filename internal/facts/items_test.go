package facts

import (
	"testing"

	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/tracker"
)

func TestEffortAndIDs(t *testing.T) {
	for days, want := range map[int]string{0: "XS", 1: "XS", 2: "S", 3: "S", 4: "M", 10: "M", 11: "L", 30: "L", 31: "XL"} {
		if got := effortOf(days); got != want {
			t.Errorf("effortOf(%d) = %s, want %s", days, got, want)
		}
	}
	at, _ := parseTime("2024-12-31T23:30:00-02:00") // 2025-01-01 in UTC
	check(t, "bucket ids are path-safe and UTC", []string{bucketID(".github-private", at), bucketID("a+b", at), itemPath("doc+x/y z")},
		[]string{"repo+.github-private+2025q1", "repo+a%2Bb+2025q1", "doc+x%2Fy%20z"})
}

func TestEffortRank(t *testing.T) {
	order := []string{"XL", "L", "M", "S", "XS", ""}
	for i := 1; i < len(order); i++ {
		if a, b := effortRank(order[i-1]), effortRank(order[i]); a >= b {
			t.Errorf("effortRank(%q) = %d, not before effortRank(%q) = %d", order[i-1], a, order[i], b)
		}
	}
}

// An id is a file name in packs/ and notes/: what is safe in one stays, the rest is
// percent-encoded; '+' separates an id's parts, so a part encodes its own.
func TestItemPath(t *testing.T) {
	for id, want := range map[string]string{
		"ENG-1": "ENG-1", "repo+app_2.x+2024q1": "repo+app_2.x+2024q1", "doc+12345": "doc+12345",
		"adr+web+apps/a/0001-x": "adr+web+apps%2Fa%2F0001-x", "a b": "a%20b", "x#1": "x%231", "50%": "50%25", "why?": "why%3F",
		// characters a URL path keeps but a file name may not (':' on Windows), and UTF-8 byte by byte
		"a:b": "a%3Ab", "a@b&c=d$e~f": "a%40b%26c%3Dd%24e%7Ef", "café": "caf%C3%A9",
	} {
		if got := itemPath(id); got != want {
			t.Errorf("itemPath(%q) = %q, want %q", id, got, want)
		}
	}
	for part, want := range map[string]string{"app": "app", "a+b": "a%2Bb", "web/app": "web%2Fapp"} {
		if got := refPart(part); got != want {
			t.Errorf("refPart(%q) = %q, want %q", part, got, want)
		}
	}
}

func TestItemNeedsAnID(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error(`item("") did not panic`)
		}
	}()
	(&Facts{items: map[string]*Item{}}).item("", KindBucket)
}

// Work naming no issue goes to its repo's bucket for the quarter it began.
func TestBuckets(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	b := f.items["repo+app+2024q1"]
	check(t, "a bucket", []any{b.Kind, b.Title, b.URL, b.Changes, b.Issues, b.Initiative == nil},
		[]any{KindBucket, "app: work naming no issue, 2024Q1", "https://code.example/org/app", []string{"app@c8", "app@c11"}, []string{}, true})
}

// A feature the user did not work on themselves, through a sub-task they finished, is theirs by
// contribution, and holds its other sub-tasks too; a change naming two features counts toward one,
// and the other lists it.
func TestItemMembership(t *testing.T) {
	done := func(r *collect.IssueRecord) {
		r.StatusCategory, r.Parent = "done", &tracker.IssueRef{Key: "ENG-20"}
		r.History = []tracker.Change{{Author: uJane, ByUser: true, CreatedAt: "2024-02-01T00:00:00Z", Items: []tracker.ChangeItem{{Field: "status"}}}}
	}
	raw := oneRepo([]collect.Commit{
		commit("c1", "2024-02-02T10:00:00Z", jane, []string{"author"}, "ENG-30 ENG-31: both", func(c *collect.Commit) { c.IssueKeys = []string{"ENG-30", "ENG-31"} }),
	}, nil, []collect.IssueRecord{
		issueRec("ENG-20", tracker.StandardLevel, func(r *collect.IssueRecord) {
			r.Roles, r.Subtasks = []string{"assignee"}, []tracker.IssueRef{{Key: "ENG-21"}, {Key: "ENG-22"}}
		}),
		issueRec("ENG-21", tracker.SubtaskLevel, done),
		issueRec("ENG-22", tracker.SubtaskLevel, func(r *collect.IssueRecord) { r.Parent = &tracker.IssueRef{Key: "ENG-20"} }),
		issueRec("ENG-30", tracker.StandardLevel, nil),
		issueRec("ENG-31", tracker.StandardLevel, nil),
	})
	f := build(t, raw, Inputs{})
	it := f.items["ENG-20"]
	check(t, "a feature through its sub-task", []any{it.Kind, it.Involvement, it.Issues, *it.Subtasks, it.First, it.Effort},
		[]any{KindFeature, "contributed", []string{"ENG-20", "ENG-21", "ENG-22"}, Subtasks{Total: 2, DoneByMe: 1}, "2024-02-01", "XS"})
	a, b := f.items["ENG-30"], f.items["ENG-31"]
	check(t, "the change's item: the first key", []any{f.changes["app@c1"].Item, f.changes["app@c1"].AlsoItems}, []any{"ENG-30", []string{"ENG-31"}})
	check(t, "its lists", []any{a.Changes, a.AlsoChanges, b.Changes, b.AlsoChanges}, []any{[]string{"app@c1"}, []string{}, []string{}, []string{"app@c1"}})
	check(t, "its totals: the commit counts once, the work on each issue in its own item",
		[]any{a.Totals.Commits, a.Totals.IssuesWorked, b.Totals.Commits, b.Totals.IssuesWorked}, []any{1, 1, 0, 1})
	check(t, "a feature without sub-tasks has none", a.Subtasks, (*Subtasks)(nil))
}
