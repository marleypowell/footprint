package facts

import (
	"strings"
	"testing"
)

// A count is the totals every other file reports, for any slice of the events: it agrees with
// facts.json, items.jsonl and repos.jsonl wherever they report the same slice.
func TestCountAgreesWithTheFacts(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	q1 := mustWindow(t, "2024-01-01..2024-03-31")
	tests := []struct {
		name   string
		filter Filter
		want   Totals
	}{
		{"no filter is facts.json", Filter{}, f.Summary(All).Totals},
		{"a window is that window's facts.json", Filter{Window: q1}, f.Summary(q1).Totals},
		{"an item is its totals in items.jsonl", Filter{Item: "ENG-1"}, f.items["ENG-1"].Totals},
		{"an initiative includes its features'", Filter{Item: "ENG-10"}, f.items["ENG-10"].Totals},
		{"an item in a window is the window's items.jsonl", Filter{Window: q1, Item: "ENG-1"}, windowItem(t, f, q1, "ENG-1").Totals},
		{"a repo is its totals in repos.jsonl", Filter{Repo: "app"}, f.Repos[0].Totals},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.Count(tt.filter, "")
			if err != nil {
				t.Fatal(err)
			}
			check(t, "totals", got.Totals, tt.want)
			check(t, "groups", got.Groups, []Group[Totals]{})
		})
	}
}

// The filters combine, and say what they picked: the window as drafts name it, the item by its
// current id.
func TestCountFilters(t *testing.T) {
	raw := fixture()
	raw.Issues[1].PreviousKeys = []string{"OLD-1"} // ENG-1
	f := build(t, raw, Inputs{})
	jan := mustWindow(t, "2024-01-01..2024-01-31")
	tests := []struct {
		name             string
		filter           Filter
		window, item     string
		days, landed     int
		issuesDone, docs int
	}{
		{"a repo leaves out issue and wiki activity, which has none",
			Filter{Repo: "app"}, "all", "", 13, 3, 0, 0},
		{"an item by a key its issue had before",
			Filter{Item: "old-1"}, "all", "ENG-1", 5, 2, 2, 0},
		{"an item by its key in lower case",
			Filter{Item: "eng-1"}, "all", "ENG-1", 5, 2, 2, 0},
		{"a doc item counts the versions the user saved",
			Filter{Item: "doc+100"}, "all", "doc+100", 2, 0, 0, 1},
		{"an item in a repo, in a window",
			Filter{Window: jan, Repo: "app", Item: "ENG-1"}, "2024-01-01..2024-01-31", "ENG-1", 3, 2, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.Count(tt.filter, "")
			if err != nil {
				t.Fatal(err)
			}
			check(t, "window", got.Window, tt.window)
			check(t, "item", got.Item, tt.item)
			tot := got.Totals
			check(t, "active_days, changes_landed, issues_done, docs_written",
				[]int{tot.ActiveDays, tot.ChangesLanded, tot.IssuesDone, tot.DocsWritten}, []int{tt.days, tt.landed, tt.issuesDone, tt.docs})
		})
	}
}

// A breakdown by year or quarter is facts.json's by_year and by_quarter for the slice; by repo or
// item it leaves out the events with none, and lists the most active first.
func TestCountBreakdowns(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	all := f.Summary(All)
	names := func(gs []Group[Totals]) []string {
		var out []string
		for _, g := range gs {
			out = append(out, g.Name)
		}
		return out
	}
	tests := []struct {
		name   string
		filter Filter
		by     string
		want   []string
	}{
		{"by year", Filter{}, ByYear, []string{"2024"}},
		{"by quarter", Filter{}, ByQuarter, []string{"2024q1", "2024q2"}},
		{"an item by quarter: only the quarters it has events in", Filter{Item: "ENG-2"}, ByQuarter, []string{"2024q1"}},
		{"by repo", Filter{}, ByRepo, []string{"app"}},
		{"by item, most active days first, then most landed, then by id", Filter{}, ByItem,
			[]string{"ENG-1", "ENG-2", "adr+app+0001-use-queues", "doc+100", "repo+app+2024q2", "repo+app+2024q1"}},
		{"an initiative by item: itself and each feature", Filter{Item: "ENG-10"}, ByItem, []string{"ENG-1"}},
		{"by item in a window", Filter{Window: mustWindow(t, "2024-04-01..2024-06-30")}, ByItem, []string{"repo+app+2024q2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.Count(tt.filter, tt.by)
			if err != nil {
				t.Fatal(err)
			}
			check(t, "by", got.By, tt.by)
			check(t, "groups", names(got.Groups), tt.want)
		})
	}

	years, _ := f.Count(Filter{}, ByYear)
	quarters, _ := f.Count(Filter{}, ByQuarter)
	check(t, "by year totals", years.Groups[0].Totals, all.ByYear[0].Totals)
	check(t, "by quarter totals", quarters.Groups[1].Totals, all.ByQuarter[1].Totals)
	items, _ := f.Count(Filter{}, ByItem)
	check(t, "an item's group", items.Groups[0].Totals, f.items["ENG-1"].Totals)
}

