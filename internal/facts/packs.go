package facts

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// PackVersion changes whenever what packs hold changes, which makes every note stale once.
const PackVersion = 1

// Pack bounds: the rendered pack stays under maxPackBytes, and every cut is marked.
const (
	maxPackBytes   = 16 << 10
	maxDescription = 2500
	maxMembers     = 60
	maxComments    = 8
	maxComment     = 400
	maxChanges     = 40
	maxBodies      = 6
	maxBody        = 600
	maxSubjects    = 60
	maxPaths       = 12
	maxFromUser    = 4000
	maxDocBody     = 10000
)

// packInput is everything a pack shows, and nothing that changes without the user's work changing
// (others' comments, review counts, branch containment, sprint state). Its hash is the inputs a note
// was written from.
type packInput struct {
	Version     int          `json:"version"`
	ID          string       `json:"id"`
	Kind        string       `json:"kind"`
	Title       string       `json:"title"`
	URL         string       `json:"url"`
	Type        string       `json:"type"`
	Status      string       `json:"status"`
	Involvement string       `json:"involvement"`
	Effort      string       `json:"effort"`
	Span        string       `json:"span"`
	Initiative  *Ref         `json:"initiative"`
	Repos       []string     `json:"repos"`
	Description string       `json:"description"`
	Members     []packIssue  `json:"members"`
	Features    []packIssue  `json:"features"`
	Comments    []packText   `json:"comments"`
	Changes     []packChange `json:"changes"`
	Bodies      []packText   `json:"bodies"`
	Subjects    []string     `json:"subjects"`
	Docs        []Ref        `json:"docs"`
	Paths       []packPath   `json:"paths"`
	Doc         *packDoc     `json:"doc"`
	FromUser    string       `json:"from_user"`
	Omitted     packOmitted  `json:"omitted"`
}

type packIssue struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	Involvement string `json:"involvement"`
	DoneByMe    bool   `json:"done_by_me"`
	Title       string `json:"title"`
}

type packText struct {
	Ref  string `json:"ref"` // what it belongs to: an issue key or a change id
	Day  string `json:"day"`
	Text string `json:"text"`
}

type packChange struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Day    string `json:"day"`
	State  string `json:"state"`
	Class  string `json:"class"`
	Lines  string `json:"lines"`
	Title  string `json:"title"`
	weight int
}

type packPath struct {
	Path  string `json:"path"`
	Files int    `json:"files"`
}

type packDoc struct {
	Space     string   `json:"space"`
	Ancestors []string `json:"ancestors"`
	Labels    []string `json:"labels"`
	Created   string   `json:"created"`
	Mine      string   `json:"mine"` // "created by you; 7 of 9 versions yours"
	Live      bool     `json:"live"`
	Body      string   `json:"body"`
}

type packOmitted struct {
	Members  int `json:"members"`
	Comments int `json:"comments"`
	Changes  int `json:"changes"`
	Subjects int `json:"subjects"`
}

var (
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	// lines of a description that are its template rather than its content: link lists an
	// integration adds, headings, checklists
	templateRE = regexp.MustCompile(`(?m)^\s*(\[[^\]]+\]:\s*\S+.*|#{1,6}\s.*|[-*]\s*\[[ xX]\].*)$`)
	wordsRE    = regexp.MustCompile(`[A-Za-z]{3,}`)
)

// excerpt is a change description worth reading: without comments, its template, or keys alone.
func excerpt(body string) string {
	body = oneLine(templateRE.ReplaceAllString(htmlComment.ReplaceAllString(body, ""), ""))
	if len(wordsRE.FindAllString(keyRE.ReplaceAllString(body, ""), -1)) < 4 {
		return ""
	}
	return clip(body, maxBody)
}

