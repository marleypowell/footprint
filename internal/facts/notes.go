package facts

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// NoteSections are the headings every note has, in order.
var NoteSections = []string{"Summary", "What I did", "Outcome", "Skills", "Evidence", "Open questions"}

// Note states.
const (
	NoteCurrent  = "current"
	NoteStale    = "stale"    // written from an earlier pack: revise it
	NoteMissing  = "missing"  // required (effort M or more, or a doc), not written
	NoteInvalid  = "invalid"  // its frontmatter or sections are wrong: see footprint check
	NoteRenamed  = "renamed"  // filed under a key the issue had before: rename it
	NoteOptional = "optional" // not required, and not written
	NoteOrphan   = "orphan"   // names no item
)

// noteFile is a note as read from the notes directory.
type noteFile struct {
	path   string
	item   string // frontmatter
	inputs string // frontmatter: the pack hash it was written from
	// shows and framework are its tags: the criteria of the framework it shows, and the hash of
	// the framework they are of
	shows     []string
	framework string
	body      string
	err       string // why it is invalid; "" when it is well formed
}

// readNotes reads every <id>.md in dir. A missing directory has no notes.
func readNotes(dir string) (map[string]*noteFile, error) {
	notes := map[string]*noteFile{}
	if dir == "" {
		return notes, nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return notes, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		n := parseNote(path, string(data))
		notes[strings.TrimSuffix(e.Name(), ".md")] = n
	}
	return notes, nil
}

func parseNote(path, text string) *noteFile {
	n := &noteFile{path: path}
	front, body, ok := Frontmatter(text)
	n.body = body
	if !ok {
		n.err = "no frontmatter: want ---, item: <id>, inputs: <pack hash>, ---"
		return n
	}
	n.item, n.inputs = front["item"], front["inputs"]
	n.shows, n.framework = tagList(front["shows"]), front["framework"]
	var missing []string
	headings := headingsOf(body)
	for _, s := range NoteSections {
		if !slices.Contains(headings, s) {
			missing = append(missing, s)
		}
	}
	switch {
	case n.item == "" || n.inputs == "":
		n.err = "frontmatter needs item and inputs"
	case len(missing) > 0:
		n.err = "missing sections: " + strings.Join(missing, ", ")
	}
	return n
}

// Frontmatter splits a Markdown file into its leading "---" block of key: value lines and the rest.
// Lines may end in \n or \r\n.
func Frontmatter(text string) (map[string]string, string, bool) {
	all := text
	text = strings.TrimPrefix(text, "\ufeff")
	first, rest, ok := strings.Cut(text, "\n")
	if !ok || strings.TrimSpace(first) != "---" {
		return nil, all, false
	}
	front := map[string]string{}
	for rest != "" {
		line, next, _ := strings.Cut(rest, "\n")
		if strings.TrimSpace(line) == "---" {
			return front, next, true
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			front[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
		rest = next
	}
	return nil, all, false
}

// headingsOf lists the ## headings of a Markdown body.
func headingsOf(body string) []string {
	var hs []string
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(t, "## ") {
			hs = append(hs, strings.TrimSpace(strings.TrimPrefix(t, "## ")))
		}
	}
	return hs
}

// required says whether an item needs a note: effort M or more, or a doc.
func required(it *Item) bool {
	return it.Kind == KindDoc || effortRank(it.Effort) <= effortRank("M")
}

// noteStates decides every item's note state, and lists the notes that name no item.
func (f *Facts) noteStates() (orphans []string) {
	used := map[string]bool{}
	for _, it := range f.Items {
		n := f.notes[itemPath(it.ID)]
		switch {
		case n == nil:
			it.Note = NoteOptional
			if required(it) {
				it.Note = NoteMissing
				if it.own != nil {
					for _, k := range it.own.PreviousKeys {
						if f.notes[itemPath(k)] != nil {
							it.Note = NoteRenamed
							used[itemPath(k)] = true
						}
					}
				}
			}
		case n.err != "" || n.item != it.ID:
			it.Note = NoteInvalid
		case n.inputs != it.Pack:
			it.Note = NoteStale
		default:
			it.Note = NoteCurrent
		}
		used[itemPath(it.ID)] = true
	}
	for _, name := range sortedKeys(f.notes) {
		if !used[name] {
			orphans = append(orphans, name)
		}
	}
	return orphans
}

// readContext reads the user's own words: context/items/<id>.md per item, and every file, for the
// numbers they stated.
func readContext(dir string) (map[string]string, []string, error) {
	items, all := map[string]string{}, []string{}
	if dir == "" {
		return items, all, nil
	}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == dir {
			return filepath.SkipDir
		}
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		all = append(all, string(data))
		if rel, _ := filepath.Rel(dir, path); filepath.Dir(rel) == "items" {
			items[strings.TrimSuffix(filepath.Base(rel), ".md")] = strings.TrimSpace(string(data))
		}
		return nil
	})
	return items, all, err
}
