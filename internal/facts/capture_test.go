package facts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureOf(f *Facts, w Window) []string {
	var out []string
	for _, c := range f.CaptureItems(w) {
		out = append(out, c.Capture+" "+c.ID)
	}
	return out
}

// Since is the window from a day to the last day anything counts. After that day it is empty, so
// there is nothing to ask about.
func TestSince(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	last := f.Summary(All).Coverage.Last
	tests := []struct {
		name, since string
		empty       bool
		wantErr     bool
	}{
		{"a day before the last", "2024-03-01", false, false},
		{"the last day itself", last, false, false},
		{"a day after the last", "2030-01-01", true, false},
		{"not a day", "March", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := f.Since(tt.since)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Since(%q) error = %v, want error %v", tt.since, err, tt.wantErr)
			}
			if err == nil && (w.From.Equal(w.To) && !w.To.IsZero()) != tt.empty {
				t.Errorf("Since(%q) = %v, empty %v, want %v", tt.since, w, !w.To.IsZero() && w.From.Equal(w.To), tt.empty)
			}
		})
	}
	late, _ := f.Since("2030-01-01")
	check(t, "nothing to ask about after the last day", len(f.CaptureItems(late)), 0)
}

// An item is listed for a window when the user acted on it there, or its own issue was resolved
// there, whoever resolved it; it is finished (resolved, a doc, a bucket whose changes landed), new
// (the user's first day on it is in the window) or continued. Finished come first, then new, then
// continued; initiatives are left out.
func TestCaptureItems(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	march, _ := f.Since("2024-03-01")
	check(t, "the work of March on: a new bucket, then work that continued", captureOf(f, march),
		[]string{"new repo+app+2024q2", "continued ENG-1", "continued repo+app+2024q1"})

	// An issue someone else resolved after the user's last day on it still gets asked about.
	f.items["ENG-2"].own.ResolvedAt = ptr("2024-06-10T00:00:00Z")
	june, _ := NewWindow("2024-06-01", "2024-06-30")
	check(t, "resolved by someone else, with none of the user's events in the window", captureOf(f, june), []string{"finished ENG-2"})
	for _, c := range f.CaptureItems(june) {
		check(t, "its window is the day it was resolved", []any{c.First, c.Last, c.ActiveDays, c.Totals}, []any{"2024-06-10", "2024-06-10", 0, Totals{}})
	}

	all, _ := f.Since("2024-01-01")
	rank := map[string]int{CaptureFinished: 0, CaptureNew: 1, CaptureContinued: 2}
	prev := -1
	for _, c := range f.CaptureItems(all) {
		if rank[c.Capture] < prev {
			t.Errorf("%s %s listed after a later kind", c.Capture, c.ID)
		}
		if c.Kind == KindInitiative {
			t.Errorf("initiative %s listed: its features carry the work", c.ID)
		}
		prev = rank[c.Capture]
	}
}

// A user's context file marks the item as one they have already said something about.
func TestCaptureItemContext(t *testing.T) {
	ctx := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ctx, "items"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ctx, "items", "ENG-1.md"), []byte("It cut load times.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := build(t, fixture(), Inputs{ContextDir: ctx})
	w, _ := f.Since("2024-01-01")
	got := map[string]bool{}
	for _, c := range f.CaptureItems(w) {
		got[c.ID] = c.Context
	}
	check(t, "context flags", []bool{got["ENG-1"], got["repo+app+2024q1"]}, []bool{true, false})
}

// capture.txt is tab-separated: capture, id, effort, first..last, title.
func TestCaptureList(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	w, _ := f.Since("2024-03-01")
	lines := strings.Split(strings.TrimSpace(string(captureList(f.CaptureItems(w)))), "\n")
	if len(lines) != 3 {
		t.Fatalf("capture.txt has %d lines, want 3:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	for _, l := range lines {
		if cols := strings.Split(l, "\t"); len(cols) != 5 || !strings.Contains(cols[3], "..") {
			t.Errorf("capture.txt line %q: want 5 tab-separated columns with first..last", l)
		}
	}
}

// With Recent set, Write also writes recent/; the next Write without it drops the directory.
func TestWriteRecent(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	w, _ := f.Since("2024-03-01")
	dir := filepath.Join(t.TempDir(), "facts")
	f.Recent = &w
	if err := f.Write(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"facts.json", "items.jsonl", "capture.txt"} {
		if _, err := os.Stat(filepath.Join(dir, "recent", name)); err != nil {
			t.Errorf("no recent/%s: %v", name, err)
		}
	}
	f.Recent = nil
	if err := f.Write(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "recent")); err == nil {
		t.Error("recent/ outlived the run that wrote it")
	}
}
