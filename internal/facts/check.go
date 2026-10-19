package facts

import (
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Finding is one thing check found in a file. Errors fail the check; warnings are for the agent to
// fix, or to list for the user.
type Finding struct {
	Line    int
	Error   bool
	Message string
}

func (fd Finding) String() string {
	level := "warning"
	if fd.Error {
		level = "error"
	}
	return fmt.Sprintf("%d: %s: %s", fd.Line, level, fd.Message)
}

var (
	citationRE      = regexp.MustCompile(`\[\[([^\[\]]+)\]\]`)
	plainCitationRE = regexp.MustCompile(`[ \t]?\[\[([^\[\]]+)\]\]`)
	urlRE           = regexp.MustCompile(`https?://[^\s<>()\[\]"'` + "`" + `]+`)
	codeSpanRE      = regexp.MustCompile("`[^`]*`")
	keyRE           = regexp.MustCompile(`\b([A-Z][A-Z0-9_]*)-(\d+)\b`)
	// not counts: dates, quarters, times, versions, and ids such as repo#12
	notCountRE = regexp.MustCompile(`\b\d{4}-\d{2}(-\d{2})?\b|\b\d{4}\s?[qQ][1-4]\b|\b[qQ][1-4]\s?\d{4}\b|\b[HhQq][1-4]\b|` +
		`\b\d{1,2}:\d{2}\b|\bv?\d+(\.\d+){2,}\b|\bv\d+(\.\d+)?\b|\S*#\d+\b|\b(19|20)\d{2}\b|\b\d+(st|nd|rd|th)\b`)
	periodRE  = regexp.MustCompile(`\b((?:19|20)\d{2})(?:\s?[qQ]([1-4]))?\b|\b[qQ]([1-4])\s?((?:19|20)\d{2})\b`)
	numberRE  = regexp.MustCompile(`(~|\b)(\d{1,3}(,\d{3})+|\d+)(\.\d+)?\s?(%|x|×|k|K|M)?`)
	approxRE  = regexp.MustCompile(`(?i)(~|about|around|almost|nearly|over|more than|roughly|some|close to|approximately|under|fewer than|less than)\s*$`)
	letterRun = regexp.MustCompile(`[A-Za-z]$`)
)

// Check checks a note (frontmatter item: <id>) or a draft (frontmatter window: all or
// YYYY-MM-DD..YYYY-MM-DD) against the facts: every citation must resolve (in a note, to that item's
// evidence), and every number should be a fact in scope or one of the user's own.
func (f *Facts) Check(path, text string) []Finding {
	front, body, ok := Frontmatter(text)
	offset := strings.Count(text[:len(text)-len(body)], "\n")
	if !ok {
		return []Finding{{Line: 1, Error: true, Message: "no frontmatter: a note starts with ---, item: <id>, inputs: <hash>, ---; " +
			"a draft with ---, window: all (or YYYY-MM-DD..YYYY-MM-DD), ---"}}
	}
	c := &checker{f: f, offset: offset}
	switch {
	case front["item"] != "":
		c.checkNote(path, front, body)
	case front["window"] != "":
		w, err := ParseWindow(front["window"])
		if err != nil {
			return []Finding{{Line: 2, Error: true, Message: err.Error()}}
		}
		c.window = w
		c.values = map[int]bool{}
		c.addTotals(totalsOf(f.events, w))
		c.periods = map[string]Totals{}
		years, quarters := periods(f.events, w)
		for _, p := range append(years, quarters...) {
			c.periods[p.Period] = p.Totals
		}
	default:
		return []Finding{{Line: 1, Error: true, Message: "the frontmatter needs item: <id> (a note) or window: ... (a draft)"}}
	}
	if c.values != nil {
		for _, text := range f.contexts {
			c.addNumbers(text)
		}
	}
	c.walk(body)
	return c.findings
}

type checker struct {
	f        *Facts
	offset   int // lines before the body
	note     *Item
	allowed  map[string]bool // a note's evidence: what it may cite
	window   Window
	values   map[int]bool      // numbers in scope (drafts): the window's totals, and the user's own
	periods  map[string]Totals // the window's years and quarters, in scope where a paragraph names them
	findings []Finding
}

func (c *checker) add(line int, isError bool, format string, args ...any) {
	c.findings = append(c.findings, Finding{Line: c.offset + line, Error: isError, Message: fmt.Sprintf(format, args...)})
}

func (c *checker) checkNote(path string, front map[string]string, body string) {
	it := c.f.items[front["item"]]
	if it == nil {
		c.add(0, true, "item %q is not a work item (items.jsonl)", front["item"])
		return
	}
	c.note, c.allowed = it, c.f.evidence(it)
	if want := itemPath(it.ID) + ".md"; filepath.Base(path) != want {
		c.add(0, false, "a note on %s is read from notes/%s", it.ID, want)
	}
	switch inputs := front["inputs"]; {
	case inputs == "":
		c.add(0, true, "the frontmatter needs inputs: %s (the pack's hash, from packs/%s.md)", it.Pack, itemPath(it.ID))
	case inputs != it.Pack:
		c.add(0, false, "written from an earlier pack: revise it from packs/%s.md, then set inputs: %s", itemPath(it.ID), it.Pack)
	}
	headings := headingsOf(body)
	for _, s := range NoteSections {
		if !slices.Contains(headings, s) {
			c.add(0, true, "missing section: ## %s", s)
		}
	}
}

// evidence is everything a note on it may cite.
func (f *Facts) evidence(it *Item) map[string]bool {
	set := map[string]bool{it.ID: true}
	if it.Initiative != nil {
		set[it.Initiative.ID] = true
	}
	for _, list := range [][]string{it.Issues, it.Features, it.Changes, it.AlsoChanges, it.Docs} {
		for _, id := range list {
			set[id] = true
		}
	}
	for _, id := range it.Features {
		for _, k := range f.items[id].Issues {
			set[k] = true
		}
	}
	for _, k := range it.Issues {
		for _, prev := range f.issues[k].PreviousKeys {
			set[prev] = true
		}
	}
	return set
}

func (c *checker) addTotals(t Totals) {
	for _, v := range t.values() {
		c.values[v] = true
	}
}

func (c *checker) addNumbers(text string) {
	for _, m := range numberRE.FindAllStringSubmatch(text, -1) {
		if v, ok := parseCount(m[2] + m[4]); ok {
			c.values[int(math.Round(v))] = true
		}
	}
}

// addItem adds the numbers of a cited record to the scope of its paragraph.
func (c *checker) itemValues(id string) []int {
	var vals []int
	if it := c.f.items[id]; it != nil {
		evs := c.f.rollup(it)
		for _, v := range totalsOf(evs, c.window).values() {
			vals = append(vals, v)
		}
		vals = append(vals, len(it.Issues), len(it.Features), len(it.Changes), len(activeDays(evs, c.window)))
		if it.Subtasks != nil {
			vals = append(vals, it.Subtasks.Total, it.Subtasks.DoneByMe)
		}
	}
	if ch := c.f.changes[id]; ch != nil {
		vals = append(vals, ch.Lines.Added, ch.Lines.Deleted, ch.Lines.Files, len(ch.Commits))
	}
	if d := c.f.docByRef(id); d != nil {
		vals = append(vals, d.Words, d.Versions, d.MyVersions, d.Share)
	}
	return vals
}

// walk checks the body paragraph by paragraph, outside code.
func (c *checker) walk(body string) {
	lines := strings.Split(body, "\n")
	inFence := false
	var para []int // line indexes of the current paragraph
	flush := func() {
		if len(para) > 0 {
			c.paragraph(lines, para)
			para = para[:0]
		}
	}
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			flush()
			continue
		}
		if inFence {
			continue
		}
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "|") {
			flush()
		}
		if t != "" {
			para = append(para, i)
		}
	}
	flush()
}