// clip shortens text to n characters at most, marking the cut as the raw data does.
func clip(text string, n int) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	r := []rune(text)
	return strings.TrimSpace(string(r[:n])) + fmt.Sprintf(" … [+%d characters]", len(r)-n)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// buildPacks makes every item's pack input and its hash, then decides the notes' states.
func (f *Facts) buildPacks() {
	f.subjects = map[string]string{}
	for i := range f.raw.Commits {
		c := &f.raw.Commits[i]
		f.subjects[c.Repo+"@"+c.SHA] = c.Subject
	}
	for _, it := range f.Items {
		it.pack = f.packInput(it)
		b, _ := json.Marshal(it.pack)
		sum := sha256.Sum256(b)
		it.Pack = "sha256:" + hex.EncodeToString(sum[:])
	}
	f.orphans = f.noteStates()
}

func (f *Facts) packInput(it *Item) *packInput {
	in := &packInput{
		Version: PackVersion, ID: it.ID, Kind: it.Kind, Title: it.Title, URL: it.URL, Type: it.Type, Status: it.Status,
		Involvement: it.Involvement, Effort: it.Effort, Span: it.First + ".." + it.Last, Initiative: it.Initiative,
		Repos: it.Repos, Members: []packIssue{}, Features: []packIssue{}, Comments: []packText{}, Changes: []packChange{},
		Bodies: []packText{}, Subjects: []string{}, Docs: []Ref{}, Paths: []packPath{},
		FromUser: clip(f.context[itemPath(it.ID)], maxFromUser),
	}
	if it.own != nil {
		in.Description = clip(it.own.rec.Description, maxDescription)
	}
	issue := func(key string) packIssue {
		is := f.issues[key]
		return packIssue{Key: is.Key, Type: is.Type, Status: is.StatusCategory, Involvement: is.Involvement, DoneByMe: is.DoneByMe, Title: is.Title}
	}
	members := it.Issues
	if it.own != nil && len(members) > 0 {
		members = members[1:]
	}
	for i, k := range members {
		if i == maxMembers {
			in.Omitted.Members = len(members) - maxMembers
			break
		}
		in.Members = append(in.Members, issue(k))
	}
	for _, id := range it.Features {
		if feature := f.items[id]; feature.own != nil {
			pi := issue(feature.own.Key)
			pi.Involvement = feature.Involvement // the user's part in the feature, sub-tasks included
			in.Features = append(in.Features, pi)
		}
	}

	// The user's own comments on its issues, oldest first.
	var comments []packText
	for _, k := range it.Issues {
		for _, c := range f.issues[k].rec.Comments {
			if at, ok := parseTime(c.CreatedAt); ok && c.ByUser && strings.TrimSpace(c.Body) != "" {
				comments = append(comments, packText{Ref: k, Day: day(at), Text: clip(oneLine(c.Body), maxComment)})
			}
		}
	}
	slices.SortStableFunc(comments, func(a, b packText) int { return cmp.Or(cmp.Compare(a.Day, b.Day), cmp.Compare(a.Ref, b.Ref)) })
	if len(comments) > maxComments {
		in.Omitted.Comments, comments = len(comments)-maxComments, comments[:maxComments]
	}
	in.Comments = append(in.Comments, comments...)

	// Its changes: the largest when there are many, in date order; where they were; what the user's
	// commits said; the largest PRs' descriptions.
	var changes []packChange
	paths := map[string]map[string]bool{}
	var subjects []string
	seen := map[string]bool{}
	for _, id := range it.Changes {
		c := f.changes[id]
		pc := packChange{ID: c.ID, Kind: c.Kind, Day: day(c.at), State: c.State, Class: c.Class, Title: oneLine(c.Title)}
		if c.Landed && c.Class != CoAuthor {
			pc.Lines = fmt.Sprintf("+%d -%d", c.Lines.Added, c.Lines.Deleted)
			pc.weight = c.Lines.Added + c.Lines.Deleted
			for _, fc := range c.files {
				if !fc.generated && !fc.bulky {
					dir := topDir(fc.path)
					if paths[dir] == nil {
						paths[dir] = map[string]bool{}
					}
					paths[dir][fc.path] = true
				}
			}
		}
		changes = append(changes, pc)
		for _, sha := range c.Commits {
			if s := oneLine(f.subjects[c.Repo+"@"+sha]); s != "" && !seen[s] && s != pc.Title {
				seen[s] = true
				subjects = append(subjects, s)
			}
		}
	}
	byWeight := slices.Clone(changes)
	slices.SortStableFunc(byWeight, func(a, b packChange) int { return cmp.Or(cmp.Compare(b.weight, a.weight), cmp.Compare(a.ID, b.ID)) })
	if len(changes) > maxChanges {
		keep := map[string]bool{}
		for _, c := range byWeight[:maxChanges] {
			keep[c.ID] = true
		}
		changes = slices.DeleteFunc(changes, func(c packChange) bool { return !keep[c.ID] })
		in.Omitted.Changes = len(it.Changes) - maxChanges
	}
	in.Changes = append(in.Changes, changes...)
	for _, pc := range byWeight {
		if len(in.Bodies) == maxBodies {
			break
		}
		if body := excerpt(f.changes[pc.ID].body); body != "" && pc.Kind != KindCommit {
			in.Bodies = append(in.Bodies, packText{Ref: pc.ID, Day: pc.Day, Text: body})
		}
	}
	if len(subjects) > maxSubjects {
		in.Omitted.Subjects, subjects = len(subjects)-maxSubjects, subjects[:maxSubjects]
	}
	in.Subjects = append(in.Subjects, subjects...)
	for _, dir := range sortedKeys(paths) {
		in.Paths = append(in.Paths, packPath{Path: dir, Files: len(paths[dir])})
	}
	slices.SortStableFunc(in.Paths, func(a, b packPath) int { return cmp.Compare(b.Files, a.Files) })
	in.Paths = in.Paths[:min(len(in.Paths), maxPaths)]
	for _, ref := range it.Docs {
		if d := f.docByRef(ref); d != nil {
			in.Docs = append(in.Docs, Ref{ID: d.Ref, Title: d.Title})
		}
	}
	if d := it.doc; d != nil {
		mine := fmt.Sprintf("%d of %d versions yours", d.MyVersions, d.Versions)
		if d.CreatedByMe {
			mine = "created by you; " + mine
		}
		in.Doc = &packDoc{Space: d.Space, Ancestors: d.Ancestors, Labels: d.Labels, Created: dayOf(d.CreatedAt), Mine: mine,
			Live: d.Live, Body: clip(deref(d.rec.Body), maxDocBody)}
	}
	return in
}

