package facts

import (
	"strings"
	"testing"
	"time"
)

func TestWindows(t *testing.T) {
	w, err := ParseWindow("2024-01-01..2024-01-31")
	if err != nil {
		t.Fatal(err)
	}
	in, _ := parseTime("2024-01-31T23:59:59Z")
	out, _ := parseTime("2024-02-01T00:00:00Z")
	check(t, "both days included, half-open", []bool{w.Contains(in), w.Contains(out)}, []bool{true, false})
	check(t, "names", []string{w.String(), w.Dir(), All.String()}, []string{"2024-01-01..2024-01-31", "2024-01-01_2024-01-31", "all"})
	if _, err := ParseWindow("2024-02-01..2024-01-01"); err == nil {
		t.Error("want an error for a window that ends before it starts")
	}
	f := build(t, fixture(), Inputs{})
	s := f.Summary(w)
	check(t, "window totals", []int{s.Totals.PRsMerged, s.Totals.Commits, s.Totals.MigratedPRsMerged}, []int{1, 3, 0})
	for _, it := range f.windowItems(w) {
		if it.ID == "repo+app+2024q2" {
			t.Error("an item with nothing in the window is in its view")
		}
	}
}

// Timestamps carry any offset; every bucket (day, quarter) is in UTC.
func TestParseTime(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string // RFC 3339, UTC; "" for not ok
	}{
		{"2024-03-01T10:00:00Z", "2024-03-01T10:00:00Z"},
		{"2024-03-01T01:00:00+02:00", "2024-02-29T23:00:00Z"},
		{"2024-03-01", "2024-03-01T00:00:00Z"},
		{"", ""},
		{"yesterday", ""},
		{"2024-03-01 10:00", ""},
	} {
		got, ok := parseTime(tc.in)
		if s := got.Format(time.RFC3339); ok != (tc.want != "") || (ok && s != tc.want) {
			t.Errorf("parseTime(%q) = %s, %v; want %q", tc.in, s, ok, tc.want)
		}
	}
	if _, ok := timeOf(nil); ok {
		t.Error("timeOf(nil) is ok, want not ok")
	}
	if got, ok := timeOf(ptr("2024-03-01")); !ok || day(got) != "2024-03-01" {
		t.Errorf("timeOf(2024-03-01) = %v, %v", got, ok)
	}
}

func TestDayAndQuarter(t *testing.T) {
	for _, tc := range []struct{ ts, day, quarter string }{
		{"2024-01-01T00:00:00Z", "2024-01-01", "2024q1"},
		{"2024-03-31T23:59:59Z", "2024-03-31", "2024q1"},
		{"2024-04-01T00:30:00+01:00", "2024-03-31", "2024q1"}, // still March in UTC
		{"2024-06-30T12:00:00Z", "2024-06-30", "2024q2"},
		{"2024-07-01T00:00:00Z", "2024-07-01", "2024q3"},
		{"2024-12-31T23:30:00-02:00", "2025-01-01", "2025q1"},
	} {
		at := mustTime(t, tc.ts)
		if got := day(at); got != tc.day {
			t.Errorf("day(%s) = %s, want %s", tc.ts, got, tc.day)
		}
		if got := quarter(at); got != tc.quarter {
			t.Errorf("quarter(%s) = %s, want %s", tc.ts, got, tc.quarter)
		}
	}
	// a time in another zone is bucketed by its UTC day too
	tokyo := time.Date(2024, 4, 1, 8, 0, 0, 0, time.FixedZone("JST", 9*3600))
	if got := []string{day(tokyo), quarter(tokyo)}; got[0] != "2024-03-31" || got[1] != "2024q1" {
		t.Errorf("day, quarter of 2024-04-01 08:00 JST = %v, want [2024-03-31 2024q1]", got)
	}
}

func TestParseWindow(t *testing.T) {
	for _, tc := range []struct {
		in, want, err string
	}{
		{in: "all", want: "all"},
		{in: "", want: "all"},
		{in: "  all\n", want: "all"},
		{in: "2024-01-01..2024-12-31", want: "2024-01-01..2024-12-31"},
		{in: " 2024-01-01 .. 2024-12-31 ", want: "2024-01-01..2024-12-31"},
		{in: "2024-05-05..2024-05-05", want: "2024-05-05..2024-05-05"}, // a single day
		{in: "2024", err: "want all or YYYY-MM-DD..YYYY-MM-DD"},
		{in: "2024-01-01_2024-12-31", err: "want all or YYYY-MM-DD..YYYY-MM-DD"},
		{in: "2024-1-1..2024-12-31", err: `window start "2024-1-1"`},
		{in: "2024-01-01..soon", err: `window end "soon"`},
		{in: "2024-02-01..2024-01-31", err: "ends before it starts"},
	} {
		w, err := ParseWindow(tc.in)
		switch {
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("ParseWindow(%q) error = %v, want one containing %q", tc.in, err, tc.err)
		case tc.err == "" && err != nil:
			t.Errorf("ParseWindow(%q) error = %v", tc.in, err)
		case tc.err == "" && w.String() != tc.want:
			t.Errorf("ParseWindow(%q) = %s, want %s", tc.in, w, tc.want)
		}
	}
}

// A window is the half-open span of midnights from its first day to the day after its last; the
// zero window, All, holds everything.
func TestWindowBounds(t *testing.T) {
	w, _ := NewWindow("2024-05-05", "2024-05-05")
	for ts, want := range map[string]bool{
		"2024-05-04T23:59:59Z": false, "2024-05-05T00:00:00Z": true, "2024-05-05T23:59:59Z": true, "2024-05-06T00:00:00Z": false,
	} {
		if got := w.Contains(mustTime(t, ts)); got != want {
			t.Errorf("2024-05-05.Contains(%s) = %v, want %v", ts, got, want)
		}
	}
	check(t, "a one-day window's names", []string{w.String(), w.Dir()}, []string{"2024-05-05..2024-05-05", "2024-05-05_2024-05-05"})
	if !All.IsAll() || w.IsAll() || !All.Contains(time.Time{}) || !All.Contains(mustTime(t, "2999-01-01")) {
		t.Error("All holds every time, and only All is all")
	}
	from := Window{From: mustTime(t, "2024-01-01")} // open-ended: from a day on
	if from.Contains(mustTime(t, "2023-12-31T23:59:59Z")) || !from.Contains(mustTime(t, "2999-01-01")) {
		t.Error("a window without an end holds everything from its start")
	}
}

func TestCalendarWindows(t *testing.T) {
	check(t, "yearWindow(2024)", yearWindow(2024).String(), "2024-01-01..2024-12-31")
	for q, want := range map[int]string{1: "2024-01-01..2024-03-31", 2: "2024-04-01..2024-06-30", 3: "2024-07-01..2024-09-30", 4: "2024-10-01..2024-12-31"} {
		if got := quarterWindow(2024, q).String(); got != want {
			t.Errorf("quarterWindow(2024, %d) = %s, want %s", q, got, want)
		}
	}
}
