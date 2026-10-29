package facts

import (
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/collect"
)

func TestTechnologies(t *testing.T) {
	raw := fixture()
	raw.Commits = append(raw.Commits,
		commit("c20", "2024-06-01T10:00:00Z", jane, []string{"author", "committer"}, "ship it", func(c *collect.Commit) {
			c.Files = []collect.FileChange{file("Dockerfile", 5, 0), file("web/card.stories.tsx", 40, 1), file("README.md", 3, 0)}
		}),
		commit("c21", "2024-06-02T10:00:00Z", other, []string{"co-author"}, "theirs\n\nCo-authored-by: Jane Doe <jane@example.com>",
			func(c *collect.Commit) { c.Files = []collect.FileChange{file("web/x.rs", 500, 0)} }),
	)
	f := build(t, raw, Inputs{})
	var names []string
	for _, tc := range f.Technologies {
		names = append(names, tc.Name)
	}
	check(t, "technologies, most days first; co-authored and excluded files left out", names, []string{"Go", "Storybook", "TypeScript", "Docker"})
	g := f.technologies["Go"]
	check(t, "Go", []any{g.Category, g.Significant, g.First, g.Last, g.Totals, g.Repos, g.Items},
		[]any{"language", true, "2024-01-02", "2024-02-01",
			TechTotals{Changes: 3, LinesAdded: 60, LinesDeleted: 7, Files: 3, ActiveDays: 4, Repos: 1}, []string{"app"}, []string{"ENG-1", "ENG-2"}})
	check(t, "Go by year", g.ByYear, []TechPeriod{{Period: "2024", Totals: g.Totals}})
	check(t, "a tool used on one day is not significant", f.technologies["Docker"].Significant, false)
	check(t, "a change's technologies, most lines first", f.changes["app@c20"].Technologies, []string{"Storybook", "TypeScript", "Docker"})
	check(t, "an unlanded change has none", f.changes["app@c11"].Technologies, []string{})
	check(t, "an item's", f.items["ENG-1"].Technologies, []string{"Go"})
	check(t, "a repo's", f.Repos[0].Technologies, []string{"Go", "Storybook", "TypeScript", "Docker"})

	w, _ := NewWindow("2024-01-01", "2024-01-31")
	check(t, "a window's", f.Summary(w).Technologies, []TechSummary{{Name: "Go", Category: "language", Significant: true,
		First: "2024-01-02", Last: "2024-01-04", Totals: TechTotals{Changes: 2, LinesAdded: 40, LinesDeleted: 7, Files: 2, ActiveDays: 3, Repos: 1}}})

	draft := "---\nwindow: all\n---\nTypeScript: 40 lines.\n\nRust: 40 lines.\n\nTypeScript, with 40 people.\n\nGo in 2024: 60 lines.\n"
	var findings []string
	for _, fd := range f.Check("draft.md", draft) {
		findings = append(findings, fd.String())
	}
	check(t, "a technology's numbers, for what they measure", len(findings), 2)
	if len(findings) == 2 && (!strings.Contains(findings[0], "Rust: 40 lines") || !strings.Contains(findings[1], "40 people")) {
		t.Errorf("findings: %v", findings)
	}
}

// techChange is a landed change in app, with the user's active days on it.
func techChange(t *testing.T, id, landed, class, item string, days []string, files ...fileChange) *Change {
	c := &Change{ID: id, Repo: "app", Landed: true, Class: class, Item: item, at: mustTime(t, landed), files: files}
	for _, d := range days {
		c.evs = append(c.evs, event{at: mustTime(t, d+"T10:00:00Z"), kind: evActive}, event{at: mustTime(t, d+"T10:00:00Z"), kind: evCommit})
	}
	return c
}

func techFacts(changes ...*Change) *Facts {
	f := &Facts{Changes: changes, changes: map[string]*Change{}}
	for _, c := range changes {
		f.changes[c.ID] = c
	}
	f.buildTechnologies()
	return f
}

// A technology is significant (worth naming in a CV) with 3 active days or more on it; a language
// needs 50 lines added or 3 changes as well, a tool or platform the days alone.
func TestTechSignificance(t *testing.T) {
	three := []string{"2024-01-01", "2024-01-02", "2024-01-03"}
	for _, tc := range []struct {
		name    string
		changes []*Change
		tech    string
		want    bool
	}{
		{"a language, 3 days, 50 lines", []*Change{techChange(t, "a", "2024-01-03", Self, "I", three, newFileChange("a.go", 50, 0))}, "Go", true},
		{"a language, 3 days, 49 lines in 1 change", []*Change{techChange(t, "a", "2024-01-03", Self, "I", three, newFileChange("a.go", 49, 0))}, "Go", false},
		{"a language, 3 days, deletions are not lines added", []*Change{techChange(t, "a", "2024-01-03", Self, "I", three, newFileChange("a.go", 10, 500))}, "Go", false},
		{"a language, 3 changes on 3 days", []*Change{
			techChange(t, "a", "2024-01-01", Self, "I", three[:1], newFileChange("a.go", 1, 0)),
			techChange(t, "b", "2024-01-02", Self, "I", three[1:2], newFileChange("a.go", 1, 0)),
			techChange(t, "c", "2024-01-03", Self, "I", three[2:], newFileChange("a.go", 1, 0)),
		}, "Go", true},
		{"a language, 2 days, many lines", []*Change{techChange(t, "a", "2024-01-02", Self, "I", three[:2], newFileChange("a.go", 1000, 0))}, "Go", false},
		{"a tool, 3 days, 1 line", []*Change{techChange(t, "a", "2024-01-03", Self, "I", three, newFileChange("Dockerfile", 1, 0))}, "Docker", true},
		{"a tool, 2 days", []*Change{techChange(t, "a", "2024-01-02", Self, "I", three[:2], newFileChange("Dockerfile", 1, 0))}, "Docker", false},
	} {
		tech := techFacts(tc.changes...).technologies[tc.tech]
		if tech == nil {
			t.Errorf("%s: no %s", tc.name, tc.tech)
			continue
		}
		check(t, tc.name, tech.Significant, tc.want)
	}
}

