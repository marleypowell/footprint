package facts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFramework(t *testing.T) {
	fw := parseFramework(testFramework)
	var got []string
	for _, c := range fw.criteria {
		got = append(got, fmt.Sprintf("%s|%s|%s|%v", c.ID, c.Level, c.Text, c.Reviews))
	}
	check(t, "criteria", got, []string{
		"code||Code: Write clean code.|false",
		"quality||Quality: Drive best practices via code reviews.|true",
		"senior-coach|senior|Coach on technical practices.|false",
		"senior-process|senior|Drive process improvements.|false",
		"lead-streams|lead|Lead work-streams.|false",
		"testing@applying|applying|Testing: Writes tests|false",
		"testing@leading|leading|Testing: Drives the test strategy|false",
	})
	check(t, "problems", fw.problems, []string{"line 24: code is marked twice; the first counts",
		"line 30: {level=oops} is not on a heading or a table's header row"})
	check(t, "Windows line endings, same hash", parseFramework(strings.ReplaceAll(testFramework, "\n", "\r\n")).hash, fw.hash)
	if parseFramework(strings.Replace(testFramework, "clean code", "clean, tested code", 1)).hash == fw.hash {
		t.Error("rewording a criterion keeps the hash")
	}
	f := build(t, fixture(), Inputs{})
	f.framework = parseFramework("# Framework\n")
	if s := f.frameworkSummary(All); s.Untagged != 0 || s.Criteria != 0 || strings.Contains(string(f.criteriaDoc(All)), "Provisional") {
		t.Errorf("with nothing marked, no note is untagged: %+v", s)
	}
	check(t, "nothing marked", parseFramework("# Framework\n\n- Code\n").problems,
		[]string{"no criteria are marked: add {#<id>} to the competencies and expectations to map evidence to"})
}

