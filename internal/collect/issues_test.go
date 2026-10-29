package collect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/codehost"
	"github.com/marleypowell/footprint/internal/doc"
	"github.com/marleypowell/footprint/internal/tracker"
)

// When the tracker loads none of the issues, the stage fails and keeps the previous files.
func TestIssuesKeepsDataWhenNothingLoads(t *testing.T) {
	raw := t.TempDir()
	previous := filepath.Join(raw, "issues", "ENG.jsonl")
	if err := writeFileAtomic(previous, []byte(`{"key":"ENG-1"}`+"\n")); err != nil {
		t.Fatal(err)
	}
	tr := &fakeTracker{account: tracker.Account{ID: "u-jane"}, assigned: []string{"ENG-1", "ENG-2"}}
	h := &harness{raw: raw, tools: Tools{Tracker: tr}, id: &identity{emails: map[string]bool{}}}
	if err := h.issues(context.Background()); err == nil || !strings.Contains(err.Error(), "none of the 2 issues") {
		t.Errorf("want a refusal, got %v", err)
	}
	if !fileExists(previous) {
		t.Error("the previous issues were removed")
	}
}

func TestNaturalCompare(t *testing.T) {
	keys := []string{"ENG-10", "ENG-9", "AB-100", "ENG-009", "ENG-1a", "ENG-1"}
	slices.SortFunc(keys, NaturalCompare)
	if want := []string{"AB-100", "ENG-1", "ENG-1a", "ENG-009", "ENG-9", "ENG-10"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("got %v, want %v", keys, want)
	}
}

func TestFileName(t *testing.T) {
	for key, want := range map[string]string{
		"ENG": "ENG", "~557058:ab-c": "~557058_ab-c", "": "_", "..": "_..", "a/../b": "a_.._b",
		"CON": "_CON", "aux": "_aux", "COM1": "_COM1", "LPT9": "_LPT9", "NUL.x": "_NUL.x",
		"CONF": "CONF", "COM": "COM", "COM10": "COM10", "PRNT": "PRNT",
	} {
		if got := fileName(key); got != want {
			t.Errorf("fileName(%q) = %q, want %q", key, got, want)
		}
	}
}

// recordingTracker records the keys each Issues call asks for.
type recordingTracker struct {
	*fakeTracker
	asked [][]string
}

func (r *recordingTracker) Issues(ctx context.Context, keys []string) ([]tracker.Issue, []tracker.Missing, error) {
	r.asked = append(r.asked, keys)
	return r.fakeTracker.Issues(ctx, keys)
}

// failingKeysTracker cannot list its projects, so it cannot say what its keys look like.
type failingKeysTracker struct{ *fakeTracker }

var errTrackerDown = errors.New("503 Service Unavailable")

func (failingKeysTracker) Keys(context.Context) (tracker.KeyFunc, error) { return nil, errTrackerDown }

func TestIssueKeysWithoutATracker(t *testing.T) {
	keys, err := (&harness{}).issueKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := keys("ENG-1 and eng-2"); got == nil || len(got) != 0 {
		t.Errorf("keys without a tracker = %#v, want an empty, non-nil list", got)
	}
}

// A tracker that cannot list its projects fails the stage, saying how to collect without it.
func TestIssueKeysExplainsATrackerFailure(t *testing.T) {
	h := &harness{tools: Tools{Tracker: failingKeysTracker{&fakeTracker{}}}}
	_, err := h.issueKeys(context.Background())
	if !errors.Is(err, errTrackerDown) || !strings.Contains(err.Error(), "remove issue_tracker from the config") {
		t.Errorf("issueKeys() = %v, want the cause, and how to do without the tracker", err)
	}
}

// Without an issue tracker in the config, the issues stage does nothing.
func TestIssuesWithoutTracker(t *testing.T) {
	h := newTestHarness(t, Tools{})
	if err := h.issues(context.Background()); err != nil || fileExists(filepath.Join(h.raw, "issues")) {
		t.Errorf("err %v; issues/ written: %v", err, fileExists(filepath.Join(h.raw, "issues")))
	}
}

