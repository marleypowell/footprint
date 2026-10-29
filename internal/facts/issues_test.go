package facts

import (
	"testing"

	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/tracker"
)

func TestIssues(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	is := func(key string) *Issue { return f.issues[key] }
	check(t, "the user's tracker account, from their marked activity", f.userID, "u-jane")
	check(t, "done: made the final transition", []any{is("ENG-1").DoneByMe, is("ENG-1").Involvement}, []any{true, Done})
	check(t, "done: assignee at resolution, reassigned since", []any{is("ENG-4").DoneByMe, is("ENG-4").Involvement}, []any{true, Done})
	check(t, "raised, and named by a change", is("ENG-2").Involvement, Worked)
	check(t, "assigned, named only by an older branch", []any{is("ENG-3").Involvement, is("ENG-3").Item}, []any{Assigned, ""})
	check(t, "ancestor", is("ENG-10").Involvement, Ancestor)
	check(t, "sub-task belongs to its story", is("ENG-4").Item, "ENG-1")

	it := f.items["ENG-1"]
	check(t, "feature", []any{it.Kind, it.Title, it.Initiative, it.Issues}, []any{KindFeature, "Fast search", &Ref{ID: "ENG-10", Title: "Search"}, []string{"ENG-1", "ENG-4"}})
	check(t, "feature sub-tasks", *it.Subtasks, Subtasks{Total: 2, DoneByMe: 1})
	check(t, "feature changes", []any{it.Changes, it.Docs}, []any{[]string{"app@c1", "app#5", "app@c12"}, []string{"doc+100", "adr+app+0001-use-queues"}})
	ini := f.items["ENG-10"]
	check(t, "initiative", []any{ini.Kind, ini.Features, ini.Involvement}, []any{KindInitiative, []string{"ENG-1"}, "contributed"})
	check(t, "initiative totals include its features'", ini.Totals.PRsMerged, it.Totals.PRsMerged)
	if _, ok := f.items["ENG-3"]; ok {
		t.Error("an issue the user only had assigned made an item")
	}
}

// Issues answer to every key they have had, in any case; a key of no issue comes back upper-cased.
// A record listed twice (an issue in two projects' files) is one issue, the first.
func TestBuildIssuesKeys(t *testing.T) {
	f := build(t, oneRepo(nil, nil, []collect.IssueRecord{
		issueRec("ENG-5", 0, func(r *collect.IssueRecord) { r.PreviousKeys, r.Summary = []string{"OLD-5"}, "first" }),
		issueRec("ENG-5", 0, func(r *collect.IssueRecord) { r.Summary = "second" }),
		issueRec("ENG-6", 0, func(r *collect.IssueRecord) { r.PreviousKeys, r.Roles = nil, nil }),
	}), Inputs{})
	for key, want := range map[string]string{"ENG-5": "ENG-5", "old-5": "ENG-5", "OLD-5": "ENG-5", "eng-6": "ENG-6", "zzz-9": "ZZZ-9"} {
		if got := f.currentKey(key); got != want {
			t.Errorf("currentKey(%q) = %q, want %q", key, got, want)
		}
	}
	check(t, "issues", []any{len(f.Issues), f.issues["ENG-5"].Title}, []any{2, "first"})
	check(t, "empty lists, never null", []any{f.issues["ENG-6"].PreviousKeys, f.issues["ENG-6"].Roles}, []any{[]string{}, []string{}})
}

// The user's tracker account is the author of the activity the collection marked as theirs: the
// most frequent, then the lowest id.
func TestTrackerAccount(t *testing.T) {
	a, b := &tracker.User{ID: "u-a"}, &tracker.User{ID: "u-b"}
	issues := []collect.IssueRecord{
		{Issue: tracker.Issue{Comments: []tracker.Comment{{Author: a, ByUser: true}, {Author: b}, {ByUser: true}}}},
		{Issue: tracker.Issue{History: []tracker.Change{{Author: b, ByUser: true}}, Worklogs: []tracker.Worklog{{Author: b, ByUser: true}}}},
	}
	check(t, "the most marked activity", trackerAccount(issues), "u-b")
	check(t, "a tie: the lowest id", trackerAccount(issues[:1:1]), "u-a")
	issues[0].Comments[0].Author = b
	issues[1].History[0].Author = a
	issues[1].Worklogs = nil
	check(t, "a tie the other way round", trackerAccount(issues), "u-a")
	check(t, "nothing marked", trackerAccount([]collect.IssueRecord{{Issue: tracker.Issue{Comments: []tracker.Comment{{Author: a}}}}}), "")
}

