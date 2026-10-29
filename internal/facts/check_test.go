package facts

import (
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/collect"
)

func TestCheck(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	run := func(text string) []string {
		var out []string
		for _, fd := range f.Check("notes/ENG-1.md", text) {
			out = append(out, fd.String())
		}
		return out
	}
	head := "---\nitem: ENG-1\ninputs: " + f.items["ENG-1"].Pack + "\n---\n"
	var body strings.Builder
	for _, s := range NoteSections {
		body.WriteString("\n## " + s + "\n\nShipped fast search [[ENG-1]] with [[app#5]] and [[app@c1]], see [[doc+100]], [[adr+app+0001-use-queues]] and [[ENG-4]].\n")
	}
	check(t, "a good note", run(head+body.String()), []string(nil))
	check(t, "a good note with Windows line endings", run(strings.ReplaceAll(head+body.String(), "\n", "\r\n")), []string(nil))
	var names strings.Builder
	for _, s := range NoteSections {
		names.WriteString("\n## " + s + "\n\nMoved Office 365 tenants to Angular 9 and .NET 10 [[ENG-1]], fixing HTTP 429 errors in version 2.\n" +
			"Angular 9, C# 12, Node 18 and Windows 11.\n\n- Python 3\n- Storybook (CSF 3.0 stories and docs pages)\n")
	}
	check(t, "versions and names are not counts", run(head+names.String()), []string(nil))
	for _, f := range run(head + "\n## Summary\n\nI upgraded the monorepo to\nAngular 15 and the library too.\n" +
		"Office 365 tenants could connect.\n\n- Angular 15 upgrade for the library.\n") {
		if strings.Contains(f, "no counts") {
			t.Errorf("a name that opens a line or sentence was flagged: %s", f)
		}
	}
	counts := run(head + "\n## Summary\n\nMerged 12 PRs. Led 4 people, and I wrote 3 docs.\nI rebuilt Storybook 12 times.\n" +
		"TypeScript 1,200 lines, Jira 9 issues, Confluence 5 pages and ADR 2 docs.\n")
	for _, n := range []string{`"12"`, `"4"`, `"3"`, `"1,200"`, `"9"`, `"5"`, `"2"`} {
		if !strings.Contains(strings.Join(counts, "\n"), n+": a note carries no counts") {
			t.Errorf("no count warning for %s:\n%s", n, strings.Join(counts, "\n"))
		}
	}
	got := run(head + "\n## Summary\n\nAlso [[ENG-2]] and [[ENG-404]], in 12 PRs.\n")
	if len(got) < 3 || !strings.Contains(strings.Join(got, "\n"), "[[ENG-2]] is not part of ENG-1's evidence") ||
		!strings.Contains(strings.Join(got, "\n"), "[[ENG-404]] names nothing") || !strings.Contains(strings.Join(got, "\n"), `"12": a note carries no counts`) ||
		!strings.Contains(strings.Join(got, "\n"), "missing section: ## What I did") {
		t.Errorf("findings:\n%s", strings.Join(got, "\n"))
	}

	draft := "---\nwindow: 2024-01-01..2024-12-31\n---\n\n" +
		"I merged 2 PRs and about 6 commits, and finished [[ENG-1]] with its 2 sub-tasks.\n\n" +
		"I mentored 4 people, cut 40% of the time, and upgraded to Go 1.22 on 2024-03-01.\n\n" +
		"See https://tracker.example/browse/ENG-1 and https://tracker.example/browse/ENG-999 and ENG-77.\n"
	var findings []string
	for _, fd := range f.Check("draft.md", draft) {
		findings = append(findings, fd.String())
	}
	want := []string{
		`7: warning: "4" matches no fact in scope`, `7: warning: "40%" is derived`, // Go 1.22 is a version
		"9: warning: https://tracker.example/browse/ENG-999 is not in the dataset", "9: warning: ENG-77 is not in the dataset",
	}
	if len(findings) != len(want) {
		t.Errorf("findings:\n%s", strings.Join(findings, "\n"))
	}
	for i := range want {
		if i < len(findings) && !strings.HasPrefix(findings[i], want[i]) {
			t.Errorf("finding %d = %q, want prefix %q", i, findings[i], want[i])
		}
	}
	adr := f.items["adr+app+0001-use-queues"]
	var adrNote []string
	for _, fd := range f.Check("notes/adr+app+0001-use-queues.md", "---\nitem: "+adr.ID+"\ninputs: "+adr.Pack+"\n---\n"+
		strings.ReplaceAll(body.String(), "[[doc+100]], ", "")) {
		adrNote = append(adrNote, fd.String())
	}
	if s := strings.Join(adrNote, "\n"); !strings.Contains(s, "[[ENG-4]] is not part of") || strings.Contains(s, "[[app#5]]") ||
		strings.Contains(s, "[[app@c1]]") || strings.Contains(s, "[[ENG-1]]") {
		t.Errorf("a note on an ADR cites the changes that wrote it, and the items they count toward:\n%s", s)
	}
	text := "---\nwindow: all\n---\n\nShipped search [[ENG-1]], and [[app#5|the PR]].\n"
	check(t, "plain", f.Final(text, false), "Shipped search, and the PR.\n")
	check(t, "links", f.Final(text, true), "Shipped search [ENG-1](https://tracker.example/browse/ENG-1), and [the PR](https://code.example/org/app/pull/5).\n")
}

