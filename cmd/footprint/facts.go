package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/marleypowell/footprint/internal/facts"
	"github.com/marleypowell/footprint/internal/logx"
)

// factsOptions are the settings of the facts and check commands.
type factsOptions struct {
	dir, notes, context string // the facts directory, the notes directory, the user's context
	from, to            string // facts: a window to write too
	plain, links        bool   // check: write <file>.final.md
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
	switch {
	case o.from != "" && o.to != "":
		w, err := facts.NewWindow(o.from, o.to)
		if err != nil {
			return err
		}
		windows = append(windows, w)
	case o.from != "" || o.to != "":
		return errors.New("facts: give both -from and -to (YYYY-MM-DD, both days included)")
	}
	f, err := o.build(dataDir)
	if err != nil {
		return err
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
