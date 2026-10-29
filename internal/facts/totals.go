package facts

import (
	"slices"
	"strings"
	"time"
)

// Totals are the counts facts.json, items and repos report over one window. Each is defined by the
// events it counts (facts-schema.md lists them).
type Totals struct {
	ActiveDays        int `json:"active_days"`         // distinct UTC days with the user's own activity
	ChangesLanded     int `json:"changes_landed"`      // PRs, migrated PRs and commits that reached a default branch
	PRsOpened         int `json:"prs_opened"`          // the user's PRs, by opening
	PRsMerged         int `json:"prs_merged"`          // the user's PRs, by merge (any base branch)
	MigratedPRsMerged int `json:"migrated_prs_merged"` // the user's PRs merged on the host a repo was migrated from
	MergedForOthers   int `json:"merged_for_others"`   // others' PRs the user completed there
	Commits           int `json:"commits"`             // the user's own commits, one per patch
	CommitsCoAuthored int `json:"commits_co_authored"` // others' commits naming the user as co-author
	AgentCommits      int `json:"agent_commits"`       // AI agents' commits in the user's PRs, or crediting the user
	LinesAdded        int `json:"lines_added"`         // in landed changes, generated files and bulk changes left out
	LinesDeleted      int `json:"lines_deleted"`
	LinesLiving       int `json:"lines_living"`  // of those added, the lines the default branch still has, in the repos blamed
	Repos             int `json:"repos"`         // distinct repos with landed changes
	IssuesDone        int `json:"issues_done"`   // done_by_me, by resolution
	IssuesWorked      int `json:"issues_worked"` // distinct issues the user acted on
	IssuesRaised      int `json:"issues_raised"` // reported or created by the user
	PagesCreated      int `json:"pages_created"`
	PagesEdited       int `json:"pages_edited"`      // distinct pages with a version by the user
	DocsWritten       int `json:"docs_written"`      // distinct substantial docs (pages and ADRs) with a version by the user
	ADRsWritten       int `json:"adrs_written"`      // those that are decision records
	PRsReviewed       int `json:"prs_reviewed"`      // distinct PRs by others, not bots, with a review by the user
	ChangesRequested  int `json:"changes_requested"` // the user's reviews requesting changes
	ReviewComments    int `json:"review_comments"`   // the user's comments on others' PRs: on the diff and the conversation
	AuthorsReviewed   int `json:"authors_reviewed"`  // distinct authors of those PRs
	BotPRsReviewed    int `json:"bot_prs_reviewed"`  // distinct bots' PRs with a review by the user
	Items             int `json:"items"`             // distinct work items with something counted toward them
}

// totalsOf counts the events in w.
func totalsOf(events []event, w Window) Totals {
	var t Totals
	distinct := map[string]map[string]bool{}
	add := func(set, key string) {
		if distinct[set] == nil {
			distinct[set] = map[string]bool{}
		}
		distinct[set][key] = true
	}
	for _, e := range events {
		if !w.Contains(e.at) {
			continue
		}
		if e.item != "" {
			add("items", e.item)
		}
		switch e.kind {
		case evActive:
			add("days", day(e.at))
		case evPROpened:
			t.PRsOpened++
		case evPRMerged:
			t.PRsMerged++
		case evMigratedPR:
			t.MigratedPRsMerged++
		case evMergedForOthers:
			t.MergedForOthers++
		case evCommit:
			t.Commits++
		case evCoAuthored:
			t.CommitsCoAuthored++
		case evAgentCommit:
			t.AgentCommits++
		case evLanded:
			t.ChangesLanded++
			t.LinesAdded += e.added
			t.LinesDeleted += e.deleted
			t.LinesLiving += e.living
			add("repos", e.repo)
		case evIssueDone:
			t.IssuesDone++
			add("worked", e.key)
		case evIssueWorked:
			add("worked", e.key)
		case evIssueRaised:
			t.IssuesRaised++
		case evPageCreated:
			t.PagesCreated++
		case evPageVersion:
			add("pages", e.key)
			if e.item != "" {
				add("docs", e.key)
			}
		case evADRVersion:
			if e.item != "" {
				add("docs", e.key)
				add("adrs", e.key)
			}
		case evReview, evChangesRequested:
			add("reviewed", e.key)
			add("authors", strings.ToLower(e.who))
			if e.kind == evChangesRequested {
				t.ChangesRequested++
			}
		case evBotReview:
			add("botreviewed", e.key)
		case evReviewComment:
			t.ReviewComments++
		}
	}
	t.ActiveDays, t.Items, t.Repos = len(distinct["days"]), len(distinct["items"]), len(distinct["repos"])
	t.IssuesWorked, t.PagesEdited, t.DocsWritten = len(distinct["worked"]), len(distinct["pages"]), len(distinct["docs"])
	t.ADRsWritten = len(distinct["adrs"])
	t.PRsReviewed, t.AuthorsReviewed, t.BotPRsReviewed = len(distinct["reviewed"]), len(distinct["authors"]), len(distinct["botreviewed"])
	return t
}

// activeDays are the distinct days in w with the user's own activity, in order.
func activeDays(events []event, w Window) []string {
	set := map[string]bool{}
	for _, e := range events {
		if e.kind == evActive && w.Contains(e.at) {
			set[day(e.at)] = true
		}
	}
	days := make([]string, 0, len(set))
	for d := range set {
		days = append(days, d)
	}
	slices.Sort(days)
	return days
}

// span is the first and last day of the events in w: an item's span includes what happened to it
// without the user's hand (an issue of theirs resolved by someone else).
func span(events []event, w Window) (first, last string) {
	for _, e := range events {
		if w.Contains(e.at) {
			d := day(e.at)
			if first == "" || d < first {
				first = d
			}
			if d > last {
				last = d
			}
		}
	}
	return first, last
}

// Period is a calendar year or quarter's totals.
type Period struct {
	Period string `json:"period"` // "2024" or "2024q3"
	Totals Totals `json:"totals"`
}

// periods breaks the events in w down by calendar year and quarter, from the first active one.
func periods(events []event, w Window) (years, quarters []Period) {
	years, quarters = []Period{}, []Period{}
	var first, last time.Time
	for _, e := range events {
		if w.Contains(e.at) {
			if first.IsZero() || e.at.Before(first) {
				first = e.at
			}
			if e.at.After(last) {
				last = e.at
			}
		}
	}
	if first.IsZero() {
		return years, quarters
	}
	for y := first.Year(); y <= last.Year(); y++ {
		if t := totalsOf(events, intersect(yearWindow(y), w)); t != (Totals{}) {
			years = append(years, Period{Period: itoa(y), Totals: t})
		}
		for q := 1; q <= 4; q++ {
			if t := totalsOf(events, intersect(quarterWindow(y, q), w)); t != (Totals{}) {
				quarters = append(quarters, Period{Period: itoa(y) + "q" + itoa(q), Totals: t})
			}
		}
	}
	return years, quarters
}

func intersect(a, b Window) Window {
	w := a
	if !b.From.IsZero() && (w.From.IsZero() || b.From.After(w.From)) {
		w.From = b.From
	}
	if !b.To.IsZero() && (w.To.IsZero() || b.To.Before(w.To)) {
		w.To = b.To
	}
	if !w.From.IsZero() && !w.To.IsZero() && !w.From.Before(w.To) {
		return Window{From: w.From, To: w.From} // empty
	}
	return w
}