func (c *checker) paragraph(lines []string, idx []int) {
	local := map[int]bool{}
	type pending struct {
		line int
		text string
	}
	var numbers []pending
	original := map[int]string{}
	for _, i := range idx {
		original[i] = clip(oneLine(lines[i]), 120)
		line := codeSpanRE.ReplaceAllString(lines[i], " ")
		for _, m := range citationRE.FindAllStringSubmatch(line, -1) {
			ref := strings.TrimSpace(strings.SplitN(m[1], "|", 2)[0])
			id, url := c.f.resolve(ref)
			switch {
			case id == "":
				c.add(i+1, true, "[[%s]] names nothing in the dataset (an item, issue key, repo#PR, repo@sha or doc+id)", ref)
			case c.allowed != nil && !c.allowed[id]:
				c.add(i+1, true, "[[%s]] is not part of %s's evidence: a note cites only its own item's records", ref, c.note.ID)
			default:
				for _, v := range c.itemValues(id) {
					local[v] = true
				}
				_ = url
			}
		}
		rest := citationRE.ReplaceAllString(line, " ")
		for _, u := range urlRE.FindAllString(rest, -1) {
			if c.f.knownHost(u) && !c.f.knownURL(u) {
				c.add(i+1, false, "%s is not in the dataset: cite with [[id]], or check the link", u)
			}
		}
		rest = urlRE.ReplaceAllString(rest, " ")
		for _, m := range keyRE.FindAllStringSubmatch(rest, -1) {
			if c.f.isProjectKey(m[1]) && c.f.issues[c.f.currentKey(m[0])] == nil {
				c.add(i+1, false, "%s is not in the dataset (the user was never assigned it, nor named it in git work)", m[0])
			}
		}
		rest = keyRE.ReplaceAllString(rest, " ")
		for _, m := range periodRE.FindAllStringSubmatch(rest, -1) {
			name := m[1]
			if m[2] != "" {
				name += "q" + m[2]
			} else if m[3] != "" {
				name = m[4] + "q" + m[3]
			}
			if t, ok := c.periods[strings.ToLower(name)]; ok {
				for _, v := range t.values() {
					local[v] = true
				}
			}
		}
		rest = notCountRE.ReplaceAllString(rest, " ")
		numbers = append(numbers, pending{i, rest})
	}
	for ni, n := range numbers {
		prev := "" // the line before, whose end says whether this one starts a sentence
		if ni > 0 {
			prev = strings.TrimSpace(numbers[ni-1].text)
		}
		for _, loc := range numberRE.FindAllStringSubmatchIndex(n.text, -1) {
			start, end := loc[0], loc[1]
			numEnd := loc[5]
			if loc[8] >= 0 {
				numEnd = loc[9]
			}
			if start > 0 && letterRun.MatchString(n.text[:start]) || // part of a word: S3, H1, OAuth2
				loc[10] < 0 && numEnd < len(n.text) && letterRun.MatchString(n.text[numEnd:numEnd+1]) { // 3D, 2FA
				continue
			}
			raw := n.text[loc[4]:loc[5]]
			if loc[8] >= 0 {
				raw += n.text[loc[8]:loc[9]]
			}
			v, ok := parseCount(raw)
			if !ok {
				continue
			}
			suffix := ""
			if loc[10] >= 0 {
				suffix = n.text[loc[10]:loc[11]]
			}
			if suffix == "" && loc[2] == loc[3] && partOfName(strings.TrimLeft(prev+" "+n.text[:start], " "), n.text[end:]) {
				continue
			}
			got := strings.TrimSpace(n.text[start:end])
			if c.note != nil {
				c.add(n.line+1, false, "%q: a note carries no counts (they come from the facts when drafting), in %q", got, original[n.line])
				continue
			}
			approx := approxRE.MatchString(n.text[:start]) || loc[2] < loc[3] // "~", "about", "over"
			switch {
			case (suffix == "%" || suffix == "x" || suffix == "×") && !c.values[int(math.Round(v))]:
				c.add(n.line+1, false, "%q is derived: write the numbers it comes from (120 of 300 commits), or the user's own figure "+
					"from context/. In %q", got, original[n.line])
			case !c.matches(v, suffix, approx, local):
				c.add(n.line+1, false, "%q matches no fact in scope (window %s): take it from facts.json or a cited record, "+
					"or the user's own words in context/; a name or version (Angular 15) is fine. In %q", got, c.window, original[n.line])
			}
		}
	}
}

