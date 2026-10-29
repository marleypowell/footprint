package facts

import (
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/catalog"
	"github.com/marleypowell/footprint/internal/collect"
)

// blamed adds a blame of the fixture's repo to raw: files with the user's commits (those of the
// fixture's changes, a co-authored one, and one no change explains) and others' lines, in
// directories sized to exercise the roles.
func blamed(raw *Raw) *Raw {
	raw.Ownership = []collect.Ownership{{
		Repo: "app", DefaultBranch: "main", Head: "h", HeadAt: "2024-06-30T10:00:00Z", Lines: 1000,
		Dirs: []collect.OwnedDir{{Path: "src/", Files: 1, Lines: 200}, {Path: "lib/", Files: 1, Lines: 200}, {Path: "docs/", Files: 1, Lines: 100},
			{Path: "src/api/", Files: 1, Lines: 300}, {Path: "(root)", Files: 2, Lines: 500}},
		Files: []collect.OwnedFile{
			{Path: "src/a.go", Lines: 175, Commits: []collect.OwnedCommit{{SHA: "c1", Lines: 120}, {SHA: "c4", Lines: 5}, {SHA: "c8", Lines: 4}, {SHA: "ffff", Lines: 2}},
				OtherLines: 44, Others: []collect.OwnedAuthor{
					{Name: "Sam", Email: "sam@example.com", Lines: 30, LastAt: "2024-05-01T10:00:00Z"},
					{Name: "Sam Smith", Email: "SAM@example.com", Lines: 8, LastAt: "2024-06-01T10:00:00Z"},
					{Name: "Claude", Email: "noreply@anthropic.com", Lines: 3, LastAt: "2024-05-02T10:00:00Z"},
					{Name: "renovate[bot]", Email: "bot@example.com", Lines: 2, LastAt: "2024-05-03T10:00:00Z"},
					{Name: "Jane Doe", Email: "jane@example.com", Lines: 1, LastAt: "2024-05-04T10:00:00Z"},
				}},
			{Path: "lib/b.go", Lines: 50, Commits: []collect.OwnedCommit{{SHA: "c7", Lines: 50}}},
			{Path: "docs/x.md", Lines: 60, Commits: []collect.OwnedCommit{{SHA: "c7", Lines: 60}}},
		},
	}}
	raw.Index.Repos[0].Files = map[string]string{"ownership": "repos/app/ownership.json"}
	raw.Projects[0].Catalog.Entities = []catalog.Entity{{Ref: "component:default/api", Category: catalog.Component, Title: ptr("The API"),
		Path: "src/catalog-info.yaml", FoundInRepo: "app", SourceRepo: ptr("app")}}
	return raw
}

// The lines blame names go to the user's changes by the commits that made them: a change's living
// lines are those of its commits, the landed ones add to the totals, and a commit that only
// names the user as co-author, or that no change explains, is counted apart.
func TestOwnershipLivingLines(t *testing.T) {
	f := build(t, blamed(fixture()), Inputs{})
	living := map[string]*int{}
	for _, c := range f.Changes {
		living[c.ID] = c.Lines.Living
	}
	check(t, "a commit's change", deref(living["app@c1"]), 120)
	check(t, "a PR gets its commits' lines", deref(living["app#5"]), 5)
	check(t, "a migrated PR: both files", deref(living["app@c7"]), 110)
	check(t, "a landed change with none living has none", deref(living["app@c11"]), 0)
	check(t, "a change that only names the user as co-author has no living lines", living["app@c8"], (*int)(nil))

	all := f.Summary(All)
	check(t, "lines living, of the landed changes", all.Totals.LinesLiving, 120+5+110)
	s := all.Ownership
	check(t, "co-authored, unattributed and the user's own others", []int{s.CoAuthoredLiving, s.UnattributedLines, s.UnmatchedSelfLines}, []int{4, 2, 1})
	check(t, "the summary", []any{s.Repos, s.Files, s.AsOfFirst, s.AsOfLast, s.LinesLiving}, []any{1, 3, "2024-06-30", "2024-06-30", 235})
	check(t, "survival: living over what the landed changes added", s.SurvivalPercent, pct(235, s.LinesAdded))

	jan, _ := NewWindow("2024-01-01", "2024-01-04")
	check(t, "a window counts the changes that landed in it", f.Summary(jan).Totals.LinesLiving, 120)

	r := f.Repos[0].Ownership
	check(t, "the repo's blame", []any{r.AsOf, r.Lines, r.Living, r.SharePercent}, []any{"2024-06-30", 1000, 235, 24})
}

