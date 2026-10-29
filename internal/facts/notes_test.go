package facts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotes(t *testing.T) {
	dir := t.TempDir()
	f := build(t, fixture(), Inputs{})
	pack := f.items["ENG-1"].Pack
	note := func(item, inputs string, sections ...string) string {
		var b strings.Builder
		b.WriteString("---\nitem: " + item + "\ninputs: " + inputs + "\n---\n")
		for _, s := range sections {
			b.WriteString("\n## " + s + "\n\nText.\n")
		}
		return b.String()
	}
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("ENG-1.md", note("ENG-1", pack, NoteSections...))
	write("ENG-10.md", note("ENG-10", "sha256:old", NoteSections...))
	write("ENG-2.md", note("ENG-2", "x", "Summary"))
	write("GONE-1.md", note("GONE-1", "x", NoteSections...))
	f = build(t, fixture(), Inputs{NotesDir: dir})
	states := map[string]string{}
	for _, it := range f.Items {
		states[it.ID] = it.Note
	}
	check(t, "note states", []string{states["ENG-1"], states["ENG-10"], states["ENG-2"], states["doc+100"], states["adr+app+0001-use-queues"]},
		[]string{NoteCurrent, NoteStale, NoteInvalid, NoteMissing, NoteMissing})
	check(t, "orphans", f.orphans, []string{"GONE-1"})
	todo := string(f.todo(All))
	if !strings.Contains(todo, "stale\tENG-10\t") || !strings.Contains(todo, "invalid\tENG-2\t") || !strings.Contains(todo, "orphan\tGONE-1.md") ||
		strings.Contains(todo, "\tENG-1\t") {
		t.Errorf("todo:\n%s", todo)
	}

	// The user's corrections join the pack, and so its hash: the note goes stale.
	ctx := filepath.Join(t.TempDir(), "items")
	if err := os.MkdirAll(ctx, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ctx, "ENG-1.md"), []byte("It cut search time for everyone."), 0o644); err != nil {
		t.Fatal(err)
	}
	g := build(t, fixture(), Inputs{NotesDir: dir, ContextDir: filepath.Dir(ctx)})
	if g.items["ENG-1"].Note != NoteStale || !strings.Contains(string(renderPack(g.items["ENG-1"].pack, g.items["ENG-1"].Pack)), "It cut search time") {
		t.Error("a correction should reach the pack and make the note stale")
	}
}

func TestFrontmatter(t *testing.T) {
	for _, nl := range []string{"\n", "\r\n"} {
		text := strings.ReplaceAll("---\nitem: ENG-1\ninputs: \"sha256:x\"\n---\n\n## Summary\n", "\n", nl)
		front, body, ok := Frontmatter(text)
		check(t, fmt.Sprintf("frontmatter, lines ending %q", nl), []any{ok, front, body},
			[]any{true, map[string]string{"item": "ENG-1", "inputs": "sha256:x"}, nl + "## Summary" + nl})
	}
	for _, text := range []string{"no frontmatter\n", "---\nitem: ENG-1\nnever closed\n"} {
		if _, body, ok := Frontmatter(text); ok || body != text {
			t.Errorf("Frontmatter(%q) = %q, %v; want the text back, false", text, body, ok)
		}
	}
}

// Frontmatter values are trimmed, and unquoted; a value may hold colons; a line without one is
// not a field; a byte order mark before the opening --- is allowed.
func TestFrontmatterValues(t *testing.T) {
	front, body, ok := Frontmatter("\ufeff---\ninputs: sha256:abc\nshows: 'code, senior-coach'\n  item :  ENG-1  \nnot a field\n---\nbody\n")
	check(t, "frontmatter", []any{ok, front, body},
		[]any{true, map[string]string{"inputs": "sha256:abc", "shows": "code, senior-coach", "item": "ENG-1"}, "body\n"})
	front, body, ok = Frontmatter("---\n---")
	check(t, "an empty block, closed at the end of the file", []any{ok, front, body}, []any{true, map[string]string{}, ""})
}

// A note is well formed with frontmatter naming its item and inputs, and every section.
func TestParseNote(t *testing.T) {
	sections := func(names ...string) string {
		var b strings.Builder
		for _, s := range names {
			b.WriteString("\n## " + s + "\n\nText.\n")
		}
		return b.String()
	}
	all := sections(NoteSections...)
	for _, tc := range []struct {
		name, text, wantErr string
	}{
		{"well formed", "---\nitem: ENG-1\ninputs: sha256:x\n---\n" + all, ""},
		{"no frontmatter", all, "no frontmatter: want ---, item: <id>, inputs: <pack hash>, ---"},
		{"no inputs", "---\nitem: ENG-1\n---\n" + all, "frontmatter needs item and inputs"},
		{"no item", "---\ninputs: sha256:x\n---\n" + all, "frontmatter needs item and inputs"},
		{"sections missing", "---\nitem: ENG-1\ninputs: sha256:x\n---\n" + sections("Summary", "Outcome", "Skills", "Open questions"),
			"missing sections: What I did, Evidence"},
		{"a section only in a code block", "---\nitem: ENG-1\ninputs: sha256:x\n---\n" + sections("Summary", "What I did", "Outcome", "Skills", "Evidence") +
			"\n```\n## Open questions\n```\n", "missing sections: Open questions"},
	} {
		n := parseNote("notes/x.md", tc.text)
		check(t, tc.name, n.err, tc.wantErr)
	}
	n := parseNote("notes/ENG-1.md", "---\nitem: ENG-1\ninputs: sha256:x\nshows: senior-coach, code,code\nframework: sha256:fw\n---\n"+all)
	check(t, "its fields", []any{n.path, n.item, n.inputs, n.shows, n.framework, strings.HasPrefix(n.body, "\n## Summary")},
		[]any{"notes/ENG-1.md", "ENG-1", "sha256:x", []string{"code", "senior-coach"}, "sha256:fw", true})
}

