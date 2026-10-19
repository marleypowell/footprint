package scrape

import (
	"os"
	"sort"
	"strings"
	"time"

	"github.com/marleypowell/footprint/internal/logx"
)

// logf prints a timestamped progress line to stderr.
func logf(format string, args ...any) { logx.Printf(format, args...) }

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

func nowISO() string { return time.Now().UTC().Format(isoLayout) }

// parseTime reads the ISO 8601 timestamps git (%aI) and code hosts emit; unparseable input is zero.
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
