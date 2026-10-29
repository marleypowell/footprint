package facts

import (
	"testing"

	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/tracker"
)

// Team signals: the board of the sprint that held each issue when the user worked on it, the
// issues' projects, and their team field, by quarter.
func TestTeamSignals(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	check(t, "signals", f.teamSignals(All), []TeamSignals{{
		Period:   "2024q1",
		Boards:   []Signal{{Name: "Search Squad", Issues: 2}},
		Projects: []Signal{{Name: "ENG", Title: "Engineering", Issues: 3}},
		Teams:    []Signal{{Name: "Platform", Issues: 1}},
	}})
	w, _ := NewWindow("2024-04-01", "2024-06-30")
	check(t, "a window with no issue work", f.teamSignals(w), []TeamSignals{})
	for name, want := range map[string]string{
		"Checkout 2020 Sprint 10": "Checkout", "Checkout CY22 Sprint 23": "Checkout", "Web Team CY23 TO 2": "Web Team",
		"Checkout 2021 - Sprint 5": "Checkout", "Payments Sprint 1": "Payments", "Payments Archive": "Payments Archive",
		"Sprint 12": "Sprint 12", "Cycle 3": "Cycle 3",
	} {
		if got := boardOf(name); got != want {
			t.Errorf("boardOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func sprint(name, start, end string, completed *string) tracker.Sprint {
	s := tracker.Sprint{Name: name, CompletedAt: completed}
	if start != "" {
		s.StartAt = &start
	}
	if end != "" {
		s.EndAt = &end
	}
	return s
}

// The board an issue was on at a time is that of the latest-starting sprint running then: from its
// start to its completion (or, not completed, its planned end), to the end of that day.
func TestSprintBoard(t *testing.T) {
	is := &Issue{rec: &collect.IssueRecord{Issue: tracker.Issue{Sprints: []tracker.Sprint{
		sprint("Search 2024 Sprint 1", "2024-01-01T09:00:00Z", "2024-01-12T17:00:00Z", nil),
		sprint("Checkout Sprint 7", "2024-01-08T09:00:00Z", "2024-01-19T17:00:00Z", nil),
		sprint("Search 2024 Sprint 2", "2024-01-22T09:00:00Z", "2024-02-02T17:00:00Z", ptr("2024-01-25T10:00:00Z")),
		sprint("Ghost Sprint 1", "", "2024-12-31T00:00:00Z", nil),          // never started: no span
		sprint("Open Sprint 1", "2024-01-26T00:00:00Z", "", nil),           // no end: no span
		sprint("Search 2024 Sprint 9", "bad", "2024-12-31T00:00:00Z", nil), // unreadable start
	}}}}
	for ts, want := range map[string]string{
		"2023-12-31T12:00:00Z": "",
		"2024-01-05T12:00:00Z": "Search",
		"2024-01-10T12:00:00Z": "Checkout", // both run: the later start wins
		"2024-01-19T23:59:00Z": "Checkout", // to the end of its last day
		"2024-01-20T00:00:00Z": "",
		"2024-01-25T23:00:00Z": "Search", // completed early, on the 25th
		"2024-01-26T00:00:00Z": "",       // completed: its planned end no longer counts
	} {
		if got := sprintBoard(is, mustTime(t, ts)); got != want {
			t.Errorf("sprintBoard(%s) = %q, want %q", ts, got, want)
		}
	}
}

// Signals count distinct issues per quarter; the most issues first, then by name. Only work on an
// issue (not, say, reading it) counts, and a blank team field is no team.
func TestTeamSignalsRanking(t *testing.T) {
	ops := tracker.ProjectRef{Key: "OPS", Name: "Operations"}
	mk := func(key string, p tracker.ProjectRef, team string, sprints ...tracker.Sprint) *Issue {
		return &Issue{Key: key, Project: p.Key, rec: &collect.IssueRecord{Issue: tracker.Issue{Key: key, Project: p, Team: ptr(team), Sprints: sprints}}}
	}
	q1 := func(name string) tracker.Sprint {
		return sprint(name, "2024-01-01T00:00:00Z", "2024-03-31T00:00:00Z", nil)
	}
	f := &Facts{issues: map[string]*Issue{
		"ENG-1": mk("ENG-1", project, "", q1("Checkout CY24 Sprint 1")),
		"ENG-2": mk("ENG-2", project, "Platform", q1("Search CY24 Sprint 1")),
		"ENG-3": mk("ENG-3", project, " ", q1("Search CY24 Sprint 2")),
		"ENG-5": mk("ENG-5", project, "", q1("Billing CY24 Sprint 1")),
		"OPS-1": mk("OPS-1", ops, "  "),
	}}
	for _, e := range []struct{ ts, kind, key string }{
		{"2024-01-10T00:00:00Z", evIssueWorked, "ENG-1"},
		{"2024-01-11T00:00:00Z", evIssueDone, "ENG-1"}, // the same issue again: one
		{"2024-01-15T00:00:00Z", evIssueWorked, "ENG-2"},
		{"2024-02-01T00:00:00Z", evIssueWorked, "ENG-3"},
		{"2024-02-02T00:00:00Z", evIssueDone, "ENG-5"},
		{"2024-02-03T00:00:00Z", evActive, "ENG-9"},       // not work on an issue
		{"2024-02-04T00:00:00Z", evIssueWorked, "GONE-1"}, // not in the dataset
		{"2024-04-02T00:00:00Z", evIssueWorked, "OPS-1"},
	} {
		f.events = append(f.events, event{at: mustTime(t, e.ts), kind: e.kind, key: e.key})
	}
	check(t, "signals", f.teamSignals(All), []TeamSignals{
		{Period: "2024q1",
			Boards:   []Signal{{Name: "Search", Issues: 2}, {Name: "Billing", Issues: 1}, {Name: "Checkout", Issues: 1}},
			Projects: []Signal{{Name: "ENG", Title: "Engineering", Issues: 4}},
			Teams:    []Signal{{Name: "Platform", Issues: 1}}},
		{Period: "2024q2", Boards: []Signal{}, Projects: []Signal{{Name: "OPS", Title: "Operations", Issues: 1}}, Teams: []Signal{}},
	})
}

// A sprint's board is its name up to the first word that numbers it; a name that starts with one
// has no team in it, and is kept whole.
func TestBoardOfPeriodWords(t *testing.T) {
	for name, want := range map[string]string{
		"Platform FY25 Sprint 2": "Platform", "Platform FY2025 Q3": "Platform", "Platform Iteration 4": "Platform",
		"Platform Q3 Sprint 1": "Platform", "Big Team 1999 Sprint 3": "Big Team", "  Spaced   Out  Sprint 1 ": "Spaced Out",
		"Q3 Platform": "Q3 Platform", "2024 Sprint 1": "2024 Sprint 1", "Payments": "Payments", "Team 2100": "Team",
		"": "",
	} {
		if got := boardOf(name); got != want {
			t.Errorf("boardOf(%q) = %q, want %q", name, got, want)
		}
	}
}