// A note's sections are its ## headings, outside code blocks.
func TestHeadingsOf(t *testing.T) {
	body := "# Title\n\n## Summary\n  ##   What I did  \n### Detail\n##Tight\n```\n## In code\n```\n~~~\n## In tildes\n~~~\n## Outcome\n"
	check(t, "headings", headingsOf(body), []string{"Summary", "What I did", "Outcome"})
	check(t, "none", headingsOf("no headings"), []string(nil))
}

// An item needs a note when it is a doc, or of effort M or more.
func TestRequired(t *testing.T) {
	for _, tc := range []struct {
		kind, effort string
		want         bool
	}{
		{KindFeature, "XL", true}, {KindFeature, "L", true}, {KindBucket, "M", true}, {KindInitiative, "S", false},
		{KindFeature, "XS", false}, {KindDoc, "XS", true},
	} {
		if got := required(&Item{Kind: tc.kind, Effort: tc.effort}); got != tc.want {
			t.Errorf("required(%s, %s) = %v, want %v", tc.kind, tc.effort, got, tc.want)
		}
	}
}

// A note's state: current when written from the item's pack as it is, stale from an earlier one,
// invalid when malformed or naming another item, renamed when filed under a key the issue had
// before; with none, missing when required, optional otherwise. Notes naming no item are orphans.
func TestNoteStates(t *testing.T) {
	note := func(item, inputs string) *noteFile { return &noteFile{item: item, inputs: inputs} }
	items := []*Item{
		{ID: "ENG-1", Kind: KindFeature, Effort: "M", Pack: "p1"},
		{ID: "ENG-2", Kind: KindFeature, Effort: "M", Pack: "p2"},
		{ID: "ENG-3", Kind: KindFeature, Effort: "M", Pack: "p3"},
		{ID: "ENG-4", Kind: KindFeature, Effort: "M", Pack: "p4"},
		{ID: "ENG-5", Kind: KindFeature, Effort: "M", Pack: "p5", own: &Issue{PreviousKeys: []string{"OLD-5"}}},
		{ID: "ENG-6", Kind: KindFeature, Effort: "M", Pack: "p6"},
		{ID: "ENG-7", Kind: KindFeature, Effort: "S", Pack: "p7"},
		{ID: "doc+1", Kind: KindDoc, Effort: "XS", Pack: "p8"},
		{ID: "doc+x/y", Kind: KindDoc, Effort: "XS", Pack: "p9"},
	}
	f := &Facts{Items: items, notes: map[string]*noteFile{
		"ENG-1": note("ENG-1", "p1"), "ENG-2": note("ENG-2", "old"), "ENG-3": {item: "ENG-3", inputs: "p3", err: "missing sections: Outcome"},
		"ENG-4": note("ENG-9", "p4"), "OLD-5": note("OLD-5", "p5"), "doc+x%2Fy": note("doc+x/y", "p9"),
		"GONE-2": note("GONE-2", "x"), "GONE-1": note("GONE-1", "x"),
	}}
	orphans := f.noteStates()
	got := map[string]string{}
	for _, it := range items {
		got[it.ID] = it.Note
	}
	check(t, "states", got, map[string]string{"ENG-1": NoteCurrent, "ENG-2": NoteStale, "ENG-3": NoteInvalid, "ENG-4": NoteInvalid,
		"ENG-5": NoteRenamed, "ENG-6": NoteMissing, "ENG-7": NoteOptional, "doc+1": NoteMissing, "doc+x/y": NoteCurrent})
	check(t, "orphans, in order; a renamed note is not one", orphans, []string{"GONE-1", "GONE-2"})
}

// readNotes reads every .md file in the notes directory, by file name; a directory that is not
// there has none.
func TestReadNotes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ENG-1.md", "---\nitem: ENG-1\ninputs: x\n---\n")
	writeFile(t, dir, "README.txt", "not a note")
	writeFile(t, dir, "drafts/ENG-2.md", "---\nitem: ENG-2\ninputs: x\n---\n")
	notes, err := readNotes(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for name, n := range notes {
		names = append(names, name+" "+n.item)
	}
	check(t, "notes", names, []string{"ENG-1 ENG-1"})
	for _, d := range []string{"", filepath.Join(dir, "missing")} {
		if notes, err := readNotes(d); err != nil || len(notes) != 0 {
			t.Errorf("readNotes(%q) = %v, %v; want none", d, notes, err)
		}
	}
	if _, err := readNotes(filepath.Join(dir, "ENG-1.md")); err == nil {
		t.Error("readNotes(a file) = nil error, want one")
	}
}

// The user's context: every .md file's text, for the numbers they stated, and items/<id>.md as
// their words on that item.
func TestReadContext(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "profile.md", "I lead 4 people.")
	writeFile(t, dir, "items/ENG-1.md", "\n  It cut search time.  \n")
	writeFile(t, dir, "items/deeper/ENG-2.md", "not an item's")
	writeFile(t, dir, "items/notes.txt", "not markdown")
	items, all, err := readContext(dir)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "items", items, map[string]string{"ENG-1": "It cut search time."})
	check(t, "every file", len(all), 3)
	if !strings.Contains(fmt.Sprint(all), "I lead 4 people.") {
		t.Errorf("readContext: all = %q, want profile.md's text", all)
	}
	for _, d := range []string{"", filepath.Join(dir, "missing")} {
		if items, all, err := readContext(d); err != nil || len(items) != 0 || len(all) != 0 {
			t.Errorf("readContext(%q) = %v, %v, %v; want nothing", d, items, all, err)
		}
	}
}