// A done issue was the user's to finish when they made its final status change, or were its
// assignee when it was resolved: the current assignee, with every reassignment since undone.
func TestDoneByMe(t *testing.T) {
	status := func(ts string, byUser bool) tracker.Change {
		return tracker.Change{CreatedAt: ts, ByUser: byUser, Items: []tracker.ChangeItem{{Field: "status", To: ptr("Done")}}}
	}
	assign := func(ts, from, to string) tracker.Change {
		return tracker.Change{CreatedAt: ts, Items: []tracker.ChangeItem{{Field: "assignee", FromID: ptr(from), ToID: ptr(to)}}}
	}
	for _, tc := range []struct {
		name         string
		category     string
		assignee     string
		history      []tracker.Change
		resolvedAt   *string
		userID       string
		want         bool
		wantResolved string
	}{
		{"not done, though the user moved it", "indeterminate", "", []tracker.Change{status("2024-01-05T00:00:00Z", true)}, nil, "u-jane", false, "2024-01-05"},
		{"the user made the final transition", "done", "u-other", []tracker.Change{status("2024-01-02T00:00:00Z", false), status("2024-01-05T00:00:00Z", true)},
			nil, "u-jane", true, "2024-01-05"},
		{"an earlier transition only", "done", "u-other", []tracker.Change{status("2024-01-02T00:00:00Z", true), status("2024-01-05T00:00:00Z", false)},
			nil, "u-jane", false, "2024-01-05"},
		{"the user's, done by another", "done", "u-jane", []tracker.Change{status("2024-01-05T00:00:00Z", false)}, nil, "u-jane", true, "2024-01-05"},
		{"the user's then, reassigned since", "done", "u-other", []tracker.Change{status("2024-01-05T00:00:00Z", false),
			assign("2024-01-06T00:00:00Z", "u-jane", "u-other")}, nil, "u-jane", true, "2024-01-05"},
		{"assigned to the user only after it was done", "done", "u-jane", []tracker.Change{status("2024-01-05T00:00:00Z", false),
			assign("2024-01-06T00:00:00Z", "u-other", "u-jane")}, nil, "u-jane", false, "2024-01-05"},
		{"assigned to the user before it was done", "done", "u-jane", []tracker.Change{assign("2024-01-04T00:00:00Z", "u-other", "u-jane"),
			status("2024-01-05T00:00:00Z", false)}, nil, "u-jane", true, "2024-01-05"},
		{"resolved_at, when the tracker has one, is when", "done", "u-other", []tracker.Change{status("2024-01-05T00:00:00Z", false),
			assign("2024-01-06T00:00:00Z", "u-jane", "u-other")}, ptr("2024-01-07T00:00:00Z"), "u-jane", false, "2024-01-07"},
		{"no history: the current assignee", "done", "u-jane", nil, nil, "u-jane", true, "0001-01-01"},
		{"no tracker account for the user", "done", "", []tracker.Change{status("2024-01-05T00:00:00Z", false)}, nil, "", false, "2024-01-05"},
	} {
		rec := &collect.IssueRecord{Issue: tracker.Issue{StatusCategory: tc.category, History: tc.history, ResolvedAt: tc.resolvedAt}}
		if tc.assignee != "" {
			rec.Assignee = &tracker.User{ID: tc.assignee}
		}
		got, resolved := doneByMe(rec, tc.userID)
		check(t, tc.name, []any{got, resolved.Format(dayLayout)}, []any{tc.want, tc.wantResolved})
	}
}

