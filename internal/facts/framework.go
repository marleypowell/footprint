package facts

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The career framework is the user's context/framework.md, in their words. Code reads only what
// they marked: {level=<id>} on a heading (or a table's column header) declares a level, and {#<id>}
// on a heading, list item or table row declares a criterion, of the level it sits under. A row
// marked {#<id>} in a table with level columns is a criterion per level: <id>@<level>.
// {#<id> reviews} says the user's reviews of others' PRs are evidence for it.

// Criterion is one marked competency or expectation of the framework.
type Criterion struct {
	ID      string `json:"id"`
	Level   string `json:"level"` // "" when it holds at any level
	Text    string `json:"text"`
	Reviews bool   `json:"reviews"` // the user's reviews of others' PRs are evidence for it
}

type framework struct {
	criteria []*Criterion // in the order the file has them
	byID     map[string]*Criterion
	hash     string
	problems []string
}

var (
	markerRE    = regexp.MustCompile(`[ \t]*\{\s*(?:#([a-z0-9][a-z0-9-]*)(\s+reviews)?|level=([a-z0-9][a-z0-9-]*))\s*\}`)
	headingRE   = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	listItemRE  = regexp.MustCompile(`^\s*([-*+]|\d+[.)])\s+`)
	tableSepRE  = regexp.MustCompile(`^\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)*\|?$`)
	emphasisRE  = regexp.MustCompile(`\*\*|__`)
	evidenceRef = "evidence+"
)

// readFramework reads context/framework.md: nil when there is none.
func readFramework(dir string) (*framework, error) {
	if dir == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "framework.md"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseFramework(string(data)), nil
}

// plainText is a line's text without its markers, list or heading syntax, or emphasis.
func plainText(s string) string {
	s = markerRE.ReplaceAllString(s, "")
	s = listItemRE.ReplaceAllString(s, "")
	if m := headingRE.FindStringSubmatch(strings.TrimSpace(s)); m != nil {
		s = m[2]
	}
	return oneLine(emphasisRE.ReplaceAllString(s, ""))
}

func tableCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
	cells := strings.Split(line, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

func parseFramework(text string) *framework {
	fw := &framework{byID: map[string]*Criterion{}}
	add := func(line int, id, level, text string, reviews bool) {
		if fw.byID[id] != nil {
			fw.problems = append(fw.problems, fmt.Sprintf("line %d: %s is marked twice; the first counts", line, id))
			return
		}
		c := &Criterion{ID: id, Level: level, Text: text, Reviews: reviews}
		fw.criteria, fw.byID[id] = append(fw.criteria, c), c
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	level, levelDepth := "", 0
	var columns []string // a table's level per column, from its header; nil outside a table
	inFence, inTable := false, false
	for i, line := range lines {
		n, t := i+1, strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if !strings.HasPrefix(t, "|") {
			inTable, columns = false, nil
		}
		markers := markerRE.FindAllStringSubmatch(line, -1)
		if h := headingRE.FindStringSubmatch(t); h != nil {
			depth := len(h[1])
			if levelDepth > 0 && depth <= levelDepth {
				level, levelDepth = "", 0
			}
			for _, m := range markers {
				if m[3] != "" {
					level, levelDepth = m[3], depth
				}
			}
			for _, m := range markers {
				if m[1] != "" {
					add(n, m[1], level, plainText(t), m[2] != "")
				}
			}
			continue
		}
		if strings.HasPrefix(t, "|") {
			if tableSepRE.MatchString(t) {
				continue
			}
			cells := tableCells(t)
			if !inTable { // the header row
				inTable = true
				for ci, cell := range cells {
					if m := markerRE.FindStringSubmatch(cell); m != nil && m[3] != "" {
						if columns == nil {
							columns = make([]string, len(cells))
						}
						columns[ci] = m[3]
					}
				}
				continue
			}
			m := markerRE.FindStringSubmatch(cells[0])
			if m == nil || m[1] == "" {
				continue
			}
			name := plainText(cells[0])
			if columns == nil {
				var parts []string
				for _, c := range cells {
					if c = plainText(c); c != "" {
						parts = append(parts, c)
					}
				}
				add(n, m[1], level, strings.Join(parts, " · "), m[2] != "")
				continue
			}
			for ci := 1; ci < len(cells) && ci < len(columns); ci++ {
				if cell := plainText(cells[ci]); columns[ci] != "" && cell != "" {
					add(n, m[1]+"@"+columns[ci], columns[ci], name+": "+cell, m[2] != "")
				}
			}
			continue
		}
		for _, m := range markers {
			switch {
			case m[1] != "":
				add(n, m[1], level, plainText(t), m[2] != "")
			case m[3] != "":
				fw.problems = append(fw.problems, fmt.Sprintf("line %d: {level=%s} is not on a heading or a table's header row", n, m[3]))
			}
		}
	}
	if len(fw.criteria) == 0 {
		fw.problems = append(fw.problems, "no criteria are marked: add {#<id>} to the competencies and expectations to map evidence to")
	}
	sorted := slices.Clone(fw.criteria)
	slices.SortFunc(sorted, func(a, b *Criterion) int { return cmp.Compare(a.ID, b.ID) })
	h := sha256.New()
	for _, c := range sorted {
		fmt.Fprintf(h, "%s\t%s\t%s\t%v\n", c.ID, c.Level, c.Text, c.Reviews)
	}
	fw.hash = "sha256:" + hex.EncodeToString(h.Sum(nil))
	return fw
}

// Evidence is a piece of the user's own evidence for criteria, from outside the tools:
// context/evidence/<slug>.md, with frontmatter shows: <criteria> and when: YYYY-MM-DD.
type Evidence struct {
	Ref   string   `json:"ref"` // evidence+<slug>
	Shows []string `json:"shows"`
	When  string   `json:"when"`
	at    time.Time
}

func readEvidence(dir string) ([]*Evidence, []string, error) {
	if dir == "" {
		return nil, nil, nil
	}
	entries, err := os.ReadDir(filepath.Join(dir, "evidence"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var out []*Evidence
	var problems []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "evidence", e.Name()))
		if err != nil {
			return nil, nil, err
		}
		slug := strings.TrimSuffix(e.Name(), ".md")
		front, _, ok := Frontmatter(string(data))
		at, errWhen := time.Parse(dayLayout, front["when"])
		if !ok || errWhen != nil {
			problems = append(problems, fmt.Sprintf("evidence/%s.md needs frontmatter with when: YYYY-MM-DD and shows: <criteria>", slug))
			continue
		}
		out = append(out, &Evidence{Ref: evidenceRef + slug, Shows: tagList(front["shows"]), When: front["when"], at: at})
	}
	slices.SortFunc(out, func(a, b *Evidence) int { return cmp.Compare(a.Ref, b.Ref) })
	return out, problems, nil
}

// tagList reads a comma-separated list of criterion ids.
func tagList(s string) []string {
	set := map[string]bool{}
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			set[t] = true
		}
	}
	return sortedKeys(set)
}