var (
	versionWords = map[string]bool{"version": true, "release": true, "level": true, "phase": true, "sprint": true, "tier": true}
	listMarkerRE = regexp.MustCompile(`^([-*+>]|\d+[.)])$`)
	// what a number counts, whatever precedes it: "rebuilt Storybook 12 times"
	countUnitRE = regexp.MustCompile(`^(times|days|weeks|months|years|hours|minutes|people|engineers|developers|teammates|PRs|commits|tickets|repos)\b`)
	// a sentence opening with a verb ("Merged 12 PRs", "Led 4 people", "Building 7 services"), as
	// notes are written: past tense, or an irregular verb
	verbRE = regexp.MustCompile(`(?i)^([a-z]+(ed|ing)|led|ran|wrote|built|made|sent|took|did|got|set|found|held|kept|taught|drove|spent|gave|brought|chose|cut|put|read|won)$`)
)

// partOfName says whether a number, with the text before it (the line before too, so a wrapped line
// reads as one sentence) and after it, belongs to a name or a version rather than counting
// something: Angular 15, .NET 8, Office 365, HTTP 429, version 2. The word before it names something
// when it has a capital letter. At the start of a sentence every word has one, so there it is a
// name unless it is a verb.
func partOfName(before, after string) bool {
	if !strings.HasSuffix(before, " ") || strings.HasSuffix(before, "  ") {
		return false
	}
	if countUnitRE.MatchString(strings.TrimLeft(after, " ")) {
		return false
	}
	before = strings.TrimSuffix(before, " ")
	word := before[strings.LastIndexAny(before, " \t")+1:]
	bare := strings.TrimLeft(word, "*_([\"'")
	if versionWords[strings.ToLower(bare)] {
		return true
	}
	if !strings.ContainsFunc(bare, unicode.IsUpper) {
		return false
	}
	rest := strings.TrimSpace(before[:len(before)-len(word)])
	if rest != "" && !strings.ContainsAny(rest[len(rest)-1:], ".!?:;") && !listMarkerRE.MatchString(rest) {
		return true // within a sentence
	}
	return !verbRE.MatchString(bare)
}

