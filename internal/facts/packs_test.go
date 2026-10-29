package facts

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/tracker"
)

// A change's description is worth a pack's room without its HTML comments and template (link
// lists, headings, checklists), and only with 4 words or more besides issue keys.
func TestExcerpt(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"prose, on one line", "Adds a retry\nwith backoff to the queue.", "Adds a retry with backoff to the queue."},
		{"its template left out", "<!-- describe the change -->\n## Summary\nAdds retries to the queue client.\n- [x] tests\n- [ ] docs\n" +
			"[ENG-1]: https://tracker.example/browse/ENG-1", "Adds retries to the queue client."},
		{"keys alone", "ENG-1 ENG-2 ENG-3 ENG-4 ENG-5", ""},
		{"too few words", "Fixes ENG-1. See ENG-2.", ""},
		{"a template with nothing in it", "## Summary\n<!-- what and why -->\n- [ ] tests", ""},
		{"empty", "", ""},
	} {
		check(t, tc.name, excerpt(tc.body), tc.want)
	}
	long := excerpt(strings.Repeat("word ", 400)) // 1999 characters on one line
	if !strings.HasSuffix(long, " … [+799 characters]") || len([]rune(long)) > maxBody+len(" … [+799 characters]") {
		t.Errorf("excerpt of a long description = %q, want it clipped to %d characters", long, maxBody)
	}
}

// clip cuts text to n characters (not bytes), marking the cut as the raw data does.
func TestClip(t *testing.T) {
	for _, tc := range []struct {
		text string
		n    int
		want string
	}{
		{"  short  ", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"eleven char", 10, "eleven cha … [+1 characters]"},
		{"word word word", 5, "word … [+9 characters]"}, // the cut is trimmed
		{"ééééé", 3, "ééé … [+2 characters]"},
		{"", 3, ""},
	} {
		if got := clip(tc.text, tc.n); got != tc.want {
			t.Errorf("clip(%q, %d) = %q, want %q", tc.text, tc.n, got, tc.want)
		}
	}
	check(t, "oneLine", oneLine("  a\n\tb   c \r\n"), "a b c")
}

// Where a change's files were, to two directory levels.
// clipTail keeps the end of a text, where the user's newest words are, and says how much it cut. It
// starts on a character, never inside one.
func TestClipTail(t *testing.T) {
	tests := []struct {
		name, text string
		n          int
		want       string
	}{
		{"short enough is whole", "abc", 10, "abc"},
		{"exactly the limit is whole", "abcde", 5, "abcde"},
		{"longer keeps the end", "one two three four", 10, "[8 earlier characters cut] …three four"},
		{"never starts inside a character", "ééééé", 5, "[6 earlier characters cut] …éé"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clipTail(tt.text, tt.n); got != tt.want {
				t.Errorf("clipTail(%q, %d) = %q, want %q", tt.text, tt.n, got, tt.want)
			}
		})
	}
}

func TestTopDir(t *testing.T) {
	for p, want := range map[string]string{
		"main.go": "(root)", "src/a.go": "src/", "src/x/a.go": "src/x/", "src/x/y/z.go": "src/x/", ".github/workflows/ci.yml": ".github/workflows/",
	} {
		if got := topDir(p); got != want {
			t.Errorf("topDir(%q) = %q, want %q", p, got, want)
		}
	}
	for ts, want := range map[string]string{"2024-01-01T23:30:00-02:00": "2024-01-02", "2024-01-01": "2024-01-01", "": "", "soon": ""} {
		if got := dayOf(ts); got != want {
			t.Errorf("dayOf(%q) = %q, want %q", ts, got, want)
		}
	}
}

// packFacts is an item with n landed PRs of n lines each (so the largest is the last), its sub-tasks
// and comments.
func packFacts(t *testing.T, changes, members, comments int) (*Facts, *Item) {
	f := &Facts{issues: map[string]*Issue{}, changes: map[string]*Change{}, items: map[string]*Item{}, reviews: map[string]*Review{},
		subjects: map[string]string{}, context: map[string]string{}}
	own := &Issue{Key: "ENG-1", rec: &collect.IssueRecord{Issue: tracker.Issue{Description: "Make search fast."}}}
	f.issues["ENG-1"] = own
	it := &Item{ID: "ENG-1", Kind: KindFeature, own: own, Issues: []string{"ENG-1"}}
	for i := 1; i <= members; i++ {
		k := "ENG-" + itoa(100+i)
		is := &Issue{Key: k, Type: "Sub-task", StatusCategory: "done", Involvement: Worked, Title: "part " + itoa(i), rec: &collect.IssueRecord{}}
		f.issues[k], it.Issues = is, append(it.Issues, k)
	}
	for i := 1; i <= comments; i++ {
		own.rec.Comments = append(own.rec.Comments, tracker.Comment{ByUser: true, CreatedAt: "2024-01-" + pad(i) + "T00:00:00Z", Body: "comment " + itoa(i)})
	}
	own.rec.Comments = append(own.rec.Comments, tracker.Comment{CreatedAt: "2024-01-01T00:00:00Z", Body: "someone else's"},
		tracker.Comment{ByUser: true, CreatedAt: "2024-01-01T00:00:00Z", Body: "  "})
	for i := 1; i <= changes; i++ {
		id := "app#" + itoa(i)
		c := &Change{ID: id, Kind: KindPR, Repo: "app", Landed: true, State: "merged", Class: Self, Title: "PR " + itoa(i),
			Lines: Lines{Added: i}, at: mustTime(t, "2024-01-01").AddDate(0, 0, i),
			body: "This change makes the search " + itoa(i) + " times faster for everyone.", Commits: []string{"s" + itoa(i), "dup"}}
		f.changes[id], it.Changes = c, append(it.Changes, id)
		f.subjects["app@s"+itoa(i)], f.subjects["app@dup"] = "subject "+itoa(i), "the same subject"
	}
	return f, it
}

