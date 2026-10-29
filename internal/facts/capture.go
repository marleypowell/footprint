package facts

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Capture kinds: why an item is on the list of what to ask the user about.
const (
	CaptureFinished  = "finished"  // resolved in the window (by anyone), or a doc saved, or a bucket's changes landed
	CaptureNew       = "new"       // the user's first day on it falls in the window
	CaptureContinued = "continued" // the user worked on it in the window, and before
)

// CaptureItem is a line of recent/items.jsonl: a window item, with why it is worth asking about.
type CaptureItem struct {
	*Item
	Capture string `json:"capture"`
	Context bool   `json:"context"` // context/items/<id>.md exists: the user has said something about it
}

// Since is the window from day since to the last day anything counts.
func (f *Facts) Since(day string) (Window, error) {
	last := f.Summary(All).Coverage.Last
	if last == "" || day > last {
		w, err := NewWindow(day, day)
		w.To = w.From // empty
		return w, err
	}
	return NewWindow(day, last)
}

// CaptureItems are the items to ask the user about for w, finished first, then new, then
// continued; within each, the most active days in the window first.
func (f *Facts) CaptureItems(w Window) []CaptureItem {
	items := f.windowItems(w)
	seen := map[string]bool{}
	for _, it := range items {
		seen[it.ID] = true
	}
	// Items whose own issue someone else resolved in the window: no event of the user's, but the
	// moment to ask what came of it.
	for _, it := range f.Items {
		if it.own == nil || seen[it.ID] || it.Kind == KindInitiative {
			continue
		}
		if at, ok := timeOf(it.own.ResolvedAt); ok && w.Contains(at) {
			c := *it
			c.Totals, c.days, c.ActiveDays, c.First, c.Last = Totals{}, nil, 0, day(at), day(at)
			items = append(items, &c)
		}
	}
	var out []CaptureItem
	for _, it := range items {
		if it.Kind == KindInitiative {
			continue
		}
		all := f.items[it.ID]
		kind := CaptureContinued
		resolved := false
		if all.own != nil {
			at, ok := timeOf(all.own.ResolvedAt)
			resolved = ok && w.Contains(at)
		}
		switch first, _ := time.Parse(dayLayout, all.First); {
		case resolved || all.Kind == KindDoc || all.Kind == KindBucket && it.Totals.ChangesLanded > 0:
			kind = CaptureFinished
		case w.Contains(first):
			kind = CaptureNew
		}
		_, hasContext := f.context[itemPath(it.ID)]
		out = append(out, CaptureItem{Item: it, Capture: kind, Context: hasContext})
	}
	rank := map[string]int{CaptureFinished: 0, CaptureNew: 1, CaptureContinued: 2}
	slices.SortStableFunc(out, func(a, b CaptureItem) int {
		return cmp.Or(cmp.Compare(rank[a.Capture], rank[b.Capture]), cmp.Compare(b.ActiveDays, a.ActiveDays), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// captureList is capture.txt: capture, id, effort, first..last, title, tab-separated.
func captureList(items []CaptureItem) []byte {
	var b strings.Builder
	for _, it := range items {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s..%s\t%s\n", it.Capture, it.ID, it.Effort, it.First, it.Last, oneLine(it.Title))
	}
	return []byte(b.String())
}
