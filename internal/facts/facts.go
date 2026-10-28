// Package facts is the deterministic half of the aggregation phase. It reads the collected dataset
// (data/raw) and decides what counts and how much: deduplicated changes, work items, docs, repos
// and totals, with a bounded evidence pack per work item for an agent to write a note from. Build
// is pure and reads no config, tokens or mirrors, so it works on a copy of the dataset; the same
// dataset, notes and context give byte-identical output. The output contract is
// docs/facts-schema.md.
package facts

import (
	"slices"
	"strconv"
	"time"

	"github.com/marleypowell/footprint/internal/collect"
)

// Facts is everything Build decides about one dataset.
type Facts struct {
	Changes  []*Change // by time, then id
	Issues   []*Issue  // by key (natural order)
	Items    []*Item   // by first activity, then id
	Docs     []*Doc    // by creation, then id
	Reviews  []*Review // by repo, then number
	Repos    []*Repo   // by name
	Excluded Excluded  // what line counts leave out

	raw         *Raw
	people      *people
	userID      string             // the user's account in the issue tracker, from their marked activity
	keyOf       map[string]string  // every key an issue answers to (upper case) -> its current key
	issues      map[string]*Issue  // by current key
	items       map[string]*Item   // by id
	changes     map[string]*Change // by id
	changeOf    map[string]string  // repo@sha of a commit of the user's -> the change it is part of
	docs        map[string]*Doc    // by page id
	reviews     map[string]*Review // by ref
	events      []event
	notes       map[string]*noteFile // by file name (the item id, path-encoded), read from the notes directory
	orphans     []string             // notes naming no item
	subjects    map[string]string    // repo@sha -> commit subject
	hosts       map[string]bool      // the hosts of the dataset's links, for check
	urls        map[string]bool
	projectKeys map[string]bool
	context     map[string]string // the user's own words per item id, from context/items/<id>.md
	contexts    []string          // every context file's text, for numbers the user stated
}

// Ref names another record: an item, issue or page.
type Ref struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Inputs are the files besides the dataset that Build reads: notes and the user's context.
type Inputs struct {
	NotesDir   string // <id>.md per item, written by the agent
	ContextDir string // profile.md, framework.md, items/<id>.md: the user's own words
}

// Build decides the facts for raw. It reads the notes and context directories in in, which may be
// empty or absent.
func Build(raw *Raw, in Inputs) (*Facts, error) {
	f := &Facts{
		raw: raw, people: newPeople(raw.Index.Identity), keyOf: map[string]string{}, issues: map[string]*Issue{},
		items: map[string]*Item{}, changes: map[string]*Change{}, changeOf: map[string]string{}, docs: map[string]*Doc{},
	}
	var err error
	if f.context, f.contexts, err = readContext(in.ContextDir); err != nil {
		return nil, err
	}
	f.buildIssues()
	f.buildChanges()
	f.buildDocs()
	f.buildItems()
	f.buildRepos()
	if f.notes, err = readNotes(in.NotesDir); err != nil {
		return nil, err
	}
	f.buildPacks()
	return f, nil
}

// event is one countable thing that happened, at a time; Totals counts them per window.
type event struct {
	at      time.Time
	kind    string // one of the ev* kinds
	key     string // the change, issue or page it is about
	item    string // the item it counts toward; "" for none
	repo    string
	added   int // lines, for evLanded
	deleted int
	who     string // a reviewed PR's author
}

const (
	evActive          = "active"       // a day of the user's own activity
	evPROpened        = "pr_opened"    // one of the user's PRs opened
	evPRMerged        = "pr_merged"    // ... merged
	evMigratedPR      = "migrated_pr"  // a PR on the host the repo was migrated from, landed
	evMergedForOthers = "merged_other" // the user completed someone else's PR there
	evCommit          = "commit"       // one of the user's own commits (one per patch)
	evCoAuthored      = "co_authored"  // a commit naming the user as co-author only
	evAgentCommit     = "agent_commit" // an AI agent's commit in the user's work
	evLanded          = "landed"       // a change reached the default branch, with its lines
	evIssueDone       = "issue_done"
	evIssueRaised     = "issue_raised"
	evIssueWorked     = "issue_worked" // the user acted on an issue: a status change, comment, worklog, or a change naming it
	evPageCreated     = "page_created"
	evPageVersion     = "page_version" // the user saved a version of a page
	evADRVersion      = "adr_version"  // a commit of the user's changed a decision record
	// reviews of other people's PRs: a submitted review (one requesting changes is counted apart),
	// one of a bot's PR, and a comment on a PR's diff or conversation
	evReview           = "review"
	evChangesRequested = "review_changes"
	evBotReview        = "review_bot"
	evReviewComment    = "review_comment"
)

func (f *Facts) emit(e event) {
	if !e.at.IsZero() {
		f.events = append(f.events, e)
	}
}

// sortedSet returns the set's members, sorted naturally, never nil.
func sortedSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.SortFunc(out, collect.NaturalCompare)
	return out
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func itoa(n int) string { return strconv.Itoa(n) }