// Coverage statuses.
const (
	CoverageNone    = "none"
	CoverageThin    = "thin"    // fewer than 2 pieces of evidence, or all in one quarter
	CoverageCovered = "covered" // 2 or more, across 2 quarters or more
)

// CriterionCoverage is one criterion's evidence in a window.
type CriterionCoverage struct {
	Criterion
	Status   string   `json:"status"`
	Items    []string `json:"items"`    // items whose current notes are tagged with it
	Evidence []string `json:"evidence"` // the user's evidence files showing it
	Reviews  int      `json:"reviews"`  // reviews of others' PRs with the user's words in them, for a criterion marked reviews
	Quarters int      `json:"quarters"` // distinct quarters of that evidence
}

// FrameworkSummary is facts.json's framework: the window's coverage, in counts.
type FrameworkSummary struct {
	Hash     string         `json:"hash"`
	Criteria int            `json:"criteria"`
	Tagged   int            `json:"tagged"`   // current notes tagged against this framework
	Untagged int            `json:"untagged"` // current notes not yet tagged against it: coverage is provisional until 0
	ByStatus map[string]int `json:"by_status"`
	Problems []string       `json:"problems"`
}

// tagged says whether an item's note is current and tagged against the framework as it is.
func (f *Facts) tagged(it *Item) (*noteFile, bool) {
	n := f.notes[itemPath(it.ID)]
	return n, f.framework != nil && it.Note == NoteCurrent && n != nil && n.framework == f.framework.hash
}

// coverage is every criterion's evidence in w, in the framework's order.
func (f *Facts) coverage(w Window) []CriterionCoverage {
	if f.framework == nil {
		return nil
	}
	out := make([]CriterionCoverage, 0, len(f.framework.criteria))
	for _, c := range f.framework.criteria {
		cc := CriterionCoverage{Criterion: *c, Items: []string{}, Evidence: []string{}}
		quarters := map[string]bool{}
		for _, it := range f.windowItems(w) {
			if n, ok := f.tagged(it); ok && slices.Contains(n.shows, c.ID) {
				cc.Items = append(cc.Items, it.ID)
				for _, d := range it.days {
					if t, err := time.Parse(dayLayout, d); err == nil {
						quarters[quarter(t)] = true
					}
				}
			}
		}
		for _, e := range f.evidenceFiles {
			if w.Contains(e.at) && slices.Contains(e.Shows, c.ID) {
				cc.Evidence = append(cc.Evidence, e.Ref)
				quarters[quarter(e.at)] = true
			}
		}
		if c.Reviews {
			for _, r := range f.Reviews {
				if at, _ := time.Parse(dayLayout, r.First); r.Words > 0 && w.Contains(at) {
					cc.Reviews++
					quarters[quarter(at)] = true
				}
			}
		}
		cc.Quarters = len(quarters)
		switch pieces := len(cc.Items) + len(cc.Evidence) + cc.Reviews; {
		case pieces == 0:
			cc.Status = CoverageNone
		case pieces < 2 || cc.Quarters < 2:
			cc.Status = CoverageThin
		default:
			cc.Status = CoverageCovered
		}
		out = append(out, cc)
	}
	return out
}

