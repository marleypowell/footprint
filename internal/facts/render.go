package facts

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/marleypowell/footprint/internal/filekind"
)

// metrics label the Totals fields, by their names in facts.json, in the order overviews show them.
var metrics = []struct{ name, label string }{
	{"changes_landed", "Changes landed (PRs, migrated PRs and commits)"},
	{"prs_opened", "PRs opened"},
	{"prs_merged", "PRs merged"},
	{"migrated_prs_merged", "PRs merged on the host repos were migrated from"},
	{"merged_for_others", "Others' PRs you completed there"},
	{"commits", "Your commits, one per patch"},
	{"commits_co_authored", "Others' commits naming you as co-author"},
	{"agent_commits", "AI agents' commits in your work"},
	{"lines_added", "Lines added, landed"},
	{"lines_deleted", "Lines deleted, landed"},
	{"repos", "Repos with landed changes"},
	{"issues_done", "Issues done by you"},
	{"issues_worked", "Issues you worked on"},
	{"issues_raised", "Issues you raised"},
	{"pages_created", "Wiki pages created"},
	{"pages_edited", "Wiki pages edited"},
	{"docs_written", "Substantial docs written"},
	{"adrs_written", "Decision records (ADRs) among them"},
	{"prs_reviewed", "Others' PRs you reviewed (not bots')"},
	{"changes_requested", "Your reviews requesting changes"},
	{"review_comments", "Your comments on others' PRs"},
	{"authors_reviewed", "People whose PRs you reviewed"},
	{"bot_prs_reviewed", "Bots' PRs you reviewed"},
	{"items", "Work items"},
	{"active_days", "Active days"},
}

// yearColumns are the totals the by-year and by-quarter tables show.
var yearColumns = []string{"changes_landed", "prs_merged", "migrated_prs_merged", "commits", "prs_reviewed", "issues_done", "docs_written", "active_days"}

// values are a Totals by name.
func (t Totals) values() map[string]int {
	b, _ := json.Marshal(t)
	m := map[string]int{}
	_ = json.Unmarshal(b, &m)
	return m
}

