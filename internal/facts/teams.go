package facts

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
	"time"
)

// TeamSignals are the traces of the user's team in one calendar quarter, from the issues they worked
// on or finished then: the boards whose sprints held those issues at the time, the issues' projects,
// and their owning team where the tracker records one. They suggest team changes for the user to
// confirm in profile.md; the data cannot say which team they were on.
type TeamSignals struct {
	Period   string   `json:"period"`   // "2024q3"
	Boards   []Signal `json:"boards"`   // sprint names without their year and number: "Checkout"
	Projects []Signal `json:"projects"` // the tracker projects, by key
	Teams    []Signal `json:"teams"`    // the issues' own team field
}

// Signal is one board, project or team, and the distinct issues of the user's in it that quarter.
type Signal struct {
	Name   string `json:"name"`
	Title  string `json:"title,omitempty"` // a project's name
	Issues int    `json:"issues"`
}

// teamSignals are the signals of each quarter in w with any, oldest first.
func (f *Facts) teamSignals(w Window) []TeamSignals {
	type key struct{ period, kind, name string }
	sets := map[key]map[string]bool{}
	titles := map[string]string{}
	add := func(k key, issue string) {
		if sets[k] == nil {
			sets[k] = map[string]bool{}
		}
		sets[k][issue] = true
	}
	for _, e := range f.events {
		if (e.kind != evIssueWorked && e.kind != evIssueDone) || !w.Contains(e.at) {
			continue
		}
		is := f.issues[e.key]
		if is == nil {
			continue
		}
		q := quarter(e.at)
		add(key{q, "project", is.Project}, is.Key)
		titles[is.Project] = is.rec.Project.Name
		if b := sprintBoard(is, e.at); b != "" {
			add(key{q, "board", b}, is.Key)
		}
		if t := strings.TrimSpace(deref(is.rec.Team)); t != "" {
			add(key{q, "team", t}, is.Key)
		}
	}
	byPeriod := map[string]*TeamSignals{}
	for k, set := range sets {
		ts := byPeriod[k.period]
		if ts == nil {
			ts = &TeamSignals{Period: k.period, Boards: []Signal{}, Projects: []Signal{}, Teams: []Signal{}}
			byPeriod[k.period] = ts
		}
		s := Signal{Name: k.name, Issues: len(set)}
		switch k.kind {
		case "board":
			ts.Boards = append(ts.Boards, s)
		case "project":
			s.Title = titles[k.name]
			ts.Projects = append(ts.Projects, s)
		case "team":
			ts.Teams = append(ts.Teams, s)
		}
	}
	out := []TeamSignals{}
	for _, p := range sortedKeys(byPeriod) {
		ts := byPeriod[p]
		for _, list := range [][]Signal{ts.Boards, ts.Projects, ts.Teams} {
			slices.SortFunc(list, func(a, b Signal) int { return cmp.Or(cmp.Compare(b.Issues, a.Issues), cmp.Compare(a.Name, b.Name)) })
		}
		out = append(out, *ts)
	}
	return out
}

// sprintBoard is the board of the sprint that held the issue at t, or "": the latest-starting sprint
// running then (start to completion, or to its planned end), to the end of its last day.
func sprintBoard(is *Issue, t time.Time) string {
	best, bestStart := "", time.Time{}
	for _, s := range is.rec.Sprints {
		start, ok := timeOf(s.StartAt)
		if !ok {
			continue
		}
		end, ok := timeOf(s.CompletedAt)
		if !ok {
			if end, ok = timeOf(s.EndAt); !ok {
				continue
			}
		}
		end = time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
		if !t.Before(start) && t.Before(end) && (best == "" || start.After(bestStart)) {
			best, bestStart = boardOf(s.Name), start
		}
	}
	return best
}

// periodWord matches the words that number a sprint rather than name its team: a year ("2024",
// "CY24", "FY25"), a number, "Sprint", "Cycle", "Iteration", "Q3", or a lone dash.
var periodWord = regexp.MustCompile(`(?i)^((19|20)\d\d|[cf]y\d\d(\d\d)?|\d+|sprint|cycle|iteration|q[1-4]|-+)$`)

// boardOf is a sprint's name up to the first word that numbers it: "Checkout 2020 Sprint 10" and
// "Checkout CY22 Sprint 3" are both "Checkout". A name that starts with one is kept whole.
func boardOf(sprint string) string {
	words := strings.Fields(sprint)
	for i, w := range words {
		if periodWord.MatchString(w) {
			if i == 0 {
				break
			}
			return strings.Join(words[:i], " ")
		}
	}
	return strings.Join(words, " ")
}
