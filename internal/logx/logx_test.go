package logx

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureStderr runs fn with os.Stderr swapped for a pipe, and returns what fn wrote to it.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = saved }()
	out := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		out <- string(b)
	}()
	fn()
	w.Close()
	return <-out
}

var lineRE = regexp.MustCompile(`^\[(\d{2}:\d{2}:\d{2})\] (.*)$`)

func TestPrintfWritesOneTimestampedLine(t *testing.T) {
	tests := []struct {
		format string
		args   []any
		want   string // the message after the timestamp
	}{
		{"=== stage %s", []any{"commits"}, "=== stage commits"},
		{"%d repos, %d commits", []any{3, 120}, "3 repos, 120 commits"},
		{"no verbs", nil, "no verbs"},
		{"", nil, ""},
	}
	for _, tt := range tests {
		before := time.Now().Format("15:04:05")
		got := captureStderr(t, func() { Printf(tt.format, tt.args...) })
		after := time.Now().Format("15:04:05")

		m := lineRE.FindStringSubmatch(strings.TrimSuffix(got, "\n"))
		if !strings.HasSuffix(got, "\n") || strings.Count(got, "\n") != 1 || m == nil {
			t.Errorf("Printf(%q, %v) wrote %q, want one line \"[15:04:05] message\"", tt.format, tt.args, got)
			continue
		}
		if m[2] != tt.want {
			t.Errorf("Printf(%q, %v) message = %q, want %q", tt.format, tt.args, m[2], tt.want)
		}
		// The stamp is the local time of the call (unless the test ran across midnight).
		if before <= after && (m[1] < before || m[1] > after) {
			t.Errorf("Printf stamp %s, want between %s and %s", m[1], before, after)
		}
	}
}

func TestPrintfConcurrentCallsDoNotInterleave(t *testing.T) {
	const writers, lines = 8, 50
	got := captureStderr(t, func() {
		var wg sync.WaitGroup
		for w := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range lines {
					Printf("writer %d line %d %s", w, i, strings.Repeat("x", 200))
				}
			}()
		}
		wg.Wait()
	})
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		m := lineRE.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("malformed line %q", line)
		}
		seen[strings.TrimSuffix(m[2], " "+strings.Repeat("x", 200))] = true
	}
	for w := range writers {
		for i := range lines {
			if msg := fmt.Sprintf("writer %d line %d", w, i); !seen[msg] {
				t.Errorf("no whole line for %q", msg)
			}
		}
	}
}