func pad(i int) string { return strings.Repeat("0", 2-len(itoa(i))) + itoa(i) }

// A pack stays bounded: past each limit it keeps what matters most (the largest changes, in date
// order; the oldest comments) and says how many it left out.
func TestPackBounds(t *testing.T) {
	f, it := packFacts(t, maxChanges+5, maxMembers+2, maxComments+3)
	in := f.packInput(it)
	check(t, "left out", in.Omitted, packOmitted{Members: 2, Comments: 3, Changes: 5})
	check(t, "changes: the largest, in date order", []string{in.Changes[0].ID, in.Changes[len(in.Changes)-1].ID}, []string{"app#6", "app#45"})
	check(t, "changes kept", len(in.Changes), maxChanges)
	check(t, "members: the item's own issue is not one", []any{len(in.Members), in.Members[0].Key}, []any{maxMembers, "ENG-101"})
	check(t, "comments: the user's, oldest first", []string{in.Comments[0].Text, in.Comments[maxComments-1].Text}, []string{"comment 1", "comment 8"})
	check(t, "descriptions: the largest changes'", []any{len(in.Bodies), in.Bodies[0].Ref}, []any{maxBodies, "app#45"})
	check(t, "commit messages: each once, all of them", len(in.Subjects), maxChanges+5+1)
	check(t, "description", in.Description, "Make search fast.")

	f, it = packFacts(t, 2, 0, 0)
	in = f.packInput(it)
	check(t, "under the limits, nothing left out", in.Omitted, packOmitted{})
	check(t, "a subject repeating a change's title is left out", in.Subjects, []string{"subject 1", "the same subject", "subject 2"})
}

// The reviews in a pack are the wordiest, by day; a pack without reviews has no reviews field, so
// that its hash is what it was before reviews were collected.
func TestPackReviews(t *testing.T) {
	f, it := packFacts(t, 1, 0, 0)
	for i := 1; i <= maxReviews+2; i++ {
		ref := reviewRef("app", 100+i)
		f.reviews[ref] = &Review{Ref: ref, First: "2024-02-" + pad(30-i), AuthorClass: Colleague, Title: "theirs\n" + itoa(i), Words: i,
			Comments: 1, texts: []packText{{Text: "first"}, {Text: "second"}}}
		it.Reviews = append(it.Reviews, ref)
	}
	in := f.packInput(it)
	check(t, "reviews left out: the fewest words", in.Omitted.Reviews, 2)
	check(t, "the first, by day", in.Reviews[0], packReview{Ref: "review+app#112", Day: "2024-02-18", Author: Colleague, Did: "1 comment",
		Title: "theirs 12", Text: "first / second", words: 12})
	check(t, "the last", in.Reviews[maxReviews-1].Ref, "review+app#103")

	it.Reviews = nil
	b, _ := json.Marshal(f.packInput(it))
	if strings.Contains(string(b), `"reviews"`) {
		t.Errorf("a pack without reviews: %s", b)
	}
}

// Only a landed change of the user's has lines and paths in a pack; generated files are not
// where the work was.
func TestPackChanges(t *testing.T) {
	f, it := packFacts(t, 0, 0, 0)
	add := func(c *Change) {
		f.changes[c.ID], it.Changes = c, append(it.Changes, c.ID)
	}
	add(&Change{ID: "app@a", Kind: KindCommit, Repo: "app", Landed: true, State: "landed", Class: Self, Title: "landed\n  work",
		Lines: Lines{Added: 3, Deleted: 1}, at: mustTime(t, "2024-01-02"), body: "A long commit body that says plenty of words.",
		files: []fileChange{newFileChange("src/a.go", 3, 1), newFileChange("src/b.go", 0, 0), newFileChange("yarn.lock", 9, 9)}})
	add(&Change{ID: "app@b", Kind: KindCommit, Repo: "app", State: "unlanded", Class: Self, Title: "draft", at: mustTime(t, "2024-01-03"),
		files: []fileChange{newFileChange("web/x.ts", 3, 1)}})
	add(&Change{ID: "app@c", Kind: KindCommit, Repo: "app", Landed: true, State: "landed", Class: CoAuthor, Title: "theirs",
		Lines: Lines{Added: 50}, at: mustTime(t, "2024-01-04"), files: []fileChange{newFileChange("lib/y.go", 50, 0)}})
	in := f.packInput(it)
	check(t, "changes", in.Changes, []packChange{
		{ID: "app@a", Kind: KindCommit, Day: "2024-01-02", State: "landed", Class: Self, Lines: "+3 -1", Title: "landed work", weight: 4},
		{ID: "app@b", Kind: KindCommit, Day: "2024-01-03", State: "unlanded", Class: Self, Title: "draft"},
		{ID: "app@c", Kind: KindCommit, Day: "2024-01-04", State: "landed", Class: CoAuthor, Title: "theirs"},
	})
	check(t, "paths", in.Paths, []packPath{{Path: "src/", Files: 2}})
	check(t, "a commit's body is not a description", in.Bodies, []packText{})
}

