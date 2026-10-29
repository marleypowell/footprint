package facts

import (
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/wiki"
)

func TestDocs(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	d, e := f.docs["100"], f.docs["101"]
	check(t, "written", []any{d.Share, d.Words >= substantialWords, d.Substantial, d.Item, d.Items}, []any{100, true, true, "doc+100", []string{"ENG-1"}})
	check(t, "edited once", []any{e.Share, e.Substantial, e.Item}, []any{25, false, ""})

	a, b := f.docs["app/docs/decisions/0001-use-queues.md"], f.docs["app/docs/decisions/0002-theirs.md"]
	check(t, "ADR: the user's commit and their agent's", []any{a.Ref, a.Type, a.CreatedAt, a.CreatedByMe, a.MyVersions, a.Versions, a.Share},
		[]any{"adr+app+0001-use-queues", "adr", "2024-01-02T09:00:00Z", true, 2, 3, 66})
	check(t, "ADR: substantial at its own length", []any{a.Words < substantialWords, a.Substantial, a.Item}, []any{true, true, "adr+app+0001-use-queues"})
	check(t, "ADR: the changes that wrote it (not the rename), and theirs and its issues' items", []any{a.Changes, a.Items, a.First, a.Last},
		[]any{[]string{"app@c1", "app#5"}, []string{"ENG-1", "ENG-2"}, "2024-01-02", "2024-01-04"})
	check(t, "ADR: a colleague's", []any{b.CreatedByMe, b.Share, b.Substantial, b.Item, b.Changes}, []any{false, 50, false, "", []string{"app@c7"}})
	it := f.items[a.Ref]
	check(t, "ADR item", []any{it.Kind, it.Type, it.AlsoChanges, it.ActiveDays}, []any{KindDoc, "adr", []string{"app@c1", "app#5"}, 2})
	pack := string(renderPack(it.pack, it.Pack))
	for _, want := range []string{"## The decision record", "- app: docs/decisions/0001-use-queues.md", "created by you; 2 of 3 commits yours",
		"- written in app#5 · ENG-1: feature"} {
		if !strings.Contains(pack, want) {
			t.Errorf("ADR pack has no %q:\n%s", want, pack)
		}
	}
	if pack := string(renderPack(f.items["ENG-1"].pack, f.items["ENG-1"].Pack)); !strings.Contains(pack, "- adr+app+0001-use-queues · Use queues") {
		t.Errorf("a feature's pack should name the ADR written in its changes:\n%s", pack)
	}
	if pack := string(renderPack(f.items["repo+app+2024q1"].pack, "")); strings.Contains(pack, "adr+app") {
		t.Errorf("renaming an ADR is not writing it:\n%s", pack)
	}
	agentPR := &packInput{Doc: &packDoc{Repo: "app", Changes: []packDocChange{{ID: "app#9", Class: Agent, Title: "docs: an ADR"}}}}
	if pack := string(renderPack(agentPR, "")); !strings.Contains(pack, "- written in app#9 (agent) · docs: an ADR") {
		t.Errorf("an ADR's pack should say who wrote the changes it was written in:\n%s", pack)
	}
	check(t, "refs: by path where file names clash", (&Facts{raw: &Raw{Decisions: []collect.Decision{
		{Repo: "web", Path: "apps/a/docs/adr/0001-x.md"}, {Repo: "web", Path: "apps/b/docs/adr/0001-x.md"}, {Repo: "a+b", Path: "docs/adr/0001-x.md"},
	}}}).decisionRefs(), []string{"adr+web+apps%2Fa%2Fdocs%2Fadr%2F0001-x", "adr+web+apps%2Fb%2Fdocs%2Fadr%2F0001-x", "adr+a%2Bb+0001-x"})
}

// A doc is substantial (a document of the user's, and a work item) when it is long enough, at
// least half its versions are the user's, and they created it or saved 3 versions or more.
func TestSubstantial(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		words, versions, mine int
		created               bool
		wantShare             int
		wantSubstantial       bool
	}{
		{"created, long enough, half the versions", 300, 2, 1, true, 50, true},
		{"one word short", 299, 2, 1, true, 50, false},
		{"created, but mostly others'", 1000, 3, 1, true, 33, false},
		{"not created, 3 versions, most of them", 300, 4, 3, false, 75, true},
		{"not created, every version, but 2", 300, 2, 2, false, 100, false},
		{"no versions", 300, 0, 0, true, 0, false},
	} {
		f := &Facts{docs: map[string]*Doc{}}
		d := &Doc{ID: "x", Words: tc.words, Versions: tc.versions, MyVersions: tc.mine, CreatedByMe: tc.created}
		f.addDoc(d, substantialWords)
		check(t, tc.name, []any{d.Share, d.Substantial}, []any{tc.wantShare, tc.wantSubstantial})
	}
	d := &Doc{ID: "adr", Words: decisionWords, Versions: 1, MyVersions: 1, CreatedByMe: true}
	(&Facts{docs: map[string]*Doc{}}).addDoc(d, decisionWords)
	check(t, "a decision record, at its own length", d.Substantial, true)
}

