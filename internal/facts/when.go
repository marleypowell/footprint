package facts

import (
	"fmt"
	"strings"
	"time"
)

const dayLayout = "2006-01-02"

// parseTime reads an ISO 8601 timestamp (any offset) or a date. ok is false for empty or invalid
// input.
func parseTime(ts string) (time.Time, bool) {
	if ts == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse(dayLayout, ts); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func timeOf(ts *string) (time.Time, bool) {
	if ts == nil {
		return time.Time{}, false
	}
	return parseTime(*ts)
}

// day is the UTC calendar day of t: every bucket is in UTC, whatever offset a timestamp carries.
func day(t time.Time) string { return t.UTC().Format(dayLayout) }

// quarter is t's UTC calendar quarter, as "2024q3".
func quarter(t time.Time) string {
	t = t.UTC()
	return fmt.Sprintf("%dq%d", t.Year(), (int(t.Month())+2)/3)
}

// Window is a span of UTC days. Its bounds are inclusive days; internally it is the half-open
// [From, To) of midnights. The zero Window is all time.
type Window struct {
	From, To time.Time // To is the midnight after the last day; zero means unbounded
}

// All is the window of all time.
var All = Window{}

// ParseWindow reads "all" or "YYYY-MM-DD..YYYY-MM-DD" (both days included).
func ParseWindow(s string) (Window, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "all" {
		return All, nil
	}
	from, to, ok := strings.Cut(s, "..")
	if !ok {
		return Window{}, fmt.Errorf("window %q: want all or YYYY-MM-DD..YYYY-MM-DD", s)
	}
	return NewWindow(from, to)
}

// NewWindow is the window from the day from to the day to, both included.
func NewWindow(from, to string) (Window, error) {
	f, err := time.Parse(dayLayout, strings.TrimSpace(from))
	if err != nil {
		return Window{}, fmt.Errorf("window start %q: want YYYY-MM-DD", from)
	}
	t, err := time.Parse(dayLayout, strings.TrimSpace(to))
	if err != nil {
		return Window{}, fmt.Errorf("window end %q: want YYYY-MM-DD", to)
	}
	if t.Before(f) {
		return Window{}, fmt.Errorf("window %s..%s ends before it starts", from, to)
	}
	return Window{From: f, To: t.AddDate(0, 0, 1)}, nil
}

func (w Window) IsAll() bool { return w.From.IsZero() && w.To.IsZero() }

func (w Window) Contains(t time.Time) bool {
	return (w.From.IsZero() || !t.Before(w.From)) && (w.To.IsZero() || t.Before(w.To))
}

// String is the window as drafts name it: "all" or "2025-04-01..2026-03-31".
func (w Window) String() string {
	if w.IsAll() {
		return "all"
	}
	return w.From.Format(dayLayout) + ".." + w.To.AddDate(0, 0, -1).Format(dayLayout)
}

// Dir is the window's directory name under windows/: "2025-04-01_2026-03-31".
func (w Window) Dir() string { return strings.Replace(w.String(), "..", "_", 1) }

// yearWindow and quarterWindow are the calendar periods facts.json breaks totals down by.
func yearWindow(y int) Window {
	from := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
	return Window{From: from, To: from.AddDate(1, 0, 0)}
}

func quarterWindow(y, q int) Window {
	from := time.Date(y, time.Month(3*q-2), 1, 0, 0, 0, 0, time.UTC)
	return Window{From: from, To: from.AddDate(0, 3, 0)}
}