func TestOwnershipWithoutTheStage(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	if f.Summary(All).Ownership != nil || f.Repos[0].Ownership != nil || len(f.Areas) != 0 {
		t.Error("a dataset the stage never ran in has ownership facts")
	}
	for _, c := range f.Changes {
		if c.Lines.Living != nil {
			t.Errorf("%s has living lines without a blame", c.ID)
		}
	}
	check(t, "no lines living", f.Summary(All).Totals.LinesLiving, 0)
}

// An area is a repo, a catalog entity's directory or a directory, where the user has living lines.
// The user's share of its text lines gives the role, from 200 lines up: half is top, a fifth major.
// The others are the colleagues with most lines in the files the user changed there: one person
// by email, whatever case; agents, bots and the user's own identities are no one's to ask.
func TestOwnershipAreas(t *testing.T) {
	f := build(t, blamed(fixture()), Inputs{})
	areas := map[string]*Area{}
	for _, a := range f.Areas {
		areas[a.ID] = a
	}
	tests := []struct {
		id         string
		kind, role string
		living     int
		share      int
	}{
		{"own+app", "repo", RoleMajor, 235, 24},
		{"own+app+src/", "entity", RoleTop, 125, 63}, // 200 lines, 125 the user's
		{"own+app+lib/", "dir", RoleMajor, 50, 25},
		{"own+app+docs/", "dir", RoleContributor, 60, 60}, // over half, but too small an area to own
	}
	for _, tt := range tests {
		a := areas[tt.id]
		if a == nil {
			t.Errorf("no area %s", tt.id)
			continue
		}
		if a.Kind != tt.kind || a.Role != tt.role || a.Living != tt.living || a.SharePercent != tt.share {
			t.Errorf("%s = %s %s living %d share %d, want %s %s living %d share %d", tt.id, a.Kind, a.Role, a.Living, a.SharePercent, tt.kind, tt.role, tt.living, tt.share)
		}
	}
	if _, ok := areas["own+app+src/api/"]; ok {
		t.Error("an area where the user has no lines is listed")
	}
	if _, ok := areas["own+app+(root)"]; ok {
		t.Error("the root has no lines of the user's in this blame")
	}
	e := areas["own+app+src/"]
	check(t, "an entity area names its entity", e.Entity, &Ref{ID: "component:default/api", Title: "The API"})
	check(t, "colleagues, one per email, without agents, bots or the user", e.Others, []AreaPerson{{Name: "Sam", Lines: 38, Last: "2024-06-01"}})
	check(t, "its changes", e.Changes, 2) // c1 and PR 5

	s := f.Summary(All).Ownership
	check(t, "areas by role (the repo's own does not count)", []int{s.AreasTop, s.AreasMajor}, []int{1, 1})
	check(t, "ordered: the repo's own first, then by path", []string{f.Areas[0].ID, f.Areas[1].ID, f.Areas[2].ID, f.Areas[3].ID},
		[]string{"own+app", "own+app+docs/", "own+app+lib/", "own+app+src/"})
}

// A draft cites an area as [[own+repo+path/]]; its numbers, and the summary's, are in scope.
func TestOwnershipCitations(t *testing.T) {
	f := build(t, blamed(fixture()), Inputs{})
	front := "---\nwindow: all\n---\n"
	var findings []string
	for _, fd := range f.Check("draft.md", front+"The user wrote 125 of the 200 lines of the API [[own+app+src/]], 63% of it.\n") {
		findings = append(findings, fd.String())
	}
	if len(findings) != 0 {
		t.Errorf("an area's numbers are in scope for its citation: %v", findings)
	}
	fd := f.Check("draft.md", front+"They own [[own+app+nowhere/]].\n")
	if len(fd) != 1 || !fd[0].Error || !strings.Contains(fd[0].Message, "names nothing") {
		t.Errorf("a citation of an area that does not exist: %v", fd)
	}
	s := f.Summary(All).Ownership
	if fd := f.Check("draft.md", front+"About "+itoa(s.LinesLiving)+" lines are still live, "+itoa(s.SurvivalPercent)+"%.\n"); len(fd) != 0 {
		t.Errorf("the summary's numbers are in scope: %v", fd)
	}
}