func parseCount(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
	return v, err == nil
}

// matches says whether v (with its suffix) is a number in scope: exactly, or within 10% when
// written as an approximation (~, about, 1.4k, 600k); percentages and multiples may be a ratio of
// two numbers in scope.
func (c *checker) matches(v float64, suffix string, approx bool, local map[int]bool) bool {
	vals := make([]int, 0, len(c.values)+len(local))
	for n := range c.values {
		vals = append(vals, n)
	}
	for n := range local {
		vals = append(vals, n)
	}
	near := func(target, got float64, tol float64) bool {
		return target != 0 && math.Abs(got-target) <= tol*math.Abs(target)
	}
	switch suffix {
	case "%", "x", "×": // derived: only the user's own figures pass
		return false
	case "k", "K":
		v, approx = v*1000, true
	case "M":
		v, approx = v*1000000, true
	}
	if v == math.Trunc(v) && (c.values[int(v)] || local[int(v)]) {
		return true
	}
	if !approx && v >= 100 && math.Mod(v, 10) == 0 {
		approx = true // a round number stands for an approximation
	}
	if approx {
		for _, n := range vals {
			if near(float64(n), v, 0.1) {
				return true
			}
		}
	}
	return false
}

// resolve finds the record a citation names, and its link.
func (f *Facts) resolve(ref string) (id, link string) {
	if it := f.items[ref]; it != nil {
		return it.ID, it.URL
	}
	if is := f.issues[f.currentKey(ref)]; is != nil && keyRE.MatchString(ref) {
		return is.Key, is.URL
	}
	if c := f.changes[ref]; c != nil {
		return c.ID, c.URL
	}
	if repo, sha, ok := strings.Cut(ref, "@"); ok && len(sha) >= 7 {
		for _, c := range f.Changes {
			if c.Repo == repo && (strings.HasPrefix(c.ID, ref) || slices.ContainsFunc(c.Commits, func(s string) bool { return strings.HasPrefix(s, sha) })) {
				return c.ID, c.URL
			}
		}
	}
	if d := f.docByRef(ref); d != nil {
		return d.Ref, d.URL
	}
	return "", ""
}