func TestFindingString(t *testing.T) {
	check(t, "a warning", Finding{Line: 3, Message: "hm"}.String(), "3: warning: hm")
	check(t, "an error", Finding{Line: 1, Error: true, Message: "no"}.String(), "1: error: no")
}

// findings runs Check and returns its findings as text.
func findings(f *Facts, path, text string) []string {
	var out []string
	for _, fd := range f.Check(path, text) {
		out = append(out, fd.String())
	}
	return out
}

// A file is a note (item:) or a draft (window:); anything else, or a note that cannot be read as
// its item's, is an error before any citation is checked.
func TestCheckFrontmatter(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	pack := f.items["ENG-1"].Pack
	var sections strings.Builder
	for _, s := range NoteSections {
		sections.WriteString("\n## " + s + "\n\nWork [[ENG-1]].\n")
	}
	for _, tc := range []struct {
		name, path, text string
		want             []string // prefixes of the findings
	}{
		{"no frontmatter", "draft.md", "I did things.\n", []string{"1: error: no frontmatter"}},
		{"neither a note nor a draft", "draft.md", "---\ntitle: x\n---\n", []string{"1: error: the frontmatter needs item: <id> (a note) or window: ... (a draft)"}},
		{"a draft's window unreadable", "draft.md", "---\nwindow: 2024..\n---\n", []string{`2: error: window start "2024": want YYYY-MM-DD`}},
		{"a note on no item", "notes/ENG-999.md", "---\nitem: ENG-999\ninputs: x\n---\n", []string{`4: error: item "ENG-999" is not a work item (items.jsonl)`}},
		{"a note filed under another name", "notes/fast-search.md", "---\nitem: ENG-1\ninputs: " + pack + "\n---\n" + sections.String(),
			[]string{"4: warning: a note on ENG-1 is read from notes/ENG-1.md"}},
		{"a note without inputs", "notes/ENG-1.md", "---\nitem: ENG-1\n---\n" + sections.String(),
			[]string{"3: error: the frontmatter needs inputs: " + pack}},
		{"a good note", "notes/ENG-1.md", "---\nitem: ENG-1\ninputs: " + pack + "\n---\n" + sections.String(), nil},
	} {
		got := findings(f, tc.path, tc.text)
		ok := len(got) == len(tc.want)
		for i := 0; ok && i < len(got); i++ {
			ok = strings.HasPrefix(got[i], tc.want[i])
		}
		if !ok {
			t.Errorf("%s: findings %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Numbers inside code are not claims.
func TestCheckSkipsCode(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	draft := "---\nwindow: all\n---\n```\nI merged 999 PRs\n```\n\n~~~\n998\n~~~\n\nI merged `997` PRs.\n"
	check(t, "findings", findings(f, "draft.md", draft), []string(nil))
	if got := findings(f, "draft.md", "---\nwindow: all\n---\nI merged 999 PRs.\n"); len(got) != 1 || !strings.HasPrefix(got[0], `4: warning: "999" matches no fact`) {
		t.Errorf("outside code: findings %q, want one on 999", got)
	}
}

// In a draft, a number is in scope when it is one of the window's totals, one the user wrote in
// their context, or one of the totals of a period or record its paragraph names.
func TestCheckScope(t *testing.T) {
	me := []string{"author", "committer"}
	var commits []collect.Commit
	for i := range 7 {
		commits = append(commits, commit("n"+itoa(i), "2024-02-01T10:00:00Z", jane, me, "work", nil))
	}
	commits = append(commits, commit("o1", "2023-05-01T10:00:00Z", jane, me, "old", nil), commit("o2", "2023-05-01T11:00:00Z", jane, me, "old", nil))
	ctx := t.TempDir()
	writeFile(t, ctx, "profile.md", "I mentored 4 people.")
	f := build(t, oneRepo(commits, nil, nil), Inputs{ContextDir: ctx})
	for _, tc := range []struct {
		text string
		ok   bool
	}{
		{"I made 9 commits.", true},                     // the window's
		{"I made 7 commits.", false},                    // 2024's, but the paragraph does not say so
		{"In 2024 I made 7 commits.", true},             // a year it names
		{"In Q1 2024, 7 commits.", true},                // a quarter it names
		{"In 2024q2 I made 7 commits.", false},          // a quarter with nothing in it
		{"I made 7 commits [[repo+app+2024q1]].", true}, // a record it cites
		{"I mentored 4 people.", true},                  // the user's own words
		{"Build times fell 30%.", false},                // a ratio: derived
		{"I made about 9 commits.", true},
		{"I made 12 commits.", false},
	} {
		got := findings(f, "draft.md", "---\nwindow: all\n---\n"+tc.text+"\n")
		if (len(got) == 0) != tc.ok {
			t.Errorf("Check(%q) = %q, want ok %v", tc.text, got, tc.ok)
		}
	}
}

// partOfName tells a number in a name or version (Angular 15, Office 365, CSF 3.0) from a count
// (12 PRs): a capitalised word before it names something, unless the sentence opens with it and it
// is a verb; a unit after a whole number makes it a count.
func TestPartOfName(t *testing.T) {
	for _, tc := range []struct {
		before, num, after string
		want               bool
	}{
		{"Angular ", "15", "", true},
		{"upgraded to Angular ", "15", " and the library", true},
		{"**Angular ", "9", "", true},
		{"Office ", "365", " tenants", true},
		{"Running on HTTP ", "429", " errors", true},
		{"CSF ", "3.0", " stories", true}, // a decimal stays a version, whatever follows
		{"the version ", "2", "", true},
		{"in sprint ", "4", "", true},
		{"- Python ", "3", "", true}, // a list item opens a sentence
		{"Merged ", "12", " PRs", false},
		{"TypeScript ", "1,200", " lines", false},
		{"I rebuilt Storybook ", "12", " times", false},
		{"Led ", "4", "", false},             // a verb opening a sentence
		{"Shipped it. Led ", "4", "", false}, // after a full stop too
		{"1. Building ", "7", " services", false},
		{"about ", "12", "", false},
		{"", "12", "", false},
		{"Angular  ", "15", "", false},
	} {
		if got := partOfName(tc.before, tc.num, tc.after); got != tc.want {
			t.Errorf("partOfName(%q, %q, %q) = %v, want %v", tc.before, tc.num, tc.after, got, tc.want)
		}
	}
}

// A number matches when it is in scope exactly, or within 10% when written as an approximation
// (~, about, 1.2k, or a round number of 100 or more); a percentage or multiple never matches.
func TestMatches(t *testing.T) {
	c := &checker{values: map[int]bool{120: true, 7: true, 1200: true, 0: true}}
	local := map[int]bool{42: true}
	for _, tc := range []struct {
		v      float64
		suffix string
		approx bool
		want   bool
	}{
		{120, "", false, true},
		{7, "", false, true},
		{42, "", false, true}, // the paragraph's own records'
		{8, "", false, false},
		{125, "", true, true},
		{135, "", true, false},
		{130, "", false, true}, // round: an approximation
		{125, "", false, false},
		{90, "", false, false}, // round, but small
		{1.2, "k", false, true},
		{1.3, "k", false, true},
		{1.4, "k", false, false},
		{1.2, "M", false, false},
		{7.5, "", false, false},
		{120, "%", false, false},
		{7, "x", false, false},
	} {
		if got := c.matches(tc.v, tc.suffix, tc.approx, local); got != tc.want {
			t.Errorf("matches(%v%s, approx %v) = %v, want %v", tc.v, tc.suffix, tc.approx, got, tc.want)
		}
	}
	for s, want := range map[string]float64{"12": 12, "1,200": 1200, "3.5": 3.5} {
		if got, ok := parseCount(s); !ok || got != want {
			t.Errorf("parseCount(%q) = %v, %v; want %v", s, got, ok, want)
		}
	}
	if _, ok := parseCount("x"); ok {
		t.Error(`parseCount("x") is ok`)
	}
}

// resolveFacts is the fixture with a commit of a full-length sha, an issue's earlier key, and an
// evidence file.
func resolveFacts(t *testing.T) *Facts {
	raw := fixture()
	raw.Commits = append(raw.Commits, commit("0123456789abcdef", "2024-06-01T10:00:00Z", jane, []string{"author", "committer"}, "long", nil))
	raw.Issues[2].PreviousKeys = []string{"SRCH-4"}
	ctx := t.TempDir()
	writeFile(t, ctx, "evidence/talk.md", "---\nwhen: 2024-02-01\nshows: code\n---\nA talk.\n")
	return build(t, raw, Inputs{ContextDir: ctx})
}

// A citation names an item, an issue (by any key it has had), a change (by id, or a commit's sha
// of 7 characters or more), a doc, a review or an evidence file.
func TestResolve(t *testing.T) {
	f := resolveFacts(t)
	for _, tc := range []struct{ ref, id, link string }{
		{"ENG-1", "ENG-1", "https://tracker.example/browse/ENG-1"},
		{"repo+app+2024q1", "repo+app+2024q1", "https://code.example/org/app"},
		{"ENG-4", "ENG-4", "https://tracker.example/browse/ENG-4"},
		{"SRCH-4", "ENG-4", "https://tracker.example/browse/ENG-4"},
		{"eng-4", "", ""}, // keys are written in capitals
		{"app#5", "app#5", "https://code.example/org/app/pull/5"},
		{"app@c1", "app@c1", "https://code.example/org/app/commit/c1"},
		{"app@0123456", "app@0123456789ab", "https://code.example/org/app/commit/0123456789abcdef"},
		{"app@0123456789abcdef", "app@0123456789ab", "https://code.example/org/app/commit/0123456789abcdef"},
		{"app@012345", "", ""}, // too short to be a prefix
		{"lib@0123456", "", ""},
		{"doc+100", "doc+100", "https://wiki.example/100"},
		{"adr+app+0002-theirs", "adr+app+0002-theirs", "https://code.example/org/app/blob/main/docs/decisions/0002-theirs.md"},
		{"review+app#21", "review+app#21", "https://code.example/org/app/pull/21"},
		{"evidence+talk", "evidence+talk", ""},
		{"nothing", "", ""},
	} {
		id, link := f.resolve(tc.ref)
		check(t, "resolve("+tc.ref+")", []string{id, link}, []string{tc.id, tc.link})
	}
}

// A note may cite its item's own records: its issues (by earlier keys too), its initiative and
// features (and their issues), the changes counted toward it or naming it, its docs and reviews.
func TestEvidence(t *testing.T) {
	f := resolveFacts(t)
	cites := func(id string) []string { return sortedSet(f.evidence(f.items[id])) }
	check(t, "a feature's", cites("ENG-1"), []string{"ENG-1", "ENG-4", "ENG-10", "SRCH-4", "adr+app+0001-use-queues", "app#5", "app@c1",
		"app@c12", "doc+100", "review+app#21"})
	check(t, "an initiative's", cites("ENG-10"), []string{"ENG-1", "ENG-4", "ENG-10"})
	check(t, "an ADR's: the changes that wrote it, and their items", cites("adr+app+0001-use-queues"),
		[]string{"ENG-1", "ENG-2", "adr+app+0001-use-queues", "app#5", "app@c1"})
}

// Final is a checked text for sharing: citations dropped (with the space before them), labels
// kept, or citations made links where the record has one.
func TestFinal(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	for _, tc := range []struct {
		text  string
		links bool
		want  string
	}{
		{"---\nwindow: all\n---\n\n\nDid it [[ENG-1]].\n", false, "Did it.\n"},
		{"Did it\t[[ENG-1]], and [[ app#5 |the PR]].\n", false, "Did it, and the PR.\n"},
		{"No frontmatter [[ENG-1]].\n", true, "No frontmatter [ENG-1](https://tracker.example/browse/ENG-1).\n"},
		{"Gone [[ENG-404]], and [[ENG-404|a label]].\n", true, "Gone ENG-404, and a label.\n"},
	} {
		if got := f.Final(tc.text, tc.links); got != tc.want {
			t.Errorf("Final(%q, %v) = %q, want %q", tc.text, tc.links, got, tc.want)
		}
	}
}

// A link to a host the dataset has links on should be one of its links; links end before trailing
// punctuation or a slash.
func TestKnownURLs(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	for u, want := range map[string]bool{"https://tracker.example/browse/ENG-404": true, "https://elsewhere.example/x": false, "not a url": false} {
		if got := f.knownHost(u); got != want {
			t.Errorf("knownHost(%q) = %v, want %v", u, got, want)
		}
	}
	for u, want := range map[string]bool{
		"https://tracker.example/browse/ENG-1": true, "https://tracker.example/browse/ENG-1/": true, "https://tracker.example/browse/ENG-1.": true,
		"https://code.example/org/app/pull/5,": true, "https://code.example/org/app": true, "https://code.example/org/app/pull/21": true,
		"https://tracker.example/browse/ENG-404": false,
	} {
		if got := f.knownURL(u); got != want {
			t.Errorf("knownURL(%q) = %v, want %v", u, got, want)
		}
	}
	// An issue key of a project the tracker has (or had) is a claim to check; other KEY-1 shapes
	// (HTTP-2, UTF-8) are not.
	for prefix, want := range map[string]bool{"ENG": true, "OLD": true, "HTTP": false, "UTF": false} {
		if got := f.isProjectKey(prefix); got != want {
			t.Errorf("isProjectKey(%q) = %v, want %v", prefix, got, want)
		}
	}
	check(t, "keys of other shapes are not flagged", findings(f, "draft.md", "---\nwindow: all\n---\nUsed UTF-8 over HTTP-2, not ENG-77.\n"),
		[]string{"4: warning: ENG-77 is not in the dataset (the user was never assigned it, nor named it in git work)"})
}
