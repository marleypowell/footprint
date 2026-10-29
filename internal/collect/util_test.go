package collect

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// captureStderr returns what fn logs (logf writes to stderr).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestOneLine(t *testing.T) {
	for in, want := range map[string]string{
		"fatal: repository not found\n":                           "fatal: repository not found",
		"<html>\n  <body>\n\t502 Bad Gateway\n  </body>\n</html>": "<html> <body> 502 Bad Gateway </body> </html>",
		"  \n ": "",
	} {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		s    string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exact", 5, "exact"},
		{"longer", 4, "long"},
	}
	for _, tt := range tests {
		if got := truncate(tt.s, tt.n); got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
		}
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{file: true, dir: true, filepath.Join(dir, "missing"): false} {
		if got := fileExists(path); got != want {
			t.Errorf("fileExists(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestParseTime(t *testing.T) {
	tests := []struct {
		ts   string
		want time.Time
	}{
		{"2024-03-01T10:00:00Z", time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)},
		{"2024-03-01T12:00:00+02:00", time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)}, // git's %aI keeps the author's offset
		{"2024-03-01", time.Time{}}, // not RFC 3339: zero
		{"", time.Time{}},
	}
	for _, tt := range tests {
		if got := parseTime(tt.ts); !got.Equal(tt.want) {
			t.Errorf("parseTime(%q) = %v, want %v", tt.ts, got, tt.want)
		}
	}
}

// isoUTC writes UTC with "+00:00", as the first datasets did, rather than Go's "Z".
func TestIsoUTC(t *testing.T) {
	tests := []struct {
		ts   string
		want *string
	}{
		{"2024-03-01T10:00:00Z", ptr("2024-03-01T10:00:00+00:00")},
		{"2024-03-01T01:30:00+02:00", ptr("2024-02-29T23:30:00+00:00")},
		{"", nil},
	}
	for _, tt := range tests {
		if got := isoUTC(tt.ts); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("isoUTC(%q) = %v, want %v", tt.ts, deref(got), deref(tt.want))
		}
	}
	if _, err := time.Parse(time.RFC3339, nowISO()); err != nil {
		t.Errorf("nowISO() = %q is not RFC 3339: %v", nowISO(), err)
	}
}

func TestSortedKeys(t *testing.T) {
	if got := sortedKeys(map[string]int{"web": 1, "API": 2, "api": 3}); !reflect.DeepEqual(got, []string{"API", "api", "web"}) {
		t.Errorf("sortedKeys = %q, want byte order", got)
	}
	if got := sortedKeys(map[string]bool{}); got == nil || len(got) != 0 {
		t.Errorf("sortedKeys(empty) = %#v, want an empty, non-nil slice", got)
	}
}

func TestOrEmpty(t *testing.T) {
	if got := orEmpty([]int(nil)); got == nil || len(got) != 0 {
		t.Errorf("orEmpty(nil) = %#v, want []int{}", got)
	}
	if got := orEmpty([]int{1, 2}); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("orEmpty([1 2]) = %v", got)
	}
}

func TestDeref(t *testing.T) {
	if got := deref((*string)(nil)); got != "" {
		t.Errorf("deref(nil) = %q, want the zero value", got)
	}
	if got := deref(ptr(42)); got != 42 {
		t.Errorf("deref(ptr(42)) = %d", got)
	}
}
