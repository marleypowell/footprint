// Package wiki is the seam to the wiki the user writes in. Confluence is the adapter today
// (wiki/confluence); Notion is a likely second. Page and the types under it are also output records
// (wiki/<space>.jsonl), so their JSON names are part of docs/data-schema.md.
package wiki

import "context"

// Wiki is the wiki, as the authenticated account sees it. Adapters authenticate on first use.
type Wiki interface {
	// Account is the authenticated user.
	Account(ctx context.Context) (Account, error)
	// Pages lists everything account created or edited, of every content type the wiki has (pages,
	// blog posts, whiteboards, databases, folders, ...), with its version history and, for types
	// that have one, its current body.
	Pages(ctx context.Context, account Account) ([]Page, error)
}

// Account is a user of the wiki.
type Account struct {
	ID    string
	Name  string
	Email string // empty when the wiki hides it
}

// Page is one piece of wiki content. Timestamps are UTC ISO 8601, and the body is Markdown.
type Page struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`    // the wiki's own content type: page, blogpost, whiteboard, ...
	Subtype   *string   `json:"subtype"` // e.g. Confluence's "live" docs
	Status    string    `json:"status"`  // current, archived, ...
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Space     Space     `json:"space"`
	Ancestors []PageRef `json:"ancestors"` // the page tree above it, root first
	Labels    []string  `json:"labels"`
	CreatedAt string    `json:"created_at"`
	CreatedBy *User     `json:"created_by"`
	Owner     *User     `json:"owner"`
	UpdatedAt string    `json:"updated_at"`
	UpdatedBy *User     `json:"updated_by"`
	Version   int       `json:"version"`  // the current version's number
	Versions  []Version `json:"versions"` // oldest first; only the current one for types without a history
	Body      *string   `json:"body"`     // null for types without a body
}

// Space is the space (Notion: teamspace) a page belongs to.
type Space struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type PageRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Version is one saved version. ByUser is set by the scraper: the user saved it.
type Version struct {
	Number    int    `json:"number"`
	Author    *User  `json:"author"`
	CreatedAt string `json:"created_at"`
	Message   string `json:"message"`
	MinorEdit bool   `json:"minor_edit"`
	ByUser    bool   `json:"by_user"`
}