func TestRenderPack(t *testing.T) {
	in := &packInput{ID: "doc+7", Kind: KindDoc, Type: "page", Effort: "S", Span: "2024-01-01..2024-01-09", Title: "Runbook",
		Initiative: &Ref{ID: "ENG-10", Title: "Search"}, Repos: []string{"app", "lib"}, URL: "https://wiki.example/7",
		Doc: &packDoc{Space: "Operations", Ancestors: []string{"Home", "Runbooks"}, Labels: []string{"ops"}, Created: "2024-01-01",
			Mine: "created by you; 2 of 3 versions yours", Live: true, Body: "The body."},
		Members:  []packIssue{{Key: "ENG-4", Type: "Sub-task", Status: "done", Involvement: Assigned, DoneByMe: true, Title: "part"}},
		Comments: []packText{{Ref: "ENG-4", Day: "2024-01-03", Text: "Indexed it."}},
		Changes:  []packChange{{ID: "app#9", Kind: KindPR, Day: "2024-01-05", State: "merged", Class: Agent, Lines: "+1 -0", Title: "agent work"}},
		Subjects: []string{"fix it"}, Paths: []packPath{{Path: "src/", Files: 2}}, Docs: []Ref{{ID: "doc+8", Title: "Design"}},
		FromUser: "It cut search time.",
		Omitted:  packOmitted{Members: 3, Comments: 1, Changes: 2, Subjects: 4},
	}
	out := string(renderPack(in, "sha256:abc"))
	for _, want := range []string{
		"# doc+7 · Runbook\n", "notes/doc+7.md", "---\nitem: doc+7\ninputs: sha256:abc\n---",
		"- kind: doc (page)\n", "- effort: S, active 2024-01-01..2024-01-09\n", "- initiative: ENG-10 · Search\n", "- repos: app, lib\n",
		"- link: https://wiki.example/7\n",
		"## The page\n\n- space: Operations › Home › Runbooks\n- created 2024-01-01; created by you; 2 of 3 versions yours; a live doc (no publish step)\n- labels: ops\n\nThe body.\n",
		"- ENG-4 · Sub-task · done · assigned, done by you · part\n- … 3 more\n",
		"- 2024-01-03 on ENG-4: Indexed it.\n- … 1 more\n",
		"- 2024-01-05 app#9 · pr merged (agent) +1 -0 · agent work\n- … 2 more, smaller\n",
		"## Your commit messages\n\n- fix it\n- … 4 more\n", "- src/ (2 files)\n", "- doc+8 · Design\n", "## From you\n\nIt cut search time.\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("renderPack has no %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"status:", "your involvement", "## Description", "## Features", "## Your reviews", "## Descriptions"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("renderPack shows %q for an empty field:\n%s", unwanted, out)
		}
	}

	big := &packInput{ID: "ENG-1", Description: strings.Repeat("A line of the description.\n", 1000)}
	out = string(renderPack(big, ""))
	if len(out) > maxPackBytes+64 || !strings.HasSuffix(out, "\n… [pack cut at 16 KiB]\n") {
		t.Errorf("a pack over %d bytes = %d bytes, ending %q", maxPackBytes, len(out), out[len(out)-40:])
	}
}

// The pack hash is the inputs a note was written from: it changes with the pack's content, and
// only then.
func TestPackHash(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	g := build(t, fixture(), Inputs{})
	check(t, "the same dataset, the same hash", g.items["ENG-1"].Pack, f.items["ENG-1"].Pack)
	raw := fixture()
	raw.Issues[1].Description = "Make search faster."
	if h := build(t, raw, Inputs{}).items["ENG-1"].Pack; h == f.items["ENG-1"].Pack || !strings.HasPrefix(h, "sha256:") {
		t.Errorf("a new description kept the hash %s", h)
	}
	raw = fixture()
	raw.Issues[1].Comments = append(raw.Issues[1].Comments, tracker.Comment{ID: "k9", Author: uOther, CreatedAt: "2024-01-04T00:00:00Z", Body: "Nice."})
	check(t, "a colleague's comment is not in the pack", build(t, raw, Inputs{}).items["ENG-1"].Pack, f.items["ENG-1"].Pack)
}