func (f *Facts) overview(w Window) []byte {
	s := f.Summary(w)
	items := f.windowItems(w)
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	scope := "all time"
	if !w.IsAll() {
		scope = w.String()
	}
	p("# Your work: %s\n\n", scope)
	p("Built by `footprint facts` from the collected dataset; README.md has the rules. A name in backticks is\n")
	p("the number's key in facts.json. Each work item's evidence is in packs/<id>.md (the all-time facts\n")
	p("directory), and its note, once written, in the notes directory.\n\n")

	p("## Coverage\n\n")
	src := s.Coverage.Sources
	p("- Counted: %s..%s, from %s (code host), %s (catalog)", s.Coverage.First, s.Coverage.Last, src.CodeHost, src.Catalog)
	if src.IssueTracker != nil {
		p(", %s (issue tracker)", *src.IssueTracker)
	}
	if src.Wiki != nil {
		p(", %s (wiki)", *src.Wiki)
	}
	p(".\n")
	if s.Coverage.PRsFrom != "" {
		p("- PR records start on %s. Earlier PRs, merged on the host repos were migrated from, count as\n", s.Coverage.PRsFrom)
		p("  `migrated_prs_merged`, from their squash commits.\n")
	}
	ex := s.Excluded
	p("- Line counts leave out generated files (%d file changes, +%d -%d) and single file changes of more\n", ex.Generated.Files, ex.Generated.Added, ex.Generated.Deleted)
	p("  than %d lines (%d, +%d -%d), all time.\n", filekind.BulkLines, ex.Bulk.Files, ex.Bulk.Added, ex.Bulk.Deleted)
	p("- Not collected, so their absence here says nothing: %s.\n\n", strings.Join(f.notCollected(), "; "))

	p("## Totals\n\n| | | name |\n|---|--:|---|\n")
	values := s.Totals.values()
	for _, m := range metrics {
		p("| %s | %d | `%s` |\n", m.label, values[m.name], m.name)
	}
	periods, what := s.ByYear, "year"
	if !w.IsAll() {
		periods, what = s.ByQuarter, "quarter"
	}
	if len(periods) > 0 {
		p("\n## By %s\n\n| %s |", what, what)
		for _, c := range yearColumns {
			p(" `%s` |", c)
		}
		p("\n|---|%s\n", strings.Repeat("--:|", len(yearColumns)))
		for _, per := range periods {
			v := per.Totals.values()
			p("| %s |", per.Period)
			for _, c := range yearColumns {
				p(" %d |", v[c])
			}
			p("\n")
		}
	}
	if len(s.TeamSignals) > 0 {
		p("\n## Team signals\n\nBy quarter, where the issues you worked on or finished were then: the boards whose sprints held\n")
		p("them, their projects, and their team field; with the issues in each. Hints for profile.md, not facts.\n\n")
		p("| quarter | boards | projects | teams |\n|---|---|---|---|\n")
		list := func(ss []Signal) string {
			var parts []string
			for _, s := range ss {
				name := s.Name
				if s.Title != "" {
					name += " " + s.Title
				}
				parts = append(parts, fmt.Sprintf("%s (%d)", name, s.Issues))
			}
			return cell(strings.Join(parts, ", "))
		}
		for _, ts := range s.TeamSignals {
			p("| %s | %s | %s | %s |\n", ts.Period, list(ts.Boards), list(ts.Projects), list(ts.Teams))
		}
	}

	var initiatives, others []*Item
	for _, it := range items {
		if it.Kind == KindInitiative {
			initiatives = append(initiatives, it)
		} else {
			others = append(others, it)
		}
	}
	byDays := func(list []*Item) {
		slices.SortStableFunc(list, func(a, b *Item) int {
			return cmp.Or(cmp.Compare(b.ActiveDays, a.ActiveDays), cmp.Compare(a.First, b.First), cmp.Compare(a.ID, b.ID))
		})
	}
	byDays(initiatives)
	if len(initiatives) > 0 {
		p("\n## Initiatives\n\nEpics and above, with the features under them; by your active days.\n\n")
		p("| id | title | span | features | days | note |\n|---|---|---|--:|--:|---|\n")
		for _, it := range initiatives {
			p("| %s | %s | %s..%s | %d | %d | %s |\n", it.ID, cell(it.Title), it.First, it.Last, len(it.Features), it.ActiveDays, it.Note)
		}
	}
	byDays(others)
	p("\n## Work items\n\nFeatures (an issue with its sub-tasks), docs, and per-quarter buckets of work naming no issue;\n")
	p("by effort (all-time active days: XL 31+, L 11-30, M 4-10, S 2-3, XS 1), then by days here.\n")
	small := 0
	for _, tier := range []string{"XL", "L", "M"} {
		var list []*Item
		for _, it := range others {
			if it.Effort == tier {
				list = append(list, it)
			}
		}
		if len(list) == 0 {
			continue
		}
		p("\n### %s\n\n", tier)
		for _, it := range list {
			p("- %s · %s · %s, %s..%s, %d days", it.ID, it.Title, it.Kind, it.First, it.Last, it.ActiveDays)
			if it.Initiative != nil {
				p(" · in %s", it.Initiative.ID)
			}
			if len(it.Repos) > 0 {
				p(" · %s", strings.Join(it.Repos, ", "))
			}
			p(" · note: %s\n", it.Note)
		}
	}
	for _, it := range others {
		if effortRank(it.Effort) > effortRank("M") {
			small++
		}
	}
	if small > 0 {
		p("\nAnd %d smaller items (S and XS), in items.jsonl.\n", small)
	}

	p("\n## Repos\n\n| repo | systems | span | `changes_landed` | `lines_added` | kinds of file |\n|---|---|---|--:|--:|---|\n")
	for _, r := range f.Repos {
		t := totalsOf(f.repoEvents(r.Name), w)
		first, last := span(f.repoEvents(r.Name), w)
		if first == "" {
			continue
		}
		var exts []string
		for _, e := range r.Extensions[:min(5, len(r.Extensions))] {
			exts = append(exts, e.Ext)
		}
		started := ""
		if r.StartedByMe {
			started = " (you started it)"
		}
		p("| %s%s | %s | %s..%s | %d | %d | %s |\n", r.Name, started, cell(strings.Join(r.Systems, ", ")), first, last,
			t.ChangesLanded, t.LinesAdded, strings.Join(exts, " "))
	}

	var docs []*Doc
	for _, d := range f.Docs {
		if d.Substantial && f.docIn(d, w) {
			docs = append(docs, d)
		}
	}
	if len(docs) > 0 {
		p("\n## Docs\n\nSubstantial docs you wrote, mostly yours: wiki pages of %d+ words, and decision records (ADRs) of %d+.\n\n",
			substantialWords, decisionWords)
		for _, d := range docs {
			if d.Repo != "" {
				p("- %s · %s · ADR in %s, created %s, %d%% of commits yours\n", d.Ref, d.Title, d.Repo, dayOf(d.CreatedAt), d.Share)
			} else {
				p("- %s · %s · %s, created %s, %d%% of versions yours\n", d.Ref, d.Title, d.Space, dayOf(d.CreatedAt), d.Share)
			}
		}
	}

	n := s.Notes
	p("\n## Notes\n\n%d items here need a note (effort M or more, and docs): %d current, %d stale, %d missing,\n", n.Required, n.Current, n.Stale, n.Missing)
	p("%d invalid, %d renamed. todo.txt lists them. %d notes name no item.\n", n.Invalid, n.Renamed, n.Orphans)
	return []byte(b.String())
}

