package facts

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// storedPair is the fixture's facts built live, and the same facts written out and read back, with
// the same context.
func storedPair(t *testing.T) (live, stored *Facts, dir string) {
	t.Helper()
	ctx := t.TempDir()
	for name, text := range map[string]string{"framework.md": testFramework, "profile.md": "Cut build time by 40%.\n",
		filepath.Join("evidence", "talk.md"): "---\nwhen: 2024-02-01\nshows: code\n---\nA talk to 30 people.\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(ctx, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ctx, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	in := Inputs{ContextDir: ctx}
	live = build(t, blamed(fixture()), in)
	dir = filepath.Join(t.TempDir(), "facts")
	if err := live.Write(dir); err != nil {
		t.Fatal(err)
	}
	stored, err := LoadStored(dir, in)
	if err != nil {
		t.Fatal(err)
	}
	return live, stored, dir
}

// Stored facts check notes and drafts as the live facts do: the same findings, for every window,
// citation and number, and the same final text. That is what a frozen dataset keeps.
func TestStoredChecksAsLive(t *testing.T) {
	live, stored, _ := storedPair(t)
	if !stored.Stored() || live.Stored() {
		t.Fatal("Stored() is wrong")
	}
	it := live.items["ENG-1"]
	var sections strings.Builder
	for _, s := range NoteSections {
		sections.WriteString("\n## " + s + "\n\nShipped search [[ENG-1]] with [[app#5]] and [[app@c1]], reviewed [[review+app#21]], see [[ENG-2]].\n")
	}
	files := map[string]string{
		"notes/ENG-1.md": "---\nitem: ENG-1\ninputs: " + it.Pack + "\nshows: code, nope\nframework: sha256:old\n---\n" + sections.String(),
		"notes/stale.md": "---\nitem: ENG-1\ninputs: sha256:earlier\n---\n## Summary\n\nIn 12 PRs.\n",
		"notes/none.md":  "---\nitem: ENG-404\ninputs: x\n---\n",
		"all.md": "---\nwindow: all\n---\n" +
			"I landed 3 changes and merged 2 PRs, reviewing 2 PRs by 2 people [[review+app#21]].\n\n" +
			"In 2024 I made 6 commits. In 2024q1, 999 of them.\n\n" +
			"Go: 60 lines across 3 changes. Rust: 60 lines.\n\n" +
			"The API is 63% mine [[own+app+src/]], 125 lines. About 235 lines are still live.\n\n" +
			"See [[ENG-1]], [[OLD-1]], [[app@c1ab]], [[doc+100]], [[adr+app+0001-use-queues]], [[evidence+talk]] and [[nothing]].\n\n" +
			"A talk to 30 people. Build time fell 40%, then 41%. Also ENG-77 and https://tracker.example/browse/ENG-999.\n",
		"window.md": "---\nwindow: 2024-01-01..2024-03-31\n---\nI landed 3 changes, 120 lines still live, and 1 thing.\n\nIn 2024q1 I made 5 commits [[ENG-1]].\n",
		"bad.md":    "---\nwindow: 2024-13-01..2024-12-31\n---\nx\n",
		"plain.md":  "no frontmatter\n",
	}
	for name, text := range files {
		if got, want := stored.Check(name, text), live.Check(name, text); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: stored findings differ:\nstored %v\nlive   %v", name, got, want)
		}
		for _, links := range []bool{false, true} {
			if got, want := stored.Final(text, links), live.Final(text, links); got != want {
				t.Errorf("%s: stored final text (links %v) differs:\n%s\n%s", name, links, got, want)
			}
		}
	}
}

// Stored facts keep a technology's totals for all time and by year only: a draft over any other
// window has none of its numbers in scope, where live facts do.
func TestStoredTechnologyWindows(t *testing.T) {
	_, stored, _ := storedPair(t)
	g := stored.technologies["Go"]
	tests := []struct {
		name string
		w    Window
		ok   bool
	}{
		{"all time", All, true},
		{"a calendar year", yearWindow(2024), true},
		{"a quarter", quarterWindow(2024, 1), false},
		{"another window", mustWindow(t, "2024-01-01..2024-02-15"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := stored.techTotalsIn(g, tt.w); ok != tt.ok {
				t.Errorf("techTotalsIn(%s) ok = %v, want %v", tt.name, ok, tt.ok)
			}
		})
	}
}

// A facts directory is read back only when it is one: with a facts.json of a version this
// footprint reads, and events it can parse.
func TestLoadStoredRefuses(t *testing.T) {
	_, _, dir := storedPair(t)
	edit := func(t *testing.T, name string, change func(string) string) string {
		t.Helper()
		copyDir := filepath.Join(t.TempDir(), "facts")
		if err := os.CopyFS(copyDir, os.DirFS(dir)); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(copyDir, name)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(change(string(data))), 0o644); err != nil {
			t.Fatal(err)
		}
		return copyDir
	}
	tests := []struct {
		name string
		dir  func(t *testing.T) string
		want string
	}{
		{"no facts", func(t *testing.T) string { return t.TempDir() }, "has no facts.json"},
		{"a newer version", func(t *testing.T) string {
			return edit(t, "facts.json", func(s string) string { return strings.Replace(s, `"facts_version": 1`, `"facts_version": 99`, 1) })
		}, "facts version 99"},
		{"a version before events were kept", func(t *testing.T) string {
			return edit(t, "facts.json", func(s string) string { return strings.Replace(s, `"facts_version": 1`, `"facts_version": 0`, 1) })
		}, "facts version 0"},
		{"an event it cannot read", func(t *testing.T) string {
			return edit(t, "events.jsonl", func(s string) string { return `{"at": "yesterday", "kind": "active", "key": "x"}` + "\n" + s })
		}, `"yesterday"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := LoadStored(tt.dir(t), Inputs{}); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("LoadStored() = %v, want an error with %q", err, tt.want)
			}
		})
	}
}

// events.jsonl keeps every event the totals count, in order, with the fields they read.
func TestStoredEvents(t *testing.T) {
	live, stored, _ := storedPair(t)
	if len(stored.events) != len(live.events) {
		t.Fatalf("%d events read back, %d written", len(stored.events), len(live.events))
	}
	for i := range live.events {
		a, b := live.events[i], stored.events[i]
		if !a.at.Equal(b.at) || a.kind != b.kind || a.key != b.key || a.item != b.item || a.repo != b.repo ||
			a.added != b.added || a.deleted != b.deleted || a.living != b.living || a.who != b.who {
			t.Fatalf("event %d: %+v, read back as %+v", i, a, b)
		}
	}
	if !reflect.DeepEqual(totalsOf(stored.events, All), totalsOf(live.events, All)) {
		t.Error("totals from the stored events differ")
	}
	if !reflect.DeepEqual(stored.ownershipSummary(All), live.ownershipSummary(All)) {
		t.Errorf("ownership summary differs:\n%+v\n%+v", stored.ownershipSummary(All), live.ownershipSummary(All))
	}
}
