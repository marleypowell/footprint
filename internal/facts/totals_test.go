package facts

import (
	"testing"
)

// Each total is defined by the events it counts: some are a count of events, others the distinct
// things the events are about.
func TestTotalsOf(t *testing.T) {
	var evs []event
	add := func(ts, kind, key, item string, mod ...func(*event)) {
		e := event{at: mustTime(t, ts), kind: kind, key: key, item: item}
		for _, m := range mod {
			m(&e)
		}
		evs = append(evs, e)
	}
	lines := func(repo string, added, deleted int) func(*event) {
		return func(e *event) { e.repo, e.added, e.deleted = repo, added, deleted }
	}
	by := func(who string) func(*event) { return func(e *event) { e.who = who } }

	add("2024-01-01T10:00:00Z", evActive, "app#1", "")
	add("2024-01-01T22:00:00Z", evActive, "app#1", "")      // the same day
	add("2024-01-02T00:30:00+01:00", evActive, "app#1", "") // 2024-01-01 in UTC
	add("2024-01-03T10:00:00Z", evActive, "app#1", "")
	add("2024-01-01T10:00:00Z", evPROpened, "app#1", "ENG-1")
	add("2024-01-01T10:00:00Z", evPROpened, "app#2", "ENG-1")
	add("2024-01-02T10:00:00Z", evPRMerged, "app#1", "ENG-1")
	add("2024-01-02T10:00:00Z", evMigratedPR, "app@m1", "ENG-1")
	add("2024-01-02T10:00:00Z", evMergedForOthers, "app@o1", "")
	for _, sha := range []string{"a", "b", "c"} {
		add("2024-01-02T10:00:00Z", evCommit, "app@"+sha, "ENG-1")
	}
	add("2024-01-02T10:00:00Z", evCoAuthored, "app@d", "")
	add("2024-01-02T10:00:00Z", evAgentCommit, "app@e", "ENG-1")
	add("2024-01-02T10:00:00Z", evLanded, "app#1", "ENG-1", lines("app", 10, 2))
	add("2024-01-02T10:00:00Z", evLanded, "lib@f", "", lines("lib", 5, 1))
	add("2024-01-02T10:00:00Z", evLanded, "app@g", "", lines("app", 1, 0))
	add("2024-01-04T10:00:00Z", evIssueDone, "ENG-1", "ENG-1")
	add("2024-01-04T10:00:00Z", evIssueWorked, "ENG-1", "ENG-1")
	add("2024-01-04T10:00:00Z", evIssueWorked, "ENG-2", "ENG-1")
	add("2024-01-04T10:00:00Z", evIssueRaised, "ENG-3", "") // raising an issue is not working on it
	add("2024-01-05T10:00:00Z", evPageCreated, "doc+1", "")
	add("2024-01-05T10:00:00Z", evPageVersion, "doc+1", "") // a page that is no item: edited, not a doc written
	add("2024-01-06T10:00:00Z", evPageVersion, "doc+1", "")
	add("2024-01-06T10:00:00Z", evPageVersion, "doc+2", "doc+2")
	add("2024-01-06T10:00:00Z", evADRVersion, "adr+app+a", "") // not substantial: nothing
	add("2024-01-06T10:00:00Z", evADRVersion, "adr+app+b", "adr+app+b")
	add("2024-01-07T10:00:00Z", evADRVersion, "adr+app+b", "adr+app+b")
	add("2024-01-08T10:00:00Z", evReview, "review+app#21", "", by("Sam"))
	add("2024-01-08T11:00:00Z", evChangesRequested, "review+app#21", "", by("sam")) // one PR, one author
	add("2024-01-08T12:00:00Z", evReview, "review+app#22", "", by("Alex"))
	add("2024-01-08T12:00:00Z", evBotReview, "review+app#23", "", by("renovate"))
	add("2024-01-09T12:00:00Z", evBotReview, "review+app#23", "", by("renovate"))
	for range 3 {
		add("2024-01-09T12:00:00Z", evReviewComment, "review+app#21", "")
	}
	add("2025-01-01T00:00:00Z", evCommit, "app@later", "ENG-9") // outside the window

	w, _ := NewWindow("2024-01-01", "2024-12-31")
	check(t, "totals", totalsOf(evs, w), Totals{
		ActiveDays: 2, ChangesLanded: 3, PRsOpened: 2, PRsMerged: 1, MigratedPRsMerged: 1, MergedForOthers: 1, Commits: 3,
		CommitsCoAuthored: 1, AgentCommits: 1, LinesAdded: 16, LinesDeleted: 3, Repos: 2, IssuesDone: 1, IssuesWorked: 2, IssuesRaised: 1,
		PagesCreated: 1, PagesEdited: 2, DocsWritten: 2, ADRsWritten: 1, PRsReviewed: 2, ChangesRequested: 1, ReviewComments: 3,
		AuthorsReviewed: 2, BotPRsReviewed: 1, Items: 3,
	})
	check(t, "all time takes the later commit too", []int{totalsOf(evs, All).Commits, totalsOf(evs, All).Items}, []int{4, 4})
	check(t, "nothing", totalsOf(nil, All), Totals{})
}