// Final is the checked text for sharing: citations removed (plain) or turned into links.
func (f *Facts) Final(text string, links bool) string {
	_, body, ok := Frontmatter(text)
	if !ok {
		body = text
	}
	re := citationRE
	if !links {
		re = plainCitationRE // takes the space before a citation with it
	}
	out := re.ReplaceAllStringFunc(body, func(m string) string {
		inner := citationRE.FindStringSubmatch(m)[1]
		space := ""
		if strings.HasPrefix(m, " ") || strings.HasPrefix(m, "\t") {
			space = m[:1]
		}
		ref, label, hasLabel := strings.Cut(inner, "|")
		ref = strings.TrimSpace(ref)
		if !hasLabel {
			label = ref
		}
		if !links {
			if hasLabel {
				return space + label
			}
			return ""
		}
		if _, link := f.resolve(ref); link != "" {
			return "[" + label + "](" + link + ")"
		}
		return label
	})
	return strings.TrimLeft(out, "\n")
}

func (f *Facts) urlSets() (hosts, urls map[string]bool) {
	if f.hosts != nil {
		return f.hosts, f.urls
	}
	f.hosts, f.urls = map[string]bool{}, map[string]bool{}
	add := func(u string) {
		if u == "" {
			return
		}
		f.urls[strings.TrimSuffix(u, "/")] = true
		if p, err := url.Parse(u); err == nil {
			f.hosts[p.Host] = true
		}
	}
	for _, it := range f.Items {
		add(it.URL)
	}
	for _, is := range f.Issues {
		add(is.URL)
	}
	for _, c := range f.Changes {
		add(c.URL)
	}
	for _, d := range f.Docs {
		add(d.URL)
	}
	for _, r := range f.Repos {
		add(r.URL)
	}
	return f.hosts, f.urls
}

func (f *Facts) knownHost(u string) bool {
	hosts, _ := f.urlSets()
	p, err := url.Parse(u)
	return err == nil && hosts[p.Host]
}

func (f *Facts) knownURL(u string) bool {
	_, urls := f.urlSets()
	return urls[strings.TrimRight(strings.TrimSuffix(u, "/"), ".,;:")]
}

// isProjectKey says whether a key prefix is one of the tracker's projects.
func (f *Facts) isProjectKey(prefix string) bool {
	if f.projectKeys == nil {
		f.projectKeys = map[string]bool{}
		for _, p := range f.raw.IssueProjects {
			f.projectKeys[p.Key] = true
			for _, k := range p.PreviousKeys {
				f.projectKeys[k] = true
			}
		}
		for _, is := range f.Issues {
			f.projectKeys[is.Project] = true
		}
	}
	return f.projectKeys[prefix]
}
