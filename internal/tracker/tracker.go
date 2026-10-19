// Package tracker is the seam to the issue tracker: it recognises the keys of the work items that
// commits and pull requests mention, and loads those work items. Jira is the adapter today
// (tracker/jira); Linear, whose TEAM-123 identifiers have the same shape, is the expected second.
// Issue and the types under it are also output records (issues/<project>.jsonl), so their JSON
// names are part of docs/data-schema.md.
package tracker

import "context"

// Tracker is the issue tracker, as the authenticated account sees it.
//
// Keys and Projects work offline, from configuration. Account, Assigned and Issues call the
// tracker's API; adapters authenticate on first use.
type Tracker interface {
	// Keys returns the sorted, de-duplicated issue keys mentioned in the texts.
	Keys(texts ...string) []string
	// Projects lists the tracker's projects (or teams), for decoding keys. It may be empty.
	Projects() []Project

	// Account is the authenticated user.
	Account(ctx context.Context) (Account, error)
	// Assigned lists the keys of every issue that is, or ever was, assigned to account.
	Assigned(ctx context.Context, account Account) ([]string, error)
	// Issues loads issues by key, in full: every comment, change and worklog. A key that cannot be
	// loaded comes back in missing, with the reason; an error means the tracker itself failed. An
	// issue moved since a key was written comes back once, under its current key, with the old keys
	// in PreviousKeys.
	Issues(ctx context.Context, keys []string) (issues []Issue, missing []Missing, err error)
}

// Missing is a key Issues could not load (<data>/issues/unresolved.json).
type Missing struct {
	Key    string `json:"key"`
	Reason string `json:"reason"` // NotFound, Hidden or Failed
	Error  string `json:"error,omitempty"`
}

// Reasons for Missing.
const (
	NotFound = "not_found" // no such issue; Jira also says this when the account may not see it
	Hidden   = "hidden"    // the account may not see it
	Failed   = "failed"    // an error that persisted through retries (Error says which)
)

// Project is one project (or team) in the tracker (<data>/org/issue_projects.json).
type Project struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Archived bool   `json:"archived"`
}

// Account is a user of the tracker.
type Account struct {
	ID    string
	Name  string
	Email string // empty when the tracker hides it
}

// Issue is one work item. Timestamps are UTC ISO 8601, and rich text is Markdown.
type Issue struct {
	Key            string       `json:"key"`
	ID             string       `json:"id"`
	URL            string       `json:"url"`
	PreviousKeys   []string     `json:"previous_keys"`
	Project        ProjectRef   `json:"project"`
	Type           string       `json:"type"`
	Summary        string       `json:"summary"`
	Description    string       `json:"description"`
	Status         string       `json:"status"`
	StatusCategory string       `json:"status_category"` // todo, in_progress or done
	Resolution     *string      `json:"resolution"`
	Priority       *string      `json:"priority"`
	Labels         []string     `json:"labels"`
	Components     []string     `json:"components"`
	FixVersions    []string     `json:"fix_versions"`
	Sprints        []Sprint     `json:"sprints"`
	Estimate       *float64     `json:"estimate"` // story points
	Team           *string      `json:"team"`
	Parent         *IssueRef    `json:"parent"`
	Subtasks       []IssueRef   `json:"subtasks"`
	Links          []IssueLink  `json:"links"`
	Assignee       *User        `json:"assignee"`
	Reporter       *User        `json:"reporter"`
	Creator        *User        `json:"creator"`
	CreatedAt      string       `json:"created_at"`
	UpdatedAt      string       `json:"updated_at"`
	ResolvedAt     *string      `json:"resolved_at"`
	DueDate        *string      `json:"due_date"`
	Comments       []Comment    `json:"comments"`
	History        []Change     `json:"history"`
	Worklogs       []Worklog    `json:"worklogs"`
	Attachments    []Attachment `json:"attachments"`
	// Fields is every other field with a value, keyed by the field's name, in the tracker's own
	// shapes (rich text as Markdown).
	Fields map[string]any `json:"fields"`
}

type ProjectRef struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// IssueRef is another issue, as one issue refers to it.
type IssueRef struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
	Type    string `json:"type"`
	Status  string `json:"status"`
}

// IssueLink is a link to another issue; Relation reads from this issue ("blocks", "is blocked by").
type IssueLink struct {
	Relation string `json:"relation"`
	IssueRef
}

// Sprint is a time box the issue was planned into (Linear: a cycle).
type Sprint struct {
	Name        string  `json:"name"`
	State       string  `json:"state"`
	StartAt     *string `json:"start_at"`
	EndAt       *string `json:"end_at"`
	CompletedAt *string `json:"completed_at"`
	Goal        string  `json:"goal,omitempty"`
}

// Comment is one comment. ByUser is set by the scraper: the user wrote it.
type Comment struct {
	ID        string `json:"id"`
	Author    *User  `json:"author"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Body      string `json:"body"`
	ByUser    bool   `json:"by_user"`
}

// Change is one edit to the issue, of one or more fields, oldest first. ByUser is set by the
// scraper: the user made it.
type Change struct {
	ID        string       `json:"id"`
	Author    *User        `json:"author"`
	CreatedAt string       `json:"created_at"`
	ByUser    bool         `json:"by_user"`
	Items     []ChangeItem `json:"items"`
}

// ChangeItem is one field's change. Field is the tracker's name for the field, except that status
// and assignee changes are always "status" and "assignee", with account IDs in FromID and ToID for
// the assignee. From and To are the values as displayed.
type ChangeItem struct {
	Field   string  `json:"field"`
	FieldID string  `json:"field_id,omitempty"`
	From    *string `json:"from"`
	To      *string `json:"to"`
	FromID  *string `json:"from_id,omitempty"`
	ToID    *string `json:"to_id,omitempty"`
}

// Worklog is time logged against the issue. ByUser is set by the scraper: the user logged it.
type Worklog struct {
	ID        string `json:"id"`
	Author    *User  `json:"author"`
	StartedAt string `json:"started_at"`
	Seconds   int    `json:"seconds"`
	Comment   string `json:"comment"`
	ByUser    bool   `json:"by_user"`
}

// Attachment is a file attached to the issue (metadata only).
type Attachment struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
	MimeType  string `json:"mime_type"`
	Author    *User  `json:"author"`
	CreatedAt string `json:"created_at"`
}