// Involvement is the strongest of what the user did on an issue. Done, worked, raised and
// discussed make work items; an issue only assigned to (or edited by) the user, or named only by
// git work facts does not trust, is context.
func TestInvolvement(t *testing.T) {
	statusByMe := []tracker.Change{{ByUser: true, Items: []tracker.ChangeItem{{Field: "status"}}}}
	for _, tc := range []struct {
		name string
		is   Issue
		want string
	}{
		{"an ancestor, whatever else", Issue{ancestor: true, DoneByMe: true, Roles: []string{"reporter"}}, Ancestor},
		{"done by the user", Issue{DoneByMe: true, Roles: []string{"assignee"}}, Done},
		{"a change of theirs names it", Issue{Changes: []string{"app#1"}, Roles: []string{"reporter"}}, Worked},
		{"they moved its status", Issue{rec: &collect.IssueRecord{Issue: tracker.Issue{History: statusByMe}}}, Worked},
		{"they logged work on it", Issue{rec: &collect.IssueRecord{Issue: tracker.Issue{Worklogs: []tracker.Worklog{{ByUser: true}}}}}, Worked},
		{"someone else's worklog", Issue{Roles: []string{"assignee"}, rec: &collect.IssueRecord{Issue: tracker.Issue{Worklogs: []tracker.Worklog{{}}}}}, Assigned},
		{"they changed only its assignee", Issue{rec: &collect.IssueRecord{Issue: tracker.Issue{History: []tracker.Change{
			{ByUser: true, Items: []tracker.ChangeItem{{Field: "assignee"}}}}}}}, Referenced},
		{"reported", Issue{Roles: []string{"reporter", "commenter"}}, Raised},
		{"created", Issue{Roles: []string{"creator"}}, Raised},
		{"commented", Issue{Roles: []string{"commenter", "assignee"}}, Discussed},
		{"assigned", Issue{Roles: []string{"assignee"}}, Assigned},
		{"edited", Issue{Roles: []string{"editor"}}, Assigned},
		{"named by an untrusted branch only", Issue{Roles: []string{"referenced"}}, Referenced},
	} {
		if tc.is.rec == nil {
			tc.is.rec = &collect.IssueRecord{}
		}
		if got := tc.is.involvement(); got != tc.want {
			t.Errorf("involvement of %s = %s, want %s", tc.name, got, tc.want)
		}
	}
	for inv, want := range map[string]bool{Done: true, Worked: true, Raised: true, Discussed: true, Assigned: false, Referenced: false, Ancestor: false} {
		if got := makesItem(inv); got != want {
			t.Errorf("makesItem(%s) = %v, want %v", inv, got, want)
		}
	}
}

// What the user did on an issue: status changes, comments and worklogs are work on it, and days of
// activity; raising it is a day of activity too. An ancestor (loaded only as a parent) gets the
// days but no work, raising or finishing.
func TestIssueEvents(t *testing.T) {
	rec := &collect.IssueRecord{Issue: tracker.Issue{
		History: []tracker.Change{
			{CreatedAt: "2024-01-03T00:00:00Z", ByUser: true, Items: []tracker.ChangeItem{{Field: "status"}}},
			{CreatedAt: "2024-01-04T00:00:00Z", ByUser: true, Items: []tracker.ChangeItem{{Field: "labels"}}}, // not work
			{CreatedAt: "2024-01-05T00:00:00Z", Items: []tracker.ChangeItem{{Field: "status"}}},               // someone else's
		},
		Comments: []tracker.Comment{{CreatedAt: "2024-01-06T00:00:00Z", ByUser: true}, {CreatedAt: "2024-01-07T00:00:00Z"}},
		Worklogs: []tracker.Worklog{{StartedAt: "2024-01-08T00:00:00Z", ByUser: true}, {StartedAt: "", ByUser: true}},
	}}
	created, resolved := mustTime(t, "2024-01-02"), mustTime(t, "2024-01-09")
	show := func(evs []event) []string {
		var out []string
		for _, e := range evs {
			out = append(out, day(e.at)+" "+e.kind)
		}
		return out
	}
	is := &Issue{Key: "ENG-1", Roles: []string{"creator"}, DoneByMe: true, created: created, resolved: resolved, rec: rec}
	check(t, "events", show(is.issueEvents()), []string{
		"2024-01-03 issue_worked", "2024-01-03 active", "2024-01-06 issue_worked", "2024-01-06 active", "2024-01-08 issue_worked", "2024-01-08 active",
		"2024-01-02 active", "2024-01-02 issue_raised", "2024-01-09 issue_done"})
	is.ancestor = true
	check(t, "an ancestor's", show(is.issueEvents()), []string{"2024-01-03 active", "2024-01-06 active", "2024-01-08 active", "2024-01-02 active"})
}