func TestCoverage(t *testing.T) {
	dir := t.TempDir()
	notes, ctx := filepath.Join(dir, "notes"), filepath.Join(dir, "context")
	put := func(p, text string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(filepath.Join(ctx, "framework.md"), testFramework)
	put(filepath.Join(ctx, "evidence", "mentoring.md"), "---\nwhen: 2024-06-01\nshows: senior-coach\n---\nI mentored a new starter for 3 months.\n")
	put(filepath.Join(ctx, "evidence", "undated.md"), "---\nshows: code\n---\nno date\n")
	first := build(t, fixture(), Inputs{})
	hash := parseFramework(testFramework).hash
	var sections strings.Builder
	for _, s := range NoteSections {
		sections.WriteString("\n## " + s + "\n\nWork.\n")
	}
	note := func(id, front string) {
		put(filepath.Join(notes, itemPath(id)+".md"), "---\nitem: "+id+"\ninputs: "+first.items[id].Pack+"\n"+front+"---\n"+sections.String())
	}
	note("ENG-1", "shows: code, senior-coach\nframework: "+hash+"\n")
	note("ENG-2", "shows: code\nframework: "+hash+"\n")
	note("repo+app+2024q2", "") // current, and not tagged yet
	f := build(t, fixture(), Inputs{NotesDir: notes, ContextDir: ctx})

	status := map[string]string{}
	for _, cc := range f.coverage(All) {
		status[cc.ID] = fmt.Sprintf("%s %v %v %d %d", cc.Status, cc.Items, cc.Evidence, cc.Reviews, cc.Quarters)
	}
	check(t, "coverage", status, map[string]string{
		"code":             "thin [ENG-1 ENG-2] [] 0 1",
		"quality":          "thin [] [] 2 1",
		"senior-coach":     "covered [ENG-1] [evidence+mentoring] 0 2",
		"senior-process":   "none [] [] 0 0",
		"lead-streams":     "none [] [] 0 0",
		"testing@applying": "none [] [] 0 0",
		"testing@leading":  "none [] [] 0 0",
	})
	w, _ := NewWindow("2024-01-01", "2024-03-31")
	for _, cc := range f.coverage(w) {
		if cc.ID == "senior-coach" {
			check(t, "in a window, evidence outside it does not count", []any{cc.Status, cc.Evidence}, []any{CoverageThin, []string{}})
		}
	}
	s := f.Summary(All).Framework
	check(t, "summary", []any{s.Criteria, s.Tagged, s.Untagged, s.ByStatus, len(s.Problems)},
		[]any{7, 2, 1, map[string]int{CoverageNone: 4, CoverageThin: 2, CoverageCovered: 1}, 3})
	check(t, "an item's tags", f.items["ENG-1"].Shows, []string{"code", "senior-coach"})
	check(t, "tags.txt", string(f.tagsTodo()), "untagged\trepo+app+2024q2\tS\t2024-04-01..2024-05-01\tapp: work naming no issue, 2024Q2\n")
	doc := string(f.criteriaDoc(All))
	for _, want := range []string{"## Any level\n", "## senior\n", "| none | `senior-process` Drive process improvements. |  | 0 |",
		"| covered | `senior-coach` Coach on technical practices. | ENG-1, evidence+mentoring | 2 |", "| thin | `quality`", "2 reviews",
		"**Provisional:** 1 current notes"} {
		if !strings.Contains(doc, want) {
			t.Errorf("criteria.md has no %q:\n%s", want, doc)
		}
	}

	var findings []string
	for _, fd := range f.Check("notes/ENG-1.md", "---\nitem: ENG-1\ninputs: "+f.items["ENG-1"].Pack+"\nshows: code, mentoring\nframework: sha256:old\n---\n"+sections.String()) {
		findings = append(findings, fd.String())
	}
	check(t, "a note's tags checked", findings, []string{
		"6: warning: tagged against an earlier framework: check its shows: against criteria.md, then set framework: " + hash,
		"6: error: shows: mentoring is not a criterion of context/framework.md (criteria.md lists them)",
	})
	if fd := f.Check("draft.md", "---\nwindow: all\n---\nI mentored a starter for 3 months [[evidence+mentoring]].\n"); len(fd) > 0 {
		t.Errorf("an evidence file is citable, with its numbers in scope: %v", fd)
	}
	if fd := build(t, fixture(), Inputs{NotesDir: notes}).Check("notes/ENG-1.md", "---\nitem: ENG-1\ninputs: x\nshows: code\n---\n"+sections.String()); len(fd) < 2 ||
		!strings.Contains(fd[1].String(), "marks none") {
		t.Errorf("tags without a framework: %v", fd)
	}
}

// A level holds from its heading to the next heading as high or higher; a criterion is a marked
// heading, list item or table row, of the level it sits under. A table without level columns makes
// one criterion of a row, its cells joined.
func TestParseFrameworkScopes(t *testing.T) {
	fw := parseFramework("## Level one {level=one}\n\n- **A.** {#a}\n\n### Under it\n\n- B. {#b}\n\n## Next\n\n- C. {#c}\n\n" +
		"# Top {#top}\n\n### Deep {level=deep}\n\n#### Deeper {#deeper reviews}\n\n## Plain table\n\n| Name | What |\n|---|---|\n" +
		"| Ship {#ship} | **Delivers** work |\n| Untagged | x |\n\n1. Numbered {#numbered}\n")
	var got []string
	for _, c := range fw.criteria {
		got = append(got, fmt.Sprintf("%s|%s|%s|%v", c.ID, c.Level, c.Text, c.Reviews))
	}
	check(t, "criteria", got, []string{"a|one|A.|false", "b|one|B.|false", "c||C.|false", "top||Top|false", "deeper|deep|Deeper|true",
		"ship||Ship · Delivers work|false", "numbered||Numbered|false"})
	check(t, "problems", fw.problems, []string(nil))
	check(t, "by id", fw.byID["deeper"].Level, "deep")
	if parseFramework(strings.Replace(testFramework, "{#code}", "{#code reviews}", 1)).hash == parseFramework(testFramework).hash {
		t.Error("marking a criterion for reviews keeps the hash")
	}
}

func TestPlainText(t *testing.T) {
	for in, want := range map[string]string{
		"- **Code {#code}:** Write clean code.": "Code: Write clean code.",
		"1. Ship it {#ship}":                    "Ship it",
		"### Heading {level=x}":                 "Heading",
		"  spaced   out  ":                      "spaced out",
		"__under__ and **bold**":                "under and bold",
		"{ #spaced }":                           "",
	} {
		if got := plainText(in); got != want {
			t.Errorf("plainText(%q) = %q, want %q", in, got, want)
		}
	}
	check(t, "tableCells", [][]string{tableCells("| a | b |"), tableCells("a | b"), tableCells("  | a |  |  ")},
		[][]string{{"a", "b"}, {"a", "b"}, {"a", ""}})
}

// tags are a comma-separated list, each once, in order.
func TestTagList(t *testing.T) {
	check(t, "tagList", tagList("senior-coach, code,code , "), []string{"code", "senior-coach"})
	check(t, "tagList of nothing", tagList(""), []string{})
}

func TestReadFramework(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"", dir} {
		if fw, err := readFramework(d); fw != nil || err != nil {
			t.Errorf("readFramework(%q) = %v, %v; want nil, nil", d, fw, err)
		}
	}
	writeFile(t, dir, "framework.md", testFramework)
	if fw, err := readFramework(dir); err != nil || fw == nil || len(fw.criteria) != 7 {
		t.Errorf("readFramework = %v, %v; want the framework", fw, err)
	}
	bad := t.TempDir()
	if err := os.Mkdir(filepath.Join(bad, "framework.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := readFramework(bad); err == nil {
		t.Error("readFramework of an unreadable framework.md: nil error")
	}
}

// Evidence files need a date and the criteria they show; the others are problems to report.
func TestReadEvidence(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "evidence/talk.md", "---\nwhen: 2024-02-01\nshows: code, quality\n---\nA talk.\n")
	writeFile(t, dir, "evidence/award.md", "---\nwhen: 2023-11-30\nshows: code\n---\n")
	writeFile(t, dir, "evidence/undated.md", "---\nshows: code\n---\n")
	writeFile(t, dir, "evidence/vague.md", "---\nwhen: last spring\n---\n")
	writeFile(t, dir, "evidence/plain.md", "No frontmatter.\n")
	writeFile(t, dir, "evidence/notes.txt", "not evidence")
	writeFile(t, dir, "evidence/old/x.md", "---\nwhen: 2020-01-01\n---\n")
	ev, problems, err := readEvidence(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range ev {
		got = append(got, fmt.Sprintf("%s %s %v %s", e.Ref, e.When, e.Shows, day(e.at)))
	}
	check(t, "evidence, by ref", got, []string{"evidence+award 2023-11-30 [code] 2023-11-30", "evidence+talk 2024-02-01 [code quality] 2024-02-01"})
	check(t, "problems", problems, []string{
		"evidence/plain.md needs frontmatter with when: YYYY-MM-DD and shows: <criteria>",
		"evidence/undated.md needs frontmatter with when: YYYY-MM-DD and shows: <criteria>",
		"evidence/vague.md needs frontmatter with when: YYYY-MM-DD and shows: <criteria>",
	})
	for _, d := range []string{"", t.TempDir()} {
		if ev, problems, err := readEvidence(d); ev != nil || problems != nil || err != nil {
			t.Errorf("readEvidence(%q) = %v, %v, %v; want nothing", d, ev, problems, err)
		}
	}
}

// coverFacts has a framework of five criteria, the user's evidence files and reviews, and items
// whose notes are tagged.
func coverFacts(t *testing.T) *Facts {
	fw := parseFramework("- A {#a}\n- B {#b}\n- C {#c}\n- R {#r reviews}\n- N {#n}\n")
	ev := func(ref, when string, shows ...string) *Evidence {
		return &Evidence{Ref: "evidence+" + ref, Shows: shows, When: when, at: mustTime(t, when)}
	}
	f := &Facts{framework: fw, notes: map[string]*noteFile{},
		evidenceFiles: []*Evidence{ev("one", "2024-01-10", "a", "b"), ev("two", "2024-02-10", "b"), ev("three", "2024-01-10", "c"), ev("four", "2024-05-10", "c")},
		Reviews:       []*Review{{Ref: "review+app#1", First: "2024-01-05", Words: 3}, {Ref: "review+app#2", First: "2024-08-05", Words: 9}, {Ref: "review+app#3", First: "2024-09-05"}},
	}
	item := func(id, note, hash, effort, first string, shows ...string) {
		it := &Item{ID: id, Note: note, Effort: effort, First: first, Last: first, Title: "Title of " + id, days: []string{first}}
		f.Items = append(f.Items, it)
		f.notes[itemPath(id)] = &noteFile{item: id, framework: hash, shows: shows}
		it.events = []event{{at: mustTime(t, first), kind: evActive}}
	}
	item("ENG-1", NoteCurrent, fw.hash, "M", "2024-04-01", "a")
	item("ENG-2", NoteStale, fw.hash, "M", "2024-04-01", "n")        // not current: its tags do not count
	item("ENG-3", NoteCurrent, "sha256:old", "L", "2024-03-01", "n") // tagged against an earlier framework
	item("ENG-4", NoteCurrent, "", "M", "2024-02-01")
	item("ENG-5", NoteCurrent, "", "M", "2024-01-01")
	return f
}

// A criterion is covered by 2 pieces of evidence or more, across 2 quarters or more; thin with
// fewer, or all in one quarter. Its evidence is the user's current notes tagged with it, their
// evidence files, and (for a criterion marked reviews) their reviews with words in them.
func TestCoverageStatus(t *testing.T) {
	f := coverFacts(t)
	status := func(w Window) map[string]string {
		out := map[string]string{}
		for _, cc := range f.coverage(w) {
			out[cc.ID] = fmt.Sprintf("%s %v %v %d %d", cc.Status, cc.Items, cc.Evidence, cc.Reviews, cc.Quarters)
		}
		return out
	}
	check(t, "all time", status(All), map[string]string{
		"a": "covered [ENG-1] [evidence+one] 0 2",
		"b": "thin [] [evidence+one evidence+two] 0 1",
		"c": "covered [] [evidence+three evidence+four] 0 2",
		"r": "covered [] [] 2 2",
		"n": "none [] [] 0 0",
	})
	q1, _ := NewWindow("2024-01-01", "2024-03-31")
	check(t, "in a window", status(q1)["c"], "thin [] [evidence+three] 0 1")
	s := f.frameworkSummary(All)
	check(t, "summary", []any{s.Criteria, s.Tagged, s.Untagged, s.ByStatus, s.Problems},
		[]any{5, 1, 3, map[string]int{CoverageNone: 1, CoverageThin: 1, CoverageCovered: 3}, []string{}})
	check(t, "tags.txt: current notes not tagged against this framework, biggest first",
		string(f.tagsTodo()), "untagged\tENG-3\tL\t2024-03-01..2024-03-01\tTitle of ENG-3\n"+
			"untagged\tENG-5\tM\t2024-01-01..2024-01-01\tTitle of ENG-5\n"+
			"untagged\tENG-4\tM\t2024-02-01..2024-02-01\tTitle of ENG-4\n")
	f.framework = nil
	check(t, "without a framework", []any{f.coverage(All), f.frameworkSummary(All), f.tagsTodo()},
		[]any{[]CriterionCoverage(nil), (*FrameworkSummary)(nil), []byte{}})
	if _, ok := f.tagged(f.Items[0]); ok {
		t.Error("an item is tagged without a framework")
	}
}

// criteria.md lists each level's criteria, gaps first (ten items in one quarter are still thin), with
// at most 8 items each.
func TestCriteriaDoc(t *testing.T) {
	f := coverFacts(t)
	for i := range 10 {
		id := "ENG-" + itoa(10+i)
		f.Items = append(f.Items, &Item{ID: id, Note: NoteCurrent, Effort: "M", days: []string{"2024-06-01"}})
		f.notes[id] = &noteFile{item: id, framework: f.framework.hash, shows: []string{"n"}}
	}
	f.evidenceProblems = []string{"evidence/x.md needs frontmatter"}
	doc := string(f.criteriaDoc(All))
	for _, want := range []string{
		"# Framework coverage: all time\n", "5 criteria: 0 with no evidence, 2 thin, 3 covered.",
		"**Provisional:** 3 current notes are not yet tagged", "- Problem: evidence/x.md needs frontmatter\n",
		"| thin | `b` B | evidence+one, evidence+two | 1 |\n",
		"| thin | `n` N | ENG-10, ENG-11, ENG-12, ENG-13, ENG-14, ENG-15, ENG-16, ENG-17, +2 more | 1 |\n| covered | `a` A |",
		"| covered | `r` R | 2 reviews | 2 |",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("criteria.md has no %q:\n%s", want, doc)
		}
	}
	w, _ := NewWindow("2024-01-01", "2024-03-31")
	if doc := string(f.criteriaDoc(w)); !strings.HasPrefix(doc, "# Framework coverage: 2024-01-01..2024-03-31\n") {
		t.Errorf("a window's criteria.md:\n%s", doc)
	}
}
