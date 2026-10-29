package facts

import (
	"encoding/json"
	"strings"
	"testing"
)

// Overviews label every total by its name in facts.json: a total without a label, or a label
// naming no total, would go missing from them silently.
func TestMetricsNameEveryTotal(t *testing.T) {
	names := Totals{}.values()
	labelled := map[string]bool{}
	for _, m := range metrics {
		if _, ok := names[m.name]; !ok {
			t.Errorf("metric %q is not a total", m.name)
		}
		labelled[m.name] = true
	}
	for name := range names {
		if !labelled[name] {
			t.Errorf("total %q has no metric", name)
		}
	}
	for _, c := range yearColumns {
		if _, ok := names[c]; !ok {
			t.Errorf("year column %q is not a total", c)
		}
	}
	b, _ := json.Marshal(Totals{})
	check(t, "a total per field", len(names), strings.Count(string(b), ":"))
	check(t, "values", Totals{PRsMerged: 3, ActiveDays: 9}.values()["prs_merged"], 3)
}

func TestCellAndDemote(t *testing.T) {
	check(t, "cell", cell("a | b\nc  d"), `a \| b c d`)
	check(t, "demote", demote("# Top\n## Summary\ntext with # in it\n  ## indented\n"), "### Top\n#### Summary\ntext with # in it\n  ## indented\n")
}

// A doc is in a window when the user saved a version of it then.
func TestDocIn(t *testing.T) {
	d := &Doc{evs: []event{{at: mustTime(t, "2024-01-05"), kind: evPageCreated}, {at: mustTime(t, "2024-02-05"), kind: evPageVersion},
		{at: mustTime(t, "2024-03-05"), kind: evActive}}}
	f := &Facts{}
	for _, tc := range []struct {
		from, to string
		want     bool
	}{{"2024-02-01", "2024-02-29", true}, {"2024-01-01", "2024-01-31", false}, {"2024-03-01", "2024-03-31", false}} {
		w, _ := NewWindow(tc.from, tc.to)
		check(t, "docIn("+w.String()+")", f.docIn(d, w), tc.want)
	}
	check(t, "an ADR's commits", f.docIn(&Doc{evs: []event{{at: mustTime(t, "2024-01-05"), kind: evADRVersion}}}, All), true)
}

func TestOverview(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	out := string(f.overview(All))
	for _, want := range []string{
		"# Your work: all time\n",
		"- Counted: 2024-01-02..2024-05-01, from fakehost (code host), fakecatalog (catalog), faketracker (issue tracker), fakewiki (wiki).\n",
		"- PR records start on 2024-01-03.",
		"generated files (2 file changes, +5100 -110) and single file changes of more\n  than 5000 lines (1, +6000 -0), all time.",
		"| PRs merged | 2 | `prs_merged` |\n",
		"## By year\n", "| 2024 | 3 | 2 | 1 | 6 | 2 | 2 | 2 |",
		"| Go | language | ✓ | 2024-01-02..2024-02-01 | 4 | 3 | 60 | 1 |\n",
		"| 2024q1 | Search Squad (2) | ENG Engineering (3) | Platform (1) |\n",
		"| ENG-10 | Search | 2024-01-02..2024-03-02 | 1 | 5 | missing |\n",
		"### M\n\n- ENG-1 · Fast search · feature, 2024-01-02..2024-03-02, 5 days · in ENG-10 · app · note: missing\n",
		"And 5 smaller items (S and XS), in items.jsonl.\n",
		"| app (you started it) |  | 2024-01-02..2024-05-01 | 3 | 60 | .go |\n",
		"- doc+100 · Search design · Engineering, created 2024-01-02, 100% of versions yours\n",
		"- adr+app+0001-use-queues · Use queues · ADR in app, created 2024-01-02, 66% of commits yours\n",
		"4 items here need a note (effort M or more, and docs): 0 current, 0 stale, 4 missing,\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("overview has no %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"## Framework coverage", "### XL", "### L\n"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("overview has %q:\n%s", unwanted, out)
		}
	}

	w, _ := NewWindow("2024-04-01", "2024-06-30")
	out = string(f.overview(w))
	for _, want := range []string{"# Your work: 2024-04-01..2024-06-30\n", "## By quarter\n\n| quarter |", "| 2024q2 |",
		"| app (you started it) |  | 2024-04-01..2024-05-01 | 0 | 0 | .go |\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("a window's overview has no %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"## Docs", "## Team signals", "## Initiatives", "## Technologies"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a window's overview has %q, with nothing of it in the window:\n%s", unwanted, out)
		}
	}
}

