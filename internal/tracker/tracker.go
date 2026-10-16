// Package tracker is the seam to the issue tracker whose work items commits and pull requests
// mention. Jira is the adapter today (tracker/jira); Linear, whose TEAM-123 identifiers have the
// same shape, is the expected second.
package tracker

// Tracker recognises references to the tracker's work items in free text.
type Tracker interface {
	// Keys returns the sorted, de-duplicated issue keys mentioned in the texts.
	Keys(texts ...string) []string
	// Projects lists the tracker's projects (or teams), for decoding keys. It may be empty.
	Projects() []Project
}

// Project is one project (or team) in the tracker (<data>/org/issue_projects.json).
type Project struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Archived bool   `json:"archived"`
}
