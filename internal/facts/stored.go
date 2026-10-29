package facts

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/tracker"
)

// FactsVersion is the version of a facts directory's files: a reader of stored facts (LoadStored)
// knows which it can read. It changes when a file check reads back changes its shape.
const FactsVersion = 1

// oldestStoredVersion is the first facts version LoadStored reads: the first with events.jsonl.
const oldestStoredVersion = 1

// StoredEvent is one line of events.jsonl: an event the totals count, so that a facts directory
// can be checked against once its raw data is gone.
type StoredEvent struct {
	At      string `json:"at"`
	Kind    string `json:"kind"`
	Key     string `json:"key"`
	Item    string `json:"item,omitempty"`
	Repo    string `json:"repo,omitempty"`
	Added   int    `json:"added,omitempty"`
	Deleted int    `json:"deleted,omitempty"`
	Living  int    `json:"living,omitempty"`
	Who     string `json:"who,omitempty"`
}

func (f *Facts) storedEvents() []StoredEvent {
	out := make([]StoredEvent, 0, len(f.events))
	for _, e := range f.events {
		out = append(out, StoredEvent{At: e.at.UTC().Format(time.RFC3339Nano), Kind: e.kind, Key: e.key, Item: e.item, Repo: e.repo,
			Added: e.added, Deleted: e.deleted, Living: e.living, Who: e.who})
	}
	return out
}

// LoadStored reads back the facts directory a run of facts wrote, with the context in in: what
// check (and Final) needs, and no more: the result cannot be summarised or written again, for a dataset whose raw data is gone (a past job's, frozen). Every window
// and period can be checked, as the events are kept; a technology's totals are kept for all time
// and by year only, so a draft's other windows have none in scope.
func LoadStored(dir string, in Inputs) (*Facts, error) {
	var s Summary
	ok, err := readJSON(filepath.Join(dir, "facts.json"), &s)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%s has no facts.json: run facts while the raw data is there", dir)
	}
	if s.FactsVersion < oldestStoredVersion || s.FactsVersion > FactsVersion {
		return nil, fmt.Errorf("%s holds facts version %d; this footprint reads versions %d to %d", dir, s.FactsVersion, oldestStoredVersion, FactsVersion)
	}
	if err := VerifyFrozen(dir); err != nil {
		return nil, err
	}
	projects := make([]tracker.Project, 0, len(s.Coverage.ProjectKeys))
	for _, k := range s.Coverage.ProjectKeys {
		projects = append(projects, tracker.Project{Key: k})
	}
	f := &Facts{stored: true, Excluded: s.Excluded, raw: &Raw{Index: collect.Index{Sources: s.Coverage.Sources}, IssueProjects: projects},
		people: newPeople(collect.IdentityConfig{}), keyOf: map[string]string{}, issues: map[string]*Issue{}, items: map[string]*Item{},
		changes: map[string]*Change{}, changeOf: map[string]string{}, docs: map[string]*Doc{}, reviews: map[string]*Review{},
		technologies: map[string]*Technology{}, ownRepos: map[string]*RepoOwnership{}}
	if s.Ownership != nil {
		base := *s.Ownership
		f.ownBase = &base
	}

	items, err := readJSONL[Item](dir, "items.jsonl")
	if err != nil {
		return nil, err
	}
	for i := range items {
		it := &items[i]
		f.Items, f.items[it.ID] = append(f.Items, it), it
	}
	changes, err := readJSONL[Change](dir, "changes.jsonl")
	if err != nil {
		return nil, err
	}
	for i := range changes {
		c := &changes[i]
		c.at, _ = parseTime(c.At)
		f.Changes, f.changes[c.ID] = append(f.Changes, c), c
		for _, sha := range c.Commits {
			f.changeOf[c.Repo+"@"+sha] = c.ID
		}
	}
	issues, err := readJSONL[Issue](dir, "issues.jsonl")
	if err != nil {
		return nil, err
	}
	for i := range issues {
		is := &issues[i]
		f.Issues, f.issues[is.Key] = append(f.Issues, is), is
		f.keyOf[strings.ToUpper(is.Key)] = is.Key
		for _, k := range is.PreviousKeys {
			f.keyOf[strings.ToUpper(k)] = is.Key
		}
	}
	docs, err := readJSONL[Doc](dir, "docs.jsonl")
	if err != nil {
		return nil, err
	}
	for i := range docs {
		d := &docs[i]
		f.Docs, f.docs[d.ID] = append(f.Docs, d), d
		if it := f.items[d.Item]; it != nil && d.Item == d.Ref {
			it.doc = d
		}
	}
	repos, err := readJSONL[Repo](dir, "repos.jsonl")
	if err != nil {
		return nil, err
	}
	for i := range repos {
		r := &repos[i]
		f.Repos = append(f.Repos, r)
		if r.Ownership != nil {
			f.ownRepos[r.Name] = r.Ownership
		}
	}
	reviews, err := readJSONL[Review](dir, "reviews.jsonl")
	if err != nil {
		return nil, err
	}
	for i := range reviews {
		r := &reviews[i]
		f.Reviews, f.reviews[r.Ref] = append(f.Reviews, r), r
	}
	areas, err := readJSONL[Area](dir, "ownership.jsonl")
	if err != nil {
		return nil, err
	}
	for i := range areas {
		f.Areas = append(f.Areas, &areas[i])
	}
	techs, err := readJSONL[Technology](dir, "technologies.jsonl")
	if err != nil {
		return nil, err
	}
	for i := range techs {
		t := &techs[i]
		f.Technologies, f.technologies[t.Name] = append(f.Technologies, t), t
	}
	events, err := readJSONL[StoredEvent](dir, "events.jsonl")
	if err != nil {
		return nil, err
	}
	for _, se := range events {
		at, err := time.Parse(time.RFC3339Nano, se.At)
		if err != nil {
			return nil, fmt.Errorf("events.jsonl: %q: %w", se.At, err)
		}
		e := event{at: at, kind: se.Kind, key: se.Key, item: se.Item, repo: se.Repo, added: se.Added, deleted: se.Deleted, living: se.Living, who: se.Who}
		f.events = append(f.events, e)
		if it := f.items[e.item]; it != nil {
			it.events = append(it.events, e)
		}
	}
	if f.context, f.contexts, err = readContext(in.ContextDir); err != nil {
		return nil, err
	}
	if f.framework, err = readFramework(in.ContextDir); err != nil {
		return nil, err
	}
	if f.evidenceFiles, f.evidenceProblems, err = readEvidence(in.ContextDir); err != nil {
		return nil, err
	}
	return f, nil
}

// Stored says whether the facts were read back from a facts directory rather than built.
func (f *Facts) Stored() bool { return f.stored }
