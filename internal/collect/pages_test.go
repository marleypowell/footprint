package collect

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/marleypowell/footprint/internal/wiki"
)

// Without a wiki in the config, the pages stage does nothing.
func TestPagesWithoutWiki(t *testing.T) {
	raw := t.TempDir()
	h := &harness{raw: raw, tools: Tools{}}
	if err := h.pages(context.Background()); err != nil || fileExists(filepath.Join(raw, "wiki")) {
		t.Errorf("err %v; wiki/ written: %v", err, fileExists(filepath.Join(raw, "wiki")))
	}
}

// A page's roles come from who created and owns it, and who saved its versions. Saving the first
// version is creating it, not editing it.
func TestPageInvolvement(t *testing.T) {
	jane, other := &wiki.User{ID: "u-jane"}, &wiki.User{ID: "u-other"}
	v := func(n int, author *wiki.User) wiki.Version { return wiki.Version{Number: n, Author: author} }
	tests := []struct {
		name     string
		page     wiki.Page
		roles    []string
		versions int
		marked   []bool
	}{
		{"created it, never edited", wiki.Page{CreatedBy: jane, Versions: []wiki.Version{v(1, jane), v(2, other)}},
			[]string{"creator"}, 1, []bool{true, false}},
		{"edited someone else's page", wiki.Page{CreatedBy: other, Versions: []wiki.Version{v(1, other), v(2, jane), v(3, jane)}},
			[]string{"editor"}, 2, []bool{false, true, true}},
		{"created and later edited", wiki.Page{CreatedBy: jane, Versions: []wiki.Version{v(1, jane), v(2, other), v(3, jane)}},
			[]string{"creator", "editor"}, 2, []bool{true, false, true}},
		{"owns a page someone else wrote", wiki.Page{CreatedBy: other, Owner: jane, Versions: []wiki.Version{v(1, other)}},
			[]string{"owner"}, 0, []bool{false}},
		// The wiki listed it as the user's, but the history kept (a whiteboard's current version) does not show them.
		{"nothing kept shows the user", wiki.Page{CreatedBy: other, Versions: []wiki.Version{v(5, other)}},
			[]string{"contributor"}, 0, []bool{false}},
		{"unknown authors (deleted accounts)", wiki.Page{Versions: []wiki.Version{v(1, nil), v(2, jane)}},
			[]string{"editor"}, 1, []bool{false, true}},
	}
	for _, tt := range tests {
		roles, versions := pageInvolvement(&tt.page, "u-jane")
		var byUser []bool
		for _, v := range tt.page.Versions {
			byUser = append(byUser, v.ByUser)
		}
		if !reflect.DeepEqual(roles, tt.roles) || versions != tt.versions || !reflect.DeepEqual(byUser, tt.marked) {
			t.Errorf("%s: pageInvolvement = %q, %d (by_user %v), want %q, %d (by_user %v)",
				tt.name, roles, versions, byUser, tt.roles, tt.versions, tt.marked)
		}
	}
}

// pages writes one file per space, oldest content first, named so any space key is a safe file
// name, and removes the files of spaces the user no longer has content in.
func TestPages(t *testing.T) {
	jane := &wiki.User{ID: "u-jane"}
	eng, personal := wiki.Space{Key: "eng", Name: "Engineering"}, wiki.Space{Key: "~557058:ab-c", Name: "Jane Doe"}
	w := &fakeWiki{account: wiki.Account{ID: "u-jane", Email: "jane.doe@example.com"}, pages: []wiki.Page{
		{ID: "p3", Title: "Runbook", Space: eng, CreatedAt: "2024-03-01T00:00:00Z", CreatedBy: jane},
		{ID: "p2", Title: "ENG-7 design", Space: eng, CreatedAt: "2024-01-01T00:00:00Z", CreatedBy: jane, Body: ptr("See eng-8.")},
		{ID: "p1", Title: "Notes", Space: eng, CreatedAt: "2024-01-01T00:00:00Z", CreatedBy: jane},
		{ID: "p4", Title: "Scratch", Space: personal, CreatedBy: jane},
	}}
	h := newTestHarness(t, Tools{Wiki: w, Tracker: &fakeTracker{}})
	writeRows(t, h, "wiki/old.jsonl", PageRecord{})
	if err := h.pages(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := readJSONL[PageRecord](filepath.Join(h.raw, "wiki", "eng.jsonl"))
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	if want := []string{"p1", "p2", "p3"}; err != nil || !reflect.DeepEqual(ids, want) {
		t.Fatalf("eng.jsonl = %q, %v, want %q (by created_at, then id)", ids, err, want)
	}
	if want := []string{"ENG-7", "ENG-8"}; !reflect.DeepEqual(rows[1].IssueKeys, want) {
		t.Errorf("issue keys = %q, want %q (title and body)", rows[1].IssueKeys, want)
	}
	if !fileExists(filepath.Join(h.raw, "wiki", "~557058_ab-c.jsonl")) {
		t.Error("a personal space's key should become a safe file name")
	}
	if fileExists(filepath.Join(h.raw, "wiki", "old.jsonl")) {
		t.Error("a space without the user's content kept its file")
	}
}