// The catalogue is every item, oldest first under its year, with its note (headings moved under
// the item's) or, for an item that needs one, a mark that it has none.
func TestCatalogue(t *testing.T) {
	dir := t.TempDir()
	f := build(t, fixture(), Inputs{})
	var note strings.Builder
	note.WriteString("---\nitem: ENG-1\ninputs: " + f.items["ENG-1"].Pack + "\n---\n")
	for _, s := range NoteSections {
		note.WriteString("\n## " + s + "\n\nAbout " + s + ".\n")
	}
	writeFile(t, dir, "ENG-1.md", note.String())
	writeFile(t, dir, "ENG-10.md", "no frontmatter: invalid, so not shown\n")
	f = build(t, fixture(), Inputs{NotesDir: dir})
	out := string(f.catalogue())
	for _, want := range []string{
		"# Catalogue of work, 2024-01-02..2024-05-01\n", "\n## 2024\n",
		"### ENG-1 · Fast search\n\n2024-01-02..2024-03-02 · feature (Story) · in ENG-10 Search · repos: app · effort M, 5 active days\n" +
			"1 PRs merged, 4 commits, 2 issues done, 1 of 2 sub-tasks done by you.\n\n#### Summary\n\nAbout Summary.\n",
		"### ENG-10 · Search\n\n2024-01-02..2024-03-02 · initiative (Epic) · repos: app · effort M, 5 active days\n1 PRs merged, 4 commits, 2 issues done.\n\n(No note yet.)\n",
		"### repo+app+2024q1 · app: work naming no issue, 2024Q1\n\n2024-02-02..2024-03-01 · bucket · repos: app · effort XS, 1 active days\n1 commits.\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("catalogue has no %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "no frontmatter") || strings.Contains(out, "2024Q1\n\n(No note yet.)") {
		t.Errorf("catalogue shows an invalid note, or asks for one an item does not need:\n%s", out)
	}
}

// todo.txt lists the notes to write or fix: required items whose note is not current, and any
// invalid note, biggest first, then the notes that name no item.
func TestTodo(t *testing.T) {
	f := &Facts{orphans: []string{"GONE-1"}, Items: []*Item{
		{ID: "ENG-1", Kind: KindFeature, Effort: "M", Note: NoteCurrent, First: "2024-01-01"},
		{ID: "ENG-2", Kind: KindFeature, Effort: "M", Note: NoteMissing, First: "2024-02-01", Last: "2024-02-03", Title: "Second\nissue"},
		{ID: "ENG-3", Kind: KindFeature, Effort: "XL", Note: NoteStale, First: "2024-03-01", Last: "2024-09-01", Title: "Big"},
		{ID: "ENG-4", Kind: KindFeature, Effort: "XS", Note: NoteInvalid, First: "2024-01-01", Last: "2024-01-01", Title: "Small, broken"},
		{ID: "ENG-5", Kind: KindFeature, Effort: "XS", Note: NoteOptional, First: "2024-01-01"},
		{ID: "ENG-6", Kind: KindFeature, Effort: "M", Note: NoteRenamed, First: "2024-01-15", Last: "2024-01-20", Title: "Renamed"},
	}}
	check(t, "todo.txt", string(f.todo(All)), "stale\tENG-3\tXL\t2024-03-01..2024-09-01\tBig\n"+
		"renamed\tENG-6\tM\t2024-01-15..2024-01-20\tRenamed\n"+
		"missing\tENG-2\tM\t2024-02-01..2024-02-03\tSecond issue\n"+
		"invalid\tENG-4\tXS\t2024-01-01..2024-01-01\tSmall, broken\n"+
		"orphan\tGONE-1.md\n")
	f.orphans, f.Items = nil, f.Items[:1]
	check(t, "nothing to do", string(f.todo(All)), "")
}
