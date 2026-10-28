package facts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/marleypowell/footprint"
	"github.com/marleypowell/footprint/internal/collect"
)

// Summary is facts.json: the numbers for one window, by name, and what they leave out.
type Summary struct {
	PackVersion int           `json:"pack_version"`
	Window      string        `json:"window"` // "all" or "YYYY-MM-DD..YYYY-MM-DD"
	Coverage    Coverage      `json:"coverage"`
	Totals      Totals        `json:"totals"`
	ByYear      []Period      `json:"by_year"`
	ByQuarter   []Period      `json:"by_quarter"`
	TeamSignals []TeamSignals `json:"team_signals"`
	// Technologies are those with the user's activity in the window, as of the window
	Technologies []TechSummary `json:"technologies"`
	Excluded     Excluded      `json:"excluded"`
	Notes        NoteCounts    `json:"notes"`
}

// Coverage says what the dataset can and cannot show.
type Coverage struct {
	Sources      collect.Sources `json:"sources"`
	First        string          `json:"first"` // the first and last day of anything counted
	Last         string          `json:"last"`
	PRsFrom      string          `json:"prs_from"` // the first PR record: earlier PRs show only as migrated_prs_merged
	NotCollected []string        `json:"not_collected"`
}

// NoteCounts count the window's items by note state.
type NoteCounts struct {
	Required int `json:"required"`
	Current  int `json:"current"`
	Stale    int `json:"stale"`
	Missing  int `json:"missing"`
	Invalid  int `json:"invalid"`
	Renamed  int `json:"renamed"`
	Optional int `json:"optional"`
	Orphans  int `json:"orphans"`
}

var notCollected = []string{
	"reviews of other people's PRs, and comments on PRs", // reviewsNotCollected once the reviews stage ran
	"the descriptions of PRs merged on a host repos were migrated from (only their squash commits' messages)",
	"issues the user only commented on, edited or watched, unless their git work names them",
	"comments on other people's wiki pages, and the text of earlier page versions",
	"work outside the code host, issue tracker and wiki: design talks, incidents, interviews, mentoring, meetings",
}

// reviewsNotCollected is what the reviews stage leaves out.
const reviewsNotCollected = "the user's replies to reviews on their own PRs, and reviews on a host repos were migrated from"

// notCollected says what the dataset leaves out.
func (f *Facts) notCollected() []string {
	out := slices.Clone(notCollected)
	if f.raw.ReviewsRan {
		out[0] = reviewsNotCollected
	}
	return out
}

// Summary is the window's facts.json.
func (f *Facts) Summary(w Window) Summary {
	s := Summary{PackVersion: PackVersion, Window: w.String(), Totals: totalsOf(f.events, w), Excluded: f.Excluded}
	s.ByYear, s.ByQuarter = periods(f.events, w)
	s.TeamSignals = f.teamSignals(w)
	s.Technologies = f.techSummaries(w)
	s.Coverage = Coverage{Sources: f.raw.Index.Sources, NotCollected: f.notCollected()}
	s.Coverage.First, s.Coverage.Last = span(f.events, w)
	for _, pr := range f.raw.PRs {
		if d := dayOf(pr.CreatedAt); d != "" && (s.Coverage.PRsFrom == "" || d < s.Coverage.PRsFrom) {
			s.Coverage.PRsFrom = d
		}
	}
	for _, it := range f.windowItems(w) {
		if required(it) {
			s.Notes.Required++
		}
		switch it.Note {
		case NoteCurrent:
			s.Notes.Current++
		case NoteStale:
			s.Notes.Stale++
		case NoteMissing:
			s.Notes.Missing++
		case NoteInvalid:
			s.Notes.Invalid++
		case NoteRenamed:
			s.Notes.Renamed++
		case NoteOptional:
			s.Notes.Optional++
		}
	}
	s.Notes.Orphans = len(f.orphans)
	return s
}

// windowItems are the items with something counted in w, as of w: their totals, days and span are
// the window's (their effort stays the item's).
func (f *Facts) windowItems(w Window) []*Item {
	if w.IsAll() {
		return f.Items
	}
	var out []*Item
	for _, it := range f.Items {
		evs := f.rollup(it)
		first, last := span(evs, w)
		if first == "" {
			continue
		}
		c := *it
		c.Totals, c.days, c.First, c.Last = totalsOf(evs, w), activeDays(evs, w), first, last
		c.ActiveDays = len(c.days)
		out = append(out, &c)
	}
	return out
}

// Write writes the facts to dir, replacing it whole: the all-time files, and a windows/<window>/
// view for each of windows and for each window dir already has.
func (f *Facts) Write(dir string, windows ...Window) error {
	existing, err := os.ReadDir(filepath.Join(dir, "windows"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, e := range existing {
		if from, to, ok := strings.Cut(e.Name(), "_"); ok && e.IsDir() {
			if w, err := NewWindow(from, to); err == nil && !slices.Contains(windows, w) {
				windows = append(windows, w)
			}
		}
	}
	tmp := dir + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	files := map[string][]byte{"README.md": footprint.FactsSchema}
	var errs []error
	put := func(name string) func([]byte, error) {
		return func(data []byte, err error) {
			files[name] = data
			errs = append(errs, err)
		}
	}
	put("facts.json")(marshal(f.Summary(All)))
	put("items.jsonl")(jsonl(f.Items))
	put("changes.jsonl")(jsonl(f.Changes))
	put("issues.jsonl")(jsonl(f.Issues))
	put("docs.jsonl")(jsonl(f.Docs))
	put("repos.jsonl")(jsonl(f.Repos))
	put("reviews.jsonl")(jsonl(f.Reviews))
	put("technologies.jsonl")(jsonl(f.Technologies))
	files["overview.md"] = f.overview(All)
	files["catalogue.md"] = f.catalogue()
	files["todo.txt"] = f.todo(All)
	for _, it := range f.Items {
		files[filepath.Join("packs", itemPath(it.ID)+".md")] = renderPack(it.pack, it.Pack)
	}
	for _, w := range windows {
		d := filepath.Join("windows", w.Dir())
		put(filepath.Join(d, "facts.json"))(marshal(f.Summary(w)))
		put(filepath.Join(d, "items.jsonl"))(jsonl(f.windowItems(w)))
		files[filepath.Join(d, "overview.md")] = f.overview(w)
		files[filepath.Join(d, "todo.txt")] = f.todo(w)
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		p := filepath.Join(tmp, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, files[name], 0o644); err != nil {
			return err
		}
	}
	old := dir + ".old"
	if err := os.RemoveAll(old); err != nil {
		return err
	}
	if err := os.Rename(dir, old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return err
	}
	return os.RemoveAll(old)
}

func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encoding facts: %w", err)
	}
	return b.Bytes(), nil
}

func jsonl[T any](rows []T) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			return nil, fmt.Errorf("encoding facts: %w", err)
		}
	}
	return b.Bytes(), nil
}