func (f *Facts) frameworkSummary(w Window) *FrameworkSummary {
	if f.framework == nil {
		return nil
	}
	s := &FrameworkSummary{Hash: f.framework.hash, Criteria: len(f.framework.criteria),
		ByStatus: map[string]int{CoverageNone: 0, CoverageThin: 0, CoverageCovered: 0},
		Problems: append(slices.Clone(f.framework.problems), f.evidenceProblems...)}
	if s.Problems == nil {
		s.Problems = []string{}
	}
	for _, it := range f.windowItems(w) {
		if it.Note != NoteCurrent || len(f.framework.criteria) == 0 {
			continue
		}
		if _, ok := f.tagged(it); ok {
			s.Tagged++
		} else {
			s.Untagged++
		}
	}
	for _, cc := range f.coverage(w) {
		s.ByStatus[cc.Status]++
	}
	return s
}

// criteriaDoc is criteria.md: each level's criteria, gaps first, with their evidence.
func (f *Facts) criteriaDoc(w Window) []byte {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	scope := "all time"
	if !w.IsAll() {
		scope = w.String()
	}
	p("# Framework coverage: %s\n\n", scope)
	s := f.frameworkSummary(w)
	p("Built by `footprint facts` from context/framework.md (%s); README.md has the rules. %d criteria: ", s.Hash[:19], s.Criteria)
	p("%d with no evidence, %d thin, %d covered.\n\n", s.ByStatus[CoverageNone], s.ByStatus[CoverageThin], s.ByStatus[CoverageCovered])
	if s.Untagged > 0 {
		p("**Provisional:** %d current notes are not yet tagged against this framework (tags.txt lists them);\n", s.Untagged)
		p("%d are.\n\n", s.Tagged)
	}
	for _, pr := range s.Problems {
		p("- Problem: %s\n", pr)
	}
	if len(s.Problems) > 0 {
		p("\n")
	}
	cov := f.coverage(w)
	var levels []string
	for _, cc := range cov {
		if !slices.Contains(levels, cc.Level) {
			levels = append(levels, cc.Level)
		}
	}
	rank := map[string]int{CoverageNone: 0, CoverageThin: 1, CoverageCovered: 2}
	for _, level := range levels {
		if level == "" {
			p("## Any level\n\n")
		} else {
			p("## %s\n\n", level)
		}
		var rows []CriterionCoverage
		for _, cc := range cov {
			if cc.Level == level {
				rows = append(rows, cc)
			}
		}
		slices.SortStableFunc(rows, func(a, b CriterionCoverage) int { return cmp.Compare(rank[a.Status], rank[b.Status]) })
		p("| status | criterion | evidence | quarters |\n|---|---|---|--:|\n")
		for _, cc := range rows {
			var ev []string
			for i, id := range cc.Items {
				if i == 8 {
					ev = append(ev, fmt.Sprintf("+%d more", len(cc.Items)-8))
					break
				}
				ev = append(ev, id)
			}
			ev = append(ev, cc.Evidence...)
			if cc.Reviews > 0 {
				ev = append(ev, fmt.Sprintf("%d reviews", cc.Reviews))
			}
			p("| %s | `%s` %s | %s | %d |\n", cc.Status, cc.ID, cell(clip(cc.Text, 160)), cell(strings.Join(ev, ", ")), cc.Quarters)
		}
		p("\n")
	}
	return []byte(b.String())
}

// tagsTodo is tags.txt: the current notes to tag against the framework as it is, biggest first.
func (f *Facts) tagsTodo() []byte {
	if f.framework == nil || len(f.framework.criteria) == 0 {
		return []byte{}
	}
	var rows []*Item
	for _, it := range f.Items {
		if _, ok := f.tagged(it); it.Note == NoteCurrent && !ok {
			rows = append(rows, it)
		}
	}
	slices.SortStableFunc(rows, func(a, b *Item) int {
		return cmp.Or(cmp.Compare(effortRank(a.Effort), effortRank(b.Effort)), cmp.Compare(a.First, b.First), cmp.Compare(a.ID, b.ID))
	})
	var b strings.Builder
	for _, it := range rows {
		fmt.Fprintf(&b, "untagged\t%s\t%s\t%s..%s\t%s\n", it.ID, it.Effort, it.First, it.Last, oneLine(it.Title))
	}
	return []byte(b.String())
}
