package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/marleypowell/footprint/internal/facts"
	"github.com/marleypowell/footprint/internal/logx"
)

// factsOptions are the settings of the facts, check, count and search commands.
type factsOptions struct {
	dir, notes, context string // the facts directory, the notes directory, the user's context
	from, to            string // facts: a window to write too; count, search: the window to look in
	since               string // facts: also write recent/, the items to ask about since this day
	plain, links        bool   // check: write <file>.final.md
	repo, item          string // count, search: only this repo's or work item's
	by                  string // count: break the totals down by year, quarter, repo or item
	tech                string // count: a technology's totals instead of the user's
	kind                string // search: only these kinds of record, comma-separated
}

// window is the window -from and -to give: all time for neither.
func (o factsOptions) window() (facts.Window, error) {
	switch {
	case o.from != "" && o.to != "":
		return facts.NewWindow(o.from, o.to)
	case o.from != "" || o.to != "":
		return facts.Window{}, errors.New("give both -from and -to (YYYY-MM-DD, both days included)")
	}
	return facts.All, nil
}

func (o factsOptions) filter() (facts.Filter, error) {
	w, err := o.window()
	return facts.Filter{Window: w, Repo: o.repo, Item: o.item}, err
}

func (o factsOptions) build(dataDir string) (*facts.Facts, error) {
	raw, err := facts.Load(dataDir)
	if err != nil {
		return nil, err
	}
	return facts.Build(raw, facts.Inputs{NotesDir: o.notes, ContextDir: o.context})
}

// runFacts writes the facts, and a window's view when -from and -to say one.
func runFacts(dataDir string, o factsOptions) error {
	var windows []facts.Window
	if w, err := o.window(); err != nil {
		return fmt.Errorf("facts: %w", err)
	} else if !w.IsAll() {
		windows = append(windows, w)
	}
	f, err := o.build(dataDir)
	if err != nil {
		return err
	}
	if o.since != "" {
		w, err := f.Since(o.since)
		if err != nil {
			return err
		}
		f.Recent = &w
	}
	if err := f.Write(o.dir, windows...); err != nil {
		return err
	}
	all := f.Summary(facts.All)
	logx.Printf("facts: %d work items, %d changes, %d issues, %d docs -> %s", len(f.Items), len(f.Changes), len(f.Issues), len(f.Docs), o.dir)
	report := func(s facts.Summary, todo string) {
		n := s.Notes
		logx.Printf("facts: %s: %d notes needed: %d current, %d stale, %d missing, %d invalid, %d renamed -> %s",
			s.Window, n.Required, n.Current, n.Stale, n.Missing, n.Invalid, n.Renamed, todo)
	}
	report(all, filepath.Join(o.dir, "todo.txt"))
	for _, w := range windows {
		report(f.Summary(w), filepath.Join(o.dir, "windows", w.Dir(), "todo.txt"))
	}
	if f.Recent != nil {
		logx.Printf("facts: %d items to ask about since %s -> %s", len(f.CaptureItems(*f.Recent)), o.since, filepath.Join(o.dir, "recent", "capture.txt"))
	}
	if all.Notes.Orphans > 0 {
		logx.Printf("facts: %d notes name no work item (listed in todo.txt)", all.Notes.Orphans)
	}
	return nil
}

// runCheck checks notes and drafts, and writes their final versions when asked.
func runCheck(dataDir string, o factsOptions, files []string) error {
	if len(files) == 0 {
		return errors.New("check needs files: footprint check <note-or-draft.md> [...]")
	}
	if o.plain && o.links {
		return errors.New("check: -plain or -links, not both")
	}
	f, err := o.build(dataDir)
	if err != nil {
		return err
	}
	failed := 0
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		findings := f.Check(path, string(data))
		errs := 0
		for _, fd := range findings {
			fmt.Printf("%s:%s\n", path, fd)
			if fd.Error {
				errs++
			}
		}
		if errs > 0 {
			failed++
			continue
		}
		if o.plain || o.links {
			final := strings.TrimSuffix(path, ".md") + ".final.md"
			if err := os.WriteFile(final, []byte(f.Final(string(data), o.links)), 0o644); err != nil {
				return err
			}
			fmt.Printf("%s: wrote %s\n", path, final)
		}
		if len(findings) == 0 {
			fmt.Printf("%s: ok\n", path)
		}
	}
	if failed > 0 {
		return fmt.Errorf("check: %d of %d files have errors", failed, len(files))
	}
	return nil
}

// runCount writes the totals of the events the filters pick, or of a technology, as JSON.
func runCount(dataDir string, o factsOptions, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("count takes flags, not %q: -from, -to, -repo, -item, -tech, -by", args[0])
	}
	fl, err := o.filter()
	if err != nil {
		return fmt.Errorf("count: %w", err)
	}
	f, err := o.build(dataDir)
	if err != nil {
		return err
	}
	var c any
	if o.tech != "" {
		c, err = f.CountTech(o.tech, fl, o.by)
	} else {
		c, err = f.Count(fl, o.by)
	}
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(c)
}

// runSearch lists the records whose text has every term, one per line.
func runSearch(dataDir string, o factsOptions, terms []string) error {
	fl, err := o.filter()
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	if len(terms) == 0 && fl == (facts.Filter{}) && o.kind == "" {
		return errors.New("search needs terms or a filter: footprint search [-from -to -repo -item -kind] <term>...")
	}
	var kinds []string
	if o.kind != "" {
		kinds = strings.Split(o.kind, ",")
	}
	f, err := o.build(dataDir)
	if err != nil {
		return err
	}
	hits, err := f.Search(facts.Query{Terms: terms, Kinds: kinds, Filter: fl})
	if err != nil {
		return err
	}
	for _, h := range hits {
		fmt.Println(h)
	}
	logx.Printf("search: %d records", len(hits))
	return nil
}