// Renaming or copying a decision record without changing it (renumbering a directory, say) is not
// a version of it.
func TestRenameOnly(t *testing.T) {
	for _, tc := range []struct {
		status     string
		similarity *int
		want       bool
	}{
		{"R", ptr(100), true}, {"C", ptr(100), true}, {"R", ptr(95), false}, {"R", nil, false}, {"M", nil, false}, {"A", nil, false},
	} {
		if got := renameOnly(collect.DecisionVersion{Status: tc.status, Similarity: tc.similarity}); got != tc.want {
			t.Errorf("renameOnly(%s, %v) = %v, want %v", tc.status, deref(tc.similarity), got, tc.want)
		}
	}
}

// A commit to a decision record is the user's work when they wrote it, or an AI agent did in their
// PR or crediting them; a colleague's commit in their PR is not.
func TestDecisionVersionMine(t *testing.T) {
	f := &Facts{people: newPeople(collect.IdentityConfig{Emails: []string{"jane@example.com"}})}
	claude := collect.Signature{Name: "Claude", Email: "noreply@anthropic.com"}
	for _, tc := range []struct {
		name   string
		author collect.Signature
		roles  []string
		want   bool
	}{
		{"the user's", jane, []string{"author", "committer"}, true},
		{"an agent's, in the user's PR", claude, []string{"pr-author"}, true},
		{"an agent's, crediting the user", claude, []string{"co-author"}, true},
		{"a colleague's, in the user's PR", other, []string{"pr-author"}, false},
		{"a colleague's, crediting the user", other, []string{"co-author"}, false},
		{"one the user only committed", other, []string{"committer"}, false},
		{"an agent's, nothing to do with the user", claude, nil, false},
	} {
		if got := f.decisionVersionMine(collect.DecisionVersion{Author: tc.author, Roles: tc.roles}); got != tc.want {
			t.Errorf("decisionVersionMine(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A page's doc: its tree, labels and status as the wiki has them, the issues it names that are in
// the dataset, and the user's creation and dated versions as their activity.
func TestPageDoc(t *testing.T) {
	raw := oneRepo(nil, nil, []collect.IssueRecord{issueRec("ENG-1", 0, nil)})
	raw.Pages = []collect.PageRecord{{Page: wiki.Page{ID: "7", Type: "page", Subtype: ptr("live"), Status: "current", Title: "Runbook",
		Space: wiki.Space{Key: "OPS", Name: "Operations"}, Ancestors: []wiki.PageRef{{Title: "Home"}, {Title: "Runbooks"}},
		CreatedAt: "2024-03-01T00:00:00Z", Body: ptr("one two three"),
		Versions: []wiki.Version{{Number: 1, ByUser: true, CreatedAt: "2024-03-05T00:00:00Z"}, {Number: 2, ByUser: true, CreatedAt: "2024-03-01T00:00:00Z"},
			{Number: 3, ByUser: true}, {Number: 4}}},
		Roles: []string{"editor"}, UserVersions: 3, IssueKeys: []string{"eng-1", "ENG-404"}}}
	raw.Decisions = []collect.Decision{{Repo: "app", Path: "docs/adr/0001-x.md", Body: "We use queues."}}
	f := build(t, raw, Inputs{})
	d := f.docs["7"]
	check(t, "a page", []any{d.Ref, d.Live, d.Space, d.Ancestors, d.Labels, d.Words, d.Keys, d.CreatedByMe, d.Versions, d.MyVersions, d.Share},
		[]any{"doc+7", true, "Operations", []string{"Home", "Runbooks"}, []string{}, 3, []string{"ENG-1"}, false, 4, 3, 75})
	check(t, "its days, from the user's dated versions", []any{d.First, d.Last, kinds(d.evs)},
		[]any{"2024-03-01", "2024-03-05", []string{evPageVersion, evActive, evPageVersion, evActive}})
	check(t, "bodies: a page's, and a decision record's", []string{d.body(), f.docs["app/docs/adr/0001-x.md"].body()},
		[]string{"one two three", "We use queues."})
}
