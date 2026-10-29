package collect

import (
	"reflect"
	"strings"
	"testing"
)

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// parseBlame reads `git blame --incremental`: a header per run of lines, with the commit's author
// the first time the commit appears. Runs of one commit add up.
func TestParseBlame(t *testing.T) {
	out := shaA + " 1 1 3\nauthor Jane Doe\nauthor-mail <jane@example.com>\nauthor-time 1700000000\nauthor-tz +0100\n" +
		"summary first\nfilename a.go\n" +
		shaB + " 4 4 1\nauthor Sam\nauthor-mail <sam@example.com>\nauthor-time 1710000000\nfilename a.go\n" +
		shaA + " 5 5 2\nprevious x a.go\nfilename a.go\n"
	got, err := parseBlame([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{shaA: 5, shaB: 1}; !reflect.DeepEqual(got.Lines, want) {
		t.Errorf("lines = %v, want %v", got.Lines, want)
	}
	if want := (blameAuthor{Name: "Jane Doe", Email: "jane@example.com", At: 1700000000}); got.Authors[shaA] != want {
		t.Errorf("author of A = %+v, want %+v", got.Authors[shaA], want)
	}
	if got.Authors[shaB].Email != "sam@example.com" {
		t.Errorf("author of B = %+v", got.Authors[shaB])
	}
}

func TestParseBlameEdges(t *testing.T) {
	tests := []struct {
		name, out, wantErr string
		wantLines          int
	}{
		{"an empty file has no blame", "", "", 0},
		{"a commit without its author is an error", shaA + " 1 1 2\nfilename a.go\n", "without its author", 0},
		{"text before any header is ignored", "warning: something\n" + shaA + " 1 1 1\nauthor A\nauthor-mail <a@x>\nauthor-time 1\n", "", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseBlame([]byte(tt.out))
			if (tt.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("parseBlame error = %v, want one with %q", err, tt.wantErr)
			}
			total := 0
			for _, n := range got.Lines {
				total += n
			}
			if err == nil && total != tt.wantLines {
				t.Errorf("lines = %d, want %d", total, tt.wantLines)
			}
		})
	}
}

// An ignore-revs file lists commits one per line, with comments; only full commit names count.
func TestParseIgnoreRevs(t *testing.T) {
	text := "# formatting\n" + shaB + " # run prettier\n\n" + shaA + "\nnot-a-sha\n" + shaA + "\nabc123\n"
	if got, want := parseIgnoreRevs(text), []string{shaA, shaB}; !reflect.DeepEqual(got, want) {
		t.Errorf("parseIgnoreRevs() = %v, want %v, sorted and without repeats", got, want)
	}
	if got := parseIgnoreRevs(""); len(got) != 0 {
		t.Errorf("parseIgnoreRevs(\"\") = %v", got)
	}
}

// A text's lines are those ending in a newline, and a last one without; a NUL in its first 8000
// bytes makes it binary, as git decides.
func TestCountLines(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		wantLines  int
		wantBinary bool
	}{
		{"empty", "", 0, false},
		{"one line with a newline", "a\n", 1, false},
		{"one line without", "a", 1, false},
		{"three lines, the last unfinished", "a\nb\nc", 3, false},
		{"blank lines count", "\n\n", 2, false},
		{"a NUL makes it binary", "a\x00b\n", 0, true},
		{"a NUL after 8000 bytes does not", strings.Repeat("x", 8000) + "\x00\n", 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines, binary := countLines([]byte(tt.text))
			if lines != tt.wantLines || binary != tt.wantBinary {
				t.Errorf("countLines(%q) = %d, %v; want %d, %v", tt.name, lines, binary, tt.wantLines, tt.wantBinary)
			}
		})
	}
}

// The cache key changes with anything that changes the blame: the file, its content, the flags and
// the commits passed over.
func TestBlameKey(t *testing.T) {
	base := blameKey("a.go", "blob1", "-w -M", digest([]string{shaA}))
	for name, other := range map[string]string{
		"path":    blameKey("b.go", "blob1", "-w -M", digest([]string{shaA})),
		"blob":    blameKey("a.go", "blob2", "-w -M", digest([]string{shaA})),
		"flags":   blameKey("a.go", "blob1", "-w -M -C", digest([]string{shaA})),
		"ignored": blameKey("a.go", "blob1", "-w -M", digest([]string{shaA, shaB})),
	} {
		if other == base {
			t.Errorf("a different %s has the same cache key", name)
		}
	}
	if digest([]string{"ab", "c"}) == digest([]string{"a", "bc"}) {
		t.Error("digest does not tell where one part ends")
	}
}

func TestBlameArgs(t *testing.T) {
	tests := []struct {
		name       string
		ignoreFile string
		want       []string
	}{
		{"without ignored revisions", "", []string{"blame", "--incremental", "-w", "-M", "refs/heads/main", "--", "a.go"}},
		{"with them", "cache/x.ignore-revs", []string{"blame", "--incremental", "-w", "-M", "--ignore-revs-file", "cache/x.ignore-revs", "refs/heads/main", "--", "a.go"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := blameArgs([]string{"-w", "-M"}, tt.ignoreFile, "refs/heads/main", "a.go"); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("blameArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}