// A technology's changes and lines count by when the change landed; its days as they fell. Only
// the user's landed changes count, without generated or bulk files.
func TestTechTotals(t *testing.T) {
	lib := techChange(t, "lib@c", "2024-03-02T00:00:00Z", Self, "ENG-1", []string{"2024-03-02"}, newFileChange("a.go", 3, 0))
	lib.Repo = "lib"
	unlanded := techChange(t, "app@d", "2024-03-03T00:00:00Z", Self, "ENG-1", []string{"2024-03-03"}, newFileChange("x.go", 9, 0))
	unlanded.Landed = false
	f := techFacts(
		techChange(t, "app@a", "2024-02-01T00:00:00Z", Agent, "ENG-1", []string{"2024-01-30", "2024-02-01"},
			newFileChange("a.go", 10, 2), newFileChange("b.go", 5, 0), newFileChange("vendor/lib/x.rs", 900, 0)),
		techChange(t, "app@b", "2024-03-01T00:00:00Z", Self, "ENG-2", []string{"2024-03-01"}, newFileChange("a.go", 1, 1)),
		lib, unlanded,
		techChange(t, "app@e", "2024-03-04T00:00:00Z", CoAuthor, "ENG-3", []string{"2024-03-04"}, newFileChange("y.go", 9, 0)),
	)
	jan, _ := NewWindow("2024-01-01", "2024-01-31")
	feb, _ := NewWindow("2024-02-01", "2024-02-29")
	check(t, "January: a day on it, the change not landed yet", f.techTotals("Go", jan), TechTotals{ActiveDays: 1})
	check(t, "February", f.techTotals("Go", feb), TechTotals{Changes: 1, AgentChanges: 1, LinesAdded: 15, LinesDeleted: 2, Files: 2, ActiveDays: 1, Repos: 1})
	check(t, "all time: files by repo and path", f.techTotals("Go", All),
		TechTotals{Changes: 3, AgentChanges: 1, LinesAdded: 19, LinesDeleted: 3, Files: 3, ActiveDays: 4, Repos: 2})
	g := f.technologies["Go"]
	check(t, "Go", []any{g.First, g.Last, g.Repos, g.Items}, []any{"2024-01-30", "2024-03-02", []string{"app", "lib"}, []string{"ENG-1", "ENG-2"}})
	check(t, "changes' technologies: not unlanded or co-authored ones", []any{f.changes["app@d"].Technologies, f.changes["app@e"].Technologies},
		[]any{[]string{}, []string{}})
	check(t, "no technology of generated files: Rust only vendored", len(f.Technologies), 1)
	check(t, "a window's summaries", f.techSummaries(jan), []TechSummary{{Name: "Go", Category: "language", Significant: g.Significant,
		First: "2024-01-30", Last: "2024-01-30", Totals: TechTotals{ActiveDays: 1}}})
	mar, _ := NewWindow("2025-01-01", "2025-12-31")
	check(t, "a window without it", f.techSummaries(mar), []TechSummary{})
}

// A technology spanning years has a breakdown per year with anything in it.
func TestTechByYear(t *testing.T) {
	f := techFacts(
		techChange(t, "a", "2022-06-01T00:00:00Z", Self, "I", []string{"2022-06-01"}, newFileChange("a.go", 1, 0)),
		techChange(t, "b", "2024-06-01T00:00:00Z", Self, "I", []string{"2024-06-01"}, newFileChange("a.go", 2, 0)),
	)
	var got []string
	for _, p := range f.technologies["Go"].ByYear {
		got = append(got, p.Period+":"+itoa(p.Totals.LinesAdded))
	}
	check(t, "by year, 2023 empty", got, []string{"2022:1", "2024:2"})
}

// Some changes' technologies, most lines (added and deleted) first, then by name.
func TestTechsOf(t *testing.T) {
	f := &Facts{changes: map[string]*Change{
		"a": {techs: []techUse{{name: "Go", added: 10}, {name: "SQL", added: 1, deleted: 4}}},
		"b": {techs: []techUse{{name: "SQL", added: 5}, {name: "Docker", added: 10}}},
	}}
	check(t, "techsOf", f.techsOf([]string{"a", "b"}), []string{"Docker", "Go", "SQL"})
	check(t, "techsOf none", f.techsOf(nil), []string{})
}

// A paragraph names a technology by any of its names, as a whole word.
func TestNamesTech(t *testing.T) {
	for _, tc := range []struct {
		text, name string
		want       bool
	}{
		{"I wrote Go and Rust.", "Go", true},
		{"Golang services", "Go", true}, // an alias
		{"Google", "Go", false},
		{"Gophers, then Go", "Go", true}, // a later match
		{"go", "Go", false},              // names are case-sensitive
		{"C# and F#", "C#", true},
		{"C#9", "C#", false},
		{"C++ code", "C++", true},
		{"C++ code", "C", false},
		{"ASP.NET Core", ".NET", true},
		{"TS_CONFIG", "TypeScript", false},
		{"TypeScripts", "TypeScript", false},
		{"TypeScript, 40 lines", "TypeScript", true},
		{"", "Go", false},
	} {
		if got := namesTech(tc.text, tc.name); got != tc.want {
			t.Errorf("namesTech(%q, %q) = %v, want %v", tc.text, tc.name, got, tc.want)
		}
	}
}