// An issue's roles are those its fields and history show, plus those found elsewhere (known), in
// issueRoles order. Being made the assignee in the history counts even if someone else has it now.
func TestIssueInvolvement(t *testing.T) {
	jane, other := &tracker.User{ID: "u-jane"}, &tracker.User{ID: "u-other"}
	tests := []struct {
		name  string
		issue tracker.Issue
		known []string
		want  []string
	}{
		{"none", tracker.Issue{Assignee: other, Reporter: other}, nil, []string{}},
		{"fields", tracker.Issue{Assignee: jane, Reporter: jane, Creator: jane}, nil, []string{"assignee", "reporter", "creator"}},
		{"assigned in the history, since reassigned", tracker.Issue{Assignee: other,
			History: []tracker.Change{{Author: other, Items: []tracker.ChangeItem{{Field: "assignee", ToID: ptr("u-jane")}}}}},
			nil, []string{"assignee"}},
		{"assigning someone else is editing", tracker.Issue{
			History: []tracker.Change{{Author: jane, Items: []tracker.ChangeItem{{Field: "assignee", ToID: ptr("u-other")}}}}},
			nil, []string{"editor"}},
		{"a field other than assignee set to the user's id", tracker.Issue{
			History: []tracker.Change{{Author: other, Items: []tracker.ChangeItem{{Field: "reviewer", ToID: ptr("u-jane")}}}}},
			nil, []string{}},
		{"commented and logged work", tracker.Issue{
			Comments: []tracker.Comment{{Author: other}, {Author: jane}},
			Worklogs: []tracker.Worklog{{Author: jane}}},
			nil, []string{"commenter", "worklogger"}},
		{"known from the search and git work, in role order", tracker.Issue{Reporter: jane},
			[]string{"ancestor", "referenced", "assignee"}, []string{"assignee", "reporter", "referenced", "ancestor"}},
		{"unknown users (deleted accounts)", tracker.Issue{Comments: []tracker.Comment{{Author: nil}}}, nil, []string{}},
	}
	for _, tt := range tests {
		if got := issueInvolvement(&tt.issue, "u-jane", tt.known...); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: issueInvolvement = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestIssueInvolvementMarksTheUsersEntries(t *testing.T) {
	jane, other := &tracker.User{ID: "u-jane"}, &tracker.User{ID: "u-other"}
	issue := tracker.Issue{
		Comments: []tracker.Comment{{ID: "c1", Author: jane}, {ID: "c2", Author: other}},
		History:  []tracker.Change{{ID: "h1", Author: other}, {ID: "h2", Author: jane}},
		Worklogs: []tracker.Worklog{{ID: "w1", Author: jane}},
	}
	issueInvolvement(&issue, "u-jane")
	got := []bool{issue.Comments[0].ByUser, issue.Comments[1].ByUser, issue.History[0].ByUser, issue.History[1].ByUser, issue.Worklogs[0].ByUser}
	if want := []bool{true, false, false, true, true}; !reflect.DeepEqual(got, want) {
		t.Errorf("by_user on comments, changes, worklogs = %v, want %v", got, want)
	}
}

// loadAncestors walks up from the loaded issues, a level per call, asking for each key once.
func TestLoadAncestors(t *testing.T) {
	ref := func(key string) *tracker.IssueRef { return &tracker.IssueRef{Key: key} }
	loaded := []tracker.Issue{
		{Key: "ENG-3", Parent: ref("ENG-2")},
		{Key: "ENG-4", Parent: ref("eng-2")}, // the same parent: asked for once
		{Key: "ENG-5", Parent: ref("ENG-3")}, // a parent already loaded: not asked for
		{Key: "ENG-50", PreviousKeys: []string{"OLD-50"}},
		{Key: "ENG-51", Parent: ref("OLD-50")}, // a parent already loaded, under its old key
		{Key: "ENG-6", Parent: ref("ENG-99")},  // a parent since deleted
		{Key: "ENG-60", Parent: ref("OLD-61")}, // the tracker answers OLD-61 with ENG-3, loaded already
	}
	tr := &recordingTracker{fakeTracker: &fakeTracker{issues: []tracker.Issue{
		{Key: "ENG-2", Parent: ref("OPS-1")},
		{Key: "OPS-1"},
		{Key: "ENG-3", PreviousKeys: []string{"OLD-61"}},
	}}}
	ancestors, missing, err := loadAncestors(context.Background(), tr, loaded)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, a := range ancestors {
		keys = append(keys, a.Key)
	}
	if !reflect.DeepEqual(keys, []string{"ENG-2", "OPS-1"}) {
		t.Errorf("ancestors = %q, want [ENG-2 OPS-1]", keys)
	}
	if want := [][]string{{"ENG-2", "ENG-99", "OLD-61"}, {"OPS-1"}}; !reflect.DeepEqual(tr.asked, want) {
		t.Errorf("asked for %q, want %q", tr.asked, want)
	}
	if want := []tracker.Missing{{Key: "ENG-99", Reason: tracker.NotFound}}; !reflect.DeepEqual(missing, want) {
		t.Errorf("missing = %+v, want %+v", missing, want)
	}
}

// The walk stops maxAncestorDepth levels up, whatever is above.
func TestLoadAncestorsDepth(t *testing.T) {
	var chain []tracker.Issue
	for i := 1; i <= maxAncestorDepth+2; i++ {
		chain = append(chain, tracker.Issue{Key: fmt.Sprintf("ENG-%d", i), Parent: &tracker.IssueRef{Key: fmt.Sprintf("ENG-%d", i+1)}})
	}
	tr := &fakeTracker{issues: chain[1:]}
	ancestors, _, err := loadAncestors(context.Background(), tr, chain[:1])
	if err != nil || len(ancestors) != maxAncestorDepth || ancestors[len(ancestors)-1].Key != fmt.Sprintf("ENG-%d", maxAncestorDepth+1) {
		t.Errorf("loadAncestors up a chain of %d = %d ancestors, %v; want %d", len(chain), len(ancestors), err, maxAncestorDepth)
	}
}

// Ancestors loaded only for context are counted apart from the issues involving the user.
func TestIssueTotals(t *testing.T) {
	withAncestors := doc.NewObject()
	withAncestors.Set("assignee", 3)
	withAncestors.Set("ancestor", 2)
	without := doc.NewObject()
	without.Set("referenced", 4)
	issues, ancestors := issueTotals([]IndexGroup{{Records: 5, ByRole: withAncestors}, {Records: 4, ByRole: without}})
	if issues != 7 || ancestors != 2 {
		t.Errorf("issueTotals = %d, %d, want 7 issues, 2 ancestors", issues, ancestors)
	}
}

// gitReferences finds where the user's commits and PRs name each issue key, keyed in upper case.
func TestGitReferences(t *testing.T) {
	h := newTestHarness(t, Tools{})
	writeRows(t, h, "repos/api/commits.jsonl", Commit{Repo: "api", SHA: "a1", IssueKeys: []string{"eng-1", "ENG-2"}})
	writeRows(t, h, "repos/api/prs.jsonl", PullRequestRecord{IssueKeys: []string{"ENG-1"}, PullRequest: codehost.PullRequest{Repo: "api", Number: 3}})
	writeRows(t, h, "repos/web/commits.jsonl", Commit{Repo: "web", SHA: "w1", IssueKeys: []string{"ENG-1"}})
	refs, err := h.gitReferences()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]*References{
		"ENG-1": {Commits: []CommitRef{{"api", "a1"}, {"web", "w1"}}, PRs: []PRRef{{"api", 3}}},
		"ENG-2": {Commits: []CommitRef{{"api", "a1"}}},
	}
	if !reflect.DeepEqual(refs, want) {
		t.Errorf("gitReferences = %s, want %s", mustJSON(t, refs), mustJSON(t, want))
	}
}

func TestReferencesNormalized(t *testing.T) {
	r := References{
		Commits: []CommitRef{{"web", "b"}, {"api", "z"}, {"api", "a"}, {"web", "b"}},
		PRs:     []PRRef{{"api", 10}, {"api", 9}, {"api", 10}},
	}
	want := References{Commits: []CommitRef{{"api", "a"}, {"api", "z"}, {"web", "b"}}, PRs: []PRRef{{"api", 9}, {"api", 10}}}
	if got := r.normalized(); !reflect.DeepEqual(got, want) {
		t.Errorf("normalized() = %+v, want %+v", got, want)
	}
	if got := (References{}).normalized(); got.Commits == nil || got.PRs == nil {
		t.Errorf("normalized() of nothing = %#v, want empty lists, not null", got)
	}
}

// checkAccount warns when the tool's token is not the configured user's.
func TestCheckAccount(t *testing.T) {
	h := newTestHarness(t, Tools{})
	tests := []struct {
		email string
		warns bool
	}{
		{" Jane.Doe@Example.com", false},
		{"other@example.com", true},
		{"", false}, // the tool hides the email: nothing to compare
	}
	for _, tt := range tests {
		out := captureStderr(t, func() { h.checkAccount("issues", "Someone", tt.email) })
		if got := strings.Contains(out, "WARNING"); got != tt.warns {
			t.Errorf("checkAccount(%q) warned %v, want %v: %q", tt.email, got, tt.warns, out)
		}
	}
}

// removeOtherJSONL removes the .jsonl files a stage did not write, and nothing else.
func TestRemoveOtherJSONL(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"ENG.jsonl", "GONE.jsonl", "unresolved.json", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeOtherJSONL(dir, map[string]bool{"ENG.jsonl": true}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	slices.Sort(left)
	if want := []string{"ENG.jsonl", "notes.txt", "unresolved.json"}; !reflect.DeepEqual(left, want) {
		t.Errorf("left %q, want %q", left, want)
	}
	if err := removeOtherJSONL(filepath.Join(dir, "missing"), nil); err != nil {
		t.Errorf("removeOtherJSONL(missing dir) = %v, want nil", err)
	}
}