func (f *Facts) docByRef(ref string) *Doc {
	for _, d := range f.Docs {
		if d.Ref == ref {
			return d
		}
	}
	return nil
}

// topDir is the directory a file sits in, to two levels.
func topDir(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) <= 1 {
		return "(root)"
	}
	return strings.Join(parts[:min(2, len(parts)-1)], "/") + "/"
}

func dayOf(ts string) string {
	if t, ok := parseTime(ts); ok {
		return day(t)
	}
	return ""
}

// renderPack writes a pack input as the Markdown an agent reads, headed by the frontmatter a note
// written from it carries.
func renderPack(in *packInput, hash string) []byte {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	w("# %s · %s\n\n", in.ID, in.Title)
	w("A note written from this pack goes in notes/%s.md and starts:\n\n", itemPath(in.ID))
	w("```\n---\nitem: %s\ninputs: %s\n---\n```\n\n", in.ID, hash)
	w("- kind: %s", in.Kind)
	if in.Type != "" {
		w(" (%s)", in.Type)
	}
	if in.Status != "" {
		w(", status: %s", in.Status)
	}
	w("\n")
	if in.Involvement != "" {
		w("- your involvement in it: %s\n", in.Involvement)
	}
	w("- effort: %s, active %s\n", in.Effort, in.Span)
	if in.Initiative != nil {
		w("- initiative: %s · %s\n", in.Initiative.ID, in.Initiative.Title)
	}
	if len(in.Repos) > 0 {
		w("- repos: %s\n", strings.Join(in.Repos, ", "))
	}
	if in.URL != "" {
		w("- link: %s\n", in.URL)
	}
	if in.Description != "" {
		w("\n## Description\n\n%s\n", in.Description)
	}
	if d := in.Doc; d != nil {
		w("\n## The page\n\n- space: %s", d.Space)
		if len(d.Ancestors) > 0 {
			w(" › %s", strings.Join(d.Ancestors, " › "))
		}
		w("\n- created %s; %s", d.Created, d.Mine)
		if d.Live {
			w("; a live doc (no publish step)")
		}
		if len(d.Labels) > 0 {
			w("\n- labels: %s", strings.Join(d.Labels, ", "))
		}
		w("\n\n%s\n", d.Body)
	}
	if len(in.Features) > 0 {
		w("\n## Features under it\n\n")
		for _, m := range in.Features {
			w("- %s · %s · %s · %s\n", m.Key, m.Status, m.Involvement, m.Title)
		}
	}
	if len(in.Members) > 0 {
		w("\n## Issues in it\n\n")
		for _, m := range in.Members {
			done := ""
			if m.DoneByMe && m.Involvement != Done {
				done = ", done by you"
			}
			w("- %s · %s · %s · %s%s · %s\n", m.Key, m.Type, m.Status, m.Involvement, done, m.Title)
		}
		if in.Omitted.Members > 0 {
			w("- … %d more\n", in.Omitted.Members)
		}
	}
	if len(in.Comments) > 0 {
		w("\n## Your comments\n\n")
		for _, c := range in.Comments {
			w("- %s on %s: %s\n", c.Day, c.Ref, c.Text)
		}
		if in.Omitted.Comments > 0 {
			w("- … %d more\n", in.Omitted.Comments)
		}
	}
	if len(in.Changes) > 0 {
		w("\n## Changes\n\n")
		for _, c := range in.Changes {
			w("- %s %s · %s %s", c.Day, c.ID, c.Kind, c.State)
			if c.Class != Self {
				w(" (%s)", c.Class)
			}
			if c.Lines != "" {
				w(" %s", c.Lines)
			}
			w(" · %s\n", c.Title)
		}
		if in.Omitted.Changes > 0 {
			w("- … %d more, smaller\n", in.Omitted.Changes)
		}
	}
	if len(in.Bodies) > 0 {
		w("\n## Descriptions of the largest changes\n\n")
		for _, t := range in.Bodies {
			w("- %s: %s\n", t.Ref, t.Text)
		}
	}
	if len(in.Subjects) > 0 {
		w("\n## Your commit messages\n\n")
		for _, s := range in.Subjects {
			w("- %s\n", s)
		}
		if in.Omitted.Subjects > 0 {
			w("- … %d more\n", in.Omitted.Subjects)
		}
	}
	if len(in.Paths) > 0 {
		w("\n## Where the changes were\n\n")
		for _, p := range in.Paths {
			w("- %s (%d files)\n", p.Path, p.Files)
		}
	}
	if len(in.Docs) > 0 {
		w("\n## Wiki pages naming it\n\n")
		for _, d := range in.Docs {
			w("- %s · %s\n", d.ID, d.Title)
		}
	}
	if in.FromUser != "" {
		w("\n## From you\n\n%s\n", in.FromUser)
	}
	out := b.String()
	if len(out) > maxPackBytes {
		cut := strings.LastIndex(out[:maxPackBytes], "\n")
		out = out[:cut+1] + fmt.Sprintf("\n… [pack cut at %d KiB]\n", maxPackBytes>>10)
	}
	return []byte(out)
}