func TestCountRejects(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	tests := []struct {
		name   string
		filter Filter
		by     string
		want   string
	}{
		{"an item the facts do not have", Filter{Item: "ENG-404"}, "", `no work item "ENG-404"`},
		{"an issue that is no item", Filter{Item: "ENG-3"}, "", `no work item "ENG-3"`},
		{"a repo the dataset does not have", Filter{Repo: "nope"}, "", `no repo "nope"`},
		{"an unknown breakdown", Filter{}, "month", `by "month"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.Count(tt.filter, tt.by)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to say %s", err, tt.want)
			}
		})
	}
}

func mustWindow(t *testing.T, s string) Window {
	t.Helper()
	w, err := ParseWindow(s)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// windowItem is the item as the window's items.jsonl has it.
func windowItem(t *testing.T, f *Facts, w Window, id string) *Item {
	t.Helper()
	for _, it := range f.windowItems(w) {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no item %s in %s", id, w)
	return nil
}

// A technology's count is technologies.jsonl's rule for any slice of the changes: those counted
// toward an item (an initiative's features' too) or in a repo, in a window.
func TestCountTech(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	goTotals := f.technologies["Go"].Totals
	q1 := mustWindow(t, "2024-01-01..2024-03-31")
	tests := []struct {
		name   string
		tech   string
		filter Filter
		item   string
		want   TechTotals
	}{
		{"no filter is technologies.jsonl", "Go", Filter{}, "", goTotals},
		{"a window is that window's facts.json", "Go", Filter{Window: q1}, "", f.Summary(q1).Technologies[0].Totals},
		{"by an alias, in any case", "golang", Filter{}, "", goTotals},
		{"an item's changes", "Go", Filter{Item: "ENG-1"}, "ENG-1",
			TechTotals{Changes: 2, LinesAdded: 40, LinesDeleted: 7, Files: 2, ActiveDays: 3, Repos: 1}},
		{"an initiative's features'", "Go", Filter{Item: "ENG-10"}, "ENG-10",
			TechTotals{Changes: 2, LinesAdded: 40, LinesDeleted: 7, Files: 2, ActiveDays: 3, Repos: 1}},
		{"a repo's", "Go", Filter{Repo: "app"}, "", goTotals},
		{"a window without it", "Go", Filter{Window: mustWindow(t, "2025-01-01..2025-12-31")}, "", TechTotals{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.CountTech(tt.tech, tt.filter, "")
			if err != nil {
				t.Fatal(err)
			}
			check(t, "tech, item", []string{got.Tech, got.Item}, []string{"Go", tt.item})
			check(t, "totals", got.Totals, tt.want)
			check(t, "groups", got.Groups, []Group[TechTotals]{})
		})
	}
}

// A technology breaks down as the user's totals do, leaving out the groups without it.
func TestCountTechBreakdowns(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	tests := []struct {
		name   string
		filter Filter
		by     string
		want   []string // "<name>:<lines added>"
	}{
		{"by year is technologies.jsonl's by_year", Filter{}, ByYear, []string{"2024:60"}},
		{"by quarter", Filter{}, ByQuarter, []string{"2024q1:60"}},
		{"by repo", Filter{}, ByRepo, []string{"app:60"}},
		{"by item, most active days first", Filter{}, ByItem, []string{"ENG-1:40", "ENG-2:20"}},
		{"by item in a window", Filter{Window: mustWindow(t, "2024-02-01..2024-02-29")}, ByItem, []string{"ENG-2:20"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.CountTech("Go", tt.filter, tt.by)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, g := range got.Groups {
				names = append(names, g.Name+":"+itoa(g.Totals.LinesAdded))
			}
			check(t, "groups", names, tt.want)
		})
	}
	years, _ := f.CountTech("Go", Filter{}, ByYear)
	check(t, "by year totals", years.Groups[0].Totals, f.technologies["Go"].ByYear[0].Totals)
}

func TestCountTechRejects(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	tests := []struct {
		name, tech string
		filter     Filter
		by, want   string
	}{
		{"a technology not in the user's changes", "Rust", Filter{}, "", `no technology "Rust"`},
		{"an item the facts do not have", "Go", Filter{Item: "ENG-404"}, "", `no work item "ENG-404"`},
		{"a repo the dataset does not have", "Go", Filter{Repo: "nope"}, "", `no repo "nope"`},
		{"an unknown breakdown", "Go", Filter{}, "week", `by "week"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.CountTech(tt.tech, tt.filter, tt.by)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to say %s", err, tt.want)
			}
		})
	}
}