// cell makes text safe in a Markdown table cell.
func cell(s string) string { return strings.ReplaceAll(oneLine(s), "|", "\\|") }

func (f *Facts) repoEvents(repo string) []event {
	var evs []event
	for _, e := range f.events {
		if e.repo == repo {
			evs = append(evs, e)
		}
	}
	return evs
}

// docIn says whether the user saved a version of d in w.
func (f *Facts) docIn(d *Doc, w Window) bool {
	for _, e := range d.evs {
		if (e.kind == evPageVersion || e.kind == evADRVersion) && w.Contains(e.at) {
			return true
		}
	}
	return false
}

// catalogue is every item in date order, as standalone text with its note: the record that
// outlives access to the tools.
func (f *Facts) catalogue() []byte {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	s := f.Summary(All)
	p("# Catalogue of work, %s..%s\n\n", s.Coverage.First, s.Coverage.Last)
	p("Every work item, oldest first, with its note where one is written. Generated by `footprint facts`;\n")
	p("edit the notes, not this file.\n")
	year := ""
	for _, it := range f.Items {
		if y := it.First[:min(4, len(it.First))]; y != year {
			year = y
			p("\n## %s\n", year)
		}
		p("\n### %s · %s\n\n", it.ID, it.Title)
		p("%s..%s · %s", it.First, it.Last, it.Kind)
		if it.Type != "" {
			p(" (%s)", it.Type)
		}
		if it.Initiative != nil {
			p(" · in %s %s", it.Initiative.ID, it.Initiative.Title)
		}
		if len(it.Repos) > 0 {
			p(" · repos: %s", strings.Join(it.Repos, ", "))
		}
		p(" · effort %s, %d active days\n", it.Effort, it.ActiveDays)
		t := it.Totals
		var counts []string
		for _, c := range []struct {
			n    int
			what string
		}{{t.PRsMerged, "PRs merged"}, {t.MigratedPRsMerged, "migrated PRs merged"}, {t.Commits, "commits"},
			{t.IssuesDone, "issues done"}, {t.PagesEdited, "pages edited"}} {
			if c.n > 0 {
				counts = append(counts, fmt.Sprintf("%d %s", c.n, c.what))
			}
		}
		if it.Subtasks != nil {
			counts = append(counts, fmt.Sprintf("%d of %d sub-tasks done by you", it.Subtasks.DoneByMe, it.Subtasks.Total))
		}
		if len(counts) > 0 {
			p("%s.\n", strings.Join(counts, ", "))
		}
		if n := f.notes[itemPath(it.ID)]; n != nil && n.err == "" {
			p("\n%s\n", demote(strings.TrimSpace(n.body)))
		} else if required(it) {
			p("\n(No note yet.)\n")
		}
	}
	return []byte(b.String())
}

// demote moves a note's headings down two levels, under the catalogue's.
func demote(body string) string {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "#") {
			lines[i] = "##" + l
		}
	}
	return strings.Join(lines, "\n")
}

// todo lists the notes to write or fix for the items in w: required items whose note is not
// current, and any invalid note; then notes naming no item. Tab-separated; empty when done.
func (f *Facts) todo(w Window) []byte {
	var rows []*Item
	for _, it := range f.windowItems(w) {
		if it.Note == NoteInvalid || (required(it) && it.Note != NoteCurrent) {
			rows = append(rows, it)
		}
	}
	slices.SortStableFunc(rows, func(a, b *Item) int {
		return cmp.Or(cmp.Compare(effortRank(a.Effort), effortRank(b.Effort)), cmp.Compare(a.First, b.First), cmp.Compare(a.ID, b.ID))
	})
	var b strings.Builder
	for _, it := range rows {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s..%s\t%s\n", it.Note, it.ID, it.Effort, it.First, it.Last, oneLine(it.Title))
	}
	for _, name := range f.orphans {
		fmt.Fprintf(&b, "%s\t%s.md\n", NoteOrphan, name)
	}
	return []byte(b.String())
}