// An issue's item is the nearest issue at or above it that is not a sub-task (a feature), or an
// epic or above itself (an initiative); a sub-task whose parent is not in the dataset stands for
// itself. Its initiative is the nearest epic or above over it.
func TestItemOfAndInitiativeOf(t *testing.T) {
	f := &Facts{issues: map[string]*Issue{}}
	for _, is := range []*Issue{
		{Key: "INI-1", Level: 2},
		{Key: "EPIC-1", Level: tracker.EpicLevel, Parent: "INI-1"},
		{Key: "STORY-1", Level: tracker.StandardLevel, Parent: "EPIC-1"},
		{Key: "STORY-2", Level: tracker.StandardLevel},
		{Key: "SUB-1", Level: tracker.SubtaskLevel, Parent: "STORY-1"},
		{Key: "SUB-2", Level: tracker.SubtaskLevel, Parent: "GONE-1"},
		{Key: "SUB-3", Level: tracker.SubtaskLevel, Parent: "SUB-1"},
		{Key: "LOOP-1", Level: tracker.SubtaskLevel, Parent: "LOOP-2"},
		{Key: "LOOP-2", Level: tracker.SubtaskLevel, Parent: "LOOP-1"},
	} {
		f.issues[is.Key] = is
	}
	for _, tc := range []struct{ key, item, kind, initiative string }{
		{"SUB-1", "STORY-1", KindFeature, "EPIC-1"},
		{"SUB-3", "STORY-1", KindFeature, "EPIC-1"},
		{"SUB-2", "SUB-2", KindFeature, ""},
		{"STORY-1", "STORY-1", KindFeature, "EPIC-1"},
		{"STORY-2", "STORY-2", KindFeature, ""},
		{"EPIC-1", "EPIC-1", KindInitiative, "INI-1"},
		{"INI-1", "INI-1", KindInitiative, ""},
		{"GONE-1", "", "", ""},
	} {
		item, kind := f.itemOf(tc.key)
		check(t, "itemOf("+tc.key+")", []string{item, kind}, []string{tc.item, tc.kind})
		check(t, "initiativeOf("+tc.key+")", f.initiativeOf(tc.key), tc.initiative)
	}
	// a parent loop ends
	if item, _ := f.itemOf("LOOP-1"); item == "" {
		t.Error("itemOf(LOOP-1) = \"\", want one of the loop")
	}
	check(t, "initiativeOf(LOOP-1)", f.initiativeOf("LOOP-1"), "")
}

// A change naming several issues counts toward the one the user is most involved in, then the
// first in natural order.
func TestBestKey(t *testing.T) {
	f := &Facts{issues: map[string]*Issue{
		"ENG-1": {Involvement: Discussed}, "ENG-2": {Involvement: Worked}, "ENG-10": {Involvement: Worked}, "ENG-3": {Involvement: Done},
	}}
	for _, tc := range []struct {
		keys []string
		want string
	}{
		{[]string{"ENG-10", "ENG-1", "ENG-2"}, "ENG-2"},
		{[]string{"ENG-1", "ENG-3", "ENG-2"}, "ENG-3"},
		{[]string{"ENG-1"}, "ENG-1"},
		{nil, ""},
	} {
		if got := f.bestKey(tc.keys); got != tc.want {
			t.Errorf("bestKey(%v) = %q, want %q", tc.keys, got, tc.want)
		}
	}
}
