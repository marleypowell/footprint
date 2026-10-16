package scrape

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

var logMu sync.Mutex

// logf prints a timestamped progress line to stderr.
func logf(format string, args ...any) {
	logMu.Lock()
	defer logMu.Unlock()
	fmt.Fprintf(os.Stderr, "[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// forEach calls fn for every item with at most n calls in flight. The first error cancels the
// context passed to the calls still running, stops new ones starting, and is returned.
func forEach[T any](ctx context.Context, n int, items []T, fn func(ctx context.Context, i int, item T) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	sem := make(chan struct{}, max(n, 1))
	var wg sync.WaitGroup
	for i, item := range items {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := fn(ctx, i, item); err != nil {
				cancel(err)
			}
		}()
	}
	wg.Wait()
	return context.Cause(ctx)
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// oneLine collapses whitespace runs (e.g. in an HTML error page) so a message fits one log line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// isoLayout matches Python's datetime.isoformat() for UTC ("+00:00", not "Z"), which the first
// datasets were written with.
const isoLayout = "2006-01-02T15:04:05-07:00"

const dateLayout = "2006-01-02"

func nowISO() string { return time.Now().UTC().Format(isoLayout) }

// parseTime reads the ISO 8601 timestamps git (%aI) and GitHub emit; unparseable input is zero.
func parseTime(ts string) time.Time {
	t, _ := time.Parse(time.RFC3339, ts)
	return t
}

func isoUTC(ts string) *string {
	if ts == "" {
		return nil
	}
	s := parseTime(ts).UTC().Format(isoLayout)
	return &s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// orEmpty turns a nil slice into an empty one, so JSON gets [] rather than null.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func ptr[T any](v T) *T { return &v }

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimRight(line, "\r")
}

// tomorrow is the exclusive-enough upper bound for GitHub search date ranges.
func tomorrow() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
}

// splitDays halves a date range for search slicing.
func splitDays(lo, hi time.Time) (time.Time, time.Time) {
	days := int(hi.Sub(lo).Hours() / 24)
	mid := lo.AddDate(0, 0, days/2)
	return mid, mid.AddDate(0, 0, 1)
}