func TestActiveDaysAndSpan(t *testing.T) {
	evs := []event{
		{at: mustTime(t, "2024-03-02T10:00:00Z"), kind: evActive},
		{at: mustTime(t, "2024-03-01T23:30:00-01:00"), kind: evActive}, // 2024-03-02 in UTC
		{at: mustTime(t, "2024-02-28T10:00:00Z"), kind: evActive},
		{at: mustTime(t, "2024-02-01T10:00:00Z"), kind: evIssueDone}, // not the user's own activity, but in the span
		{at: mustTime(t, "2024-05-01T10:00:00Z"), kind: evActive},
	}
	check(t, "active days", activeDays(evs, All), []string{"2024-02-28", "2024-03-02", "2024-05-01"})
	w, _ := NewWindow("2024-01-01", "2024-03-31")
	check(t, "active days in a window", activeDays(evs, w), []string{"2024-02-28", "2024-03-02"})
	check(t, "none", activeDays(nil, All), []string{})
	first, last := span(evs, All)
	check(t, "span: any event", []string{first, last}, []string{"2024-02-01", "2024-05-01"})
	first, last = span(evs, w)
	check(t, "span in a window", []string{first, last}, []string{"2024-02-01", "2024-03-02"})
	first, last = span(nil, All)
	check(t, "no span", []string{first, last}, []string{"", ""})
}

// Totals break down by calendar year and quarter, leaving out the periods with nothing in them,
// and clipped to the window.
func TestPeriods(t *testing.T) {
	evs := []event{
		{at: mustTime(t, "2023-12-31T23:00:00Z"), kind: evCommit},
		{at: mustTime(t, "2024-02-01T00:00:00Z"), kind: evCommit},
		{at: mustTime(t, "2024-08-01T00:00:00Z"), kind: evCommit},
		{at: mustTime(t, "2024-08-02T00:00:00Z"), kind: evCommit},
	}
	commits := func(ps []Period) map[string]int {
		m := map[string]int{}
		for _, p := range ps {
			m[p.Period] = p.Totals.Commits
		}
		return m
	}
	years, quarters := periods(evs, All)
	check(t, "years", commits(years), map[string]int{"2023": 1, "2024": 3})
	check(t, "quarters, the empty ones left out", commits(quarters), map[string]int{"2023q4": 1, "2024q1": 1, "2024q3": 2})
	check(t, "in order", []string{quarters[0].Period, quarters[2].Period}, []string{"2023q4", "2024q3"})
	w, _ := NewWindow("2024-01-15", "2024-08-01")
	years, quarters = periods(evs, w)
	check(t, "in a window", []any{commits(years), commits(quarters)}, []any{map[string]int{"2024": 2}, map[string]int{"2024q1": 1, "2024q3": 1}})
	years, quarters = periods(nil, All)
	check(t, "nothing: empty lists, not null", []any{years, quarters}, []any{[]Period{}, []Period{}})
}

func TestIntersect(t *testing.T) {
	win := func(from, to string) Window { w, _ := NewWindow(from, to); return w }
	for _, tc := range []struct {
		name string
		a, b Window
		want string
	}{
		{"with all time", win("2024-01-01", "2024-12-31"), All, "2024-01-01..2024-12-31"},
		{"all time with a window", All, win("2024-01-01", "2024-12-31"), "2024-01-01..2024-12-31"},
		{"overlapping", win("2024-01-01", "2024-12-31"), win("2024-06-01", "2025-06-30"), "2024-06-01..2024-12-31"},
		{"one inside the other", win("2024-01-01", "2024-12-31"), win("2024-03-01", "2024-03-31"), "2024-03-01..2024-03-31"},
	} {
		if got := intersect(tc.a, tc.b).String(); got != tc.want {
			t.Errorf("intersect(%s): %s, want %s", tc.name, got, tc.want)
		}
	}
	empty := intersect(win("2024-01-01", "2024-01-31"), win("2024-03-01", "2024-03-31"))
	if empty.IsAll() || empty.Contains(mustTime(t, "2024-01-15")) || empty.Contains(mustTime(t, "2024-03-15")) {
		t.Errorf("intersect of disjoint windows = %+v, want an empty window", empty)
	}
}
