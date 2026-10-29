// Package codehost is the seam to the service hosting an org's git repositories. GitHub is the
// adapter today (codehost/github); GitLab is the expected second. Repo, PullRequest and
// ReviewedPullRequest are also output records (repos.json, prs.jsonl, reviews.jsonl), so their JSON
// names are part of docs/data-schema.md.
package codehost

import "context"

// Host is one org on a code host, as the authenticated account sees it.
//
// Account, Repos, PullRequests, Reviewed and AttributedCommits call the host's API and deal with its
// pagination, search caps and rate limits. Adapters authenticate on first use, so Remote,
// CommitURL, FileURL, PullRequestNumbers, MigratedPullRequestNumbers and RepoPath work without
// credentials.
type Host interface {
	// Account is the authenticated user.
	Account(ctx context.Context) (Account, error)
	// Repos lists every repository in the org that the account can see.
	Repos(ctx context.Context) ([]Repo, error)
	// PullRequests lists every change request author opened in the org, in any state, with its
	// commits and files.
	PullRequests(ctx context.Context, author string) ([]PullRequest, error)
	// Reviewed lists every change request in the org that someone other than reviewer opened and
	// reviewer reviewed or commented on, in any state, with only reviewer's submitted reviews (and
	// their inline comments) and conversation comments. Pending reviews are left out.
	Reviewed(ctx context.Context, reviewer string) ([]ReviewedPullRequest, error)
	// AttributedCommits lists the default-branch commits the host links to author's account,
	// through any email verified on it. A positive limit asks for a sample of about that many,
	// split between the newest and the oldest: enough to discover the emails author commits with.
	AttributedCommits(ctx context.Context, author string, limit int) ([]AttributedCommit, error)

	// Remote says how to mirror repo.
	Remote(repo Repo) Remote
	// CommitURL is the web page of a commit.
	CommitURL(repo Repo, sha string) string
	// FileURL is the web page of a file on a branch.
	FileURL(repo Repo, branch, path string) string
	// PullRequestNumbers finds the change requests a commit message says it merged, by the host's
	// merge and squash conventions.
	PullRequestNumbers(message string) []int
	// MigratedPullRequestNumbers finds the change requests a commit message says it merged on a
	// host the repository was migrated from: history imported with a repository keeps that host's
	// merge messages, and their numbers are not this host's.
	MigratedPullRequestNumbers(message string) []int
	// RepoPath extracts the repository path ("owner/name") that a reference names: an owner/name
	// slug, or a web or clone URL on this host.
	RepoPath(ref string) (path string, ok bool)
}

// Account is a user on the code host.
type Account struct {
	Login     string
	ID        int64
	Name      string
	CreatedAt string
	// NoReplyEmails are the addresses the host commits web edits as, for the identity config.
	NoReplyEmails []string
}

// AttributedCommit is a commit the host links to an account.
type AttributedCommit struct {
	Repo        string
	SHA         string
	Date        string
	AuthorName  string
	AuthorEmail string
	Subject     string
}

// Remote is how to mirror one repository.
type Remote struct {
	URL string
	// PullRequestRefspec fetches change-request heads to refs/pull/<number>/head, the layout the
	// scraper reads whatever the host calls them (GitLab: +refs/merge-requests/*/head:refs/pull/*/head).
	PullRequestRefspec string
	// GitConfig is git configuration for talking to the host (credential helpers), applied in order.
	GitConfig []GitSetting
}

// GitSetting is one git config key and value.
type GitSetting struct{ Key, Value string }

// Repo is one org repository's metadata (<data>/org/repos.json).
type Repo struct {
	Name                  string     `json:"name"`
	FullName              string     `json:"full_name"`
	URL                   string     `json:"url"`
	Description           *string    `json:"description"`
	Homepage              *string    `json:"homepage"`
	DefaultBranch         *string    `json:"default_branch"`
	Visibility            string     `json:"visibility"`
	IsArchived            bool       `json:"is_archived"`
	IsEmpty               bool       `json:"is_empty"`
	IsFork                bool       `json:"is_fork"`
	IsTemplate            bool       `json:"is_template"`
	IsMirror              bool       `json:"is_mirror"`
	ForkOf                *string    `json:"fork_of"`
	GeneratedFromTemplate *string    `json:"generated_from_template"`
	CreatedAt             string     `json:"created_at"`
	UpdatedAt             string     `json:"updated_at"`
	PushedAt              *string    `json:"pushed_at"`
	ArchivedAt            *string    `json:"archived_at"`
	DiskUsageKB           *int64     `json:"disk_usage_kb"`
	PrimaryLanguage       *string    `json:"primary_language"`
	Languages             []Language `json:"languages"`
	Topics                []string   `json:"topics"`
}

// DefaultBranchName is the default branch, or "" for an empty repository.
func (r Repo) DefaultBranchName() string {
	if r.DefaultBranch == nil {
		return ""
	}
	return *r.DefaultBranch
}

type Language struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// PullRequest is a change request (pull request or merge request) with its commits and files.
type PullRequest struct {
	Repo               string         `json:"repo"`
	Number             int            `json:"number"`
	URL                string         `json:"url"`
	Title              string         `json:"title"`
	Body               string         `json:"body"`
	State              string         `json:"state"`
	IsDraft            bool           `json:"is_draft"`
	CreatedAt          string         `json:"created_at"`
	UpdatedAt          string         `json:"updated_at"`
	ClosedAt           *string        `json:"closed_at"`
	MergedAt           *string        `json:"merged_at"`
	Author             *string        `json:"author"`
	MergedBy           *string        `json:"merged_by"`
	BaseRef            string         `json:"base_ref"`
	HeadRef            string         `json:"head_ref"`
	HeadSHA            string         `json:"head_sha"`
	MergeCommitSHA     *string        `json:"merge_commit_sha"`
	Additions          int            `json:"additions"`
	Deletions          int            `json:"deletions"`
	ChangedFiles       int            `json:"changed_files"`
	ReviewDecision     *string        `json:"review_decision"`
	Labels             []string       `json:"labels"`
	Assignees          []string       `json:"assignees"`
	CommentCount       int            `json:"comment_count"`
	ReviewCount        int            `json:"review_count"`
	Reviews            []Review       `json:"reviews"`
	RequestedReviewers []*string      `json:"requested_reviewers"`
	ClosingIssues      []ClosingIssue `json:"closing_issues"`
	CommitCount        int            `json:"commit_count"`
	Commits            []PRCommit     `json:"commits"`
	FileCount          int            `json:"file_count"`
	Files              []PRFile       `json:"files"`
}

type Review struct {
	Author      *string `json:"author"`
	State       string  `json:"state"`
	SubmittedAt *string `json:"submitted_at"`
}

// ReviewedPullRequest is a change request someone else opened, with what the reviewer left on it.
type ReviewedPullRequest struct {
	Repo          string           `json:"repo"`
	Number        int              `json:"number"`
	URL           string           `json:"url"`
	Title         string           `json:"title"`
	Body          string           `json:"body"`
	State         string           `json:"state"`
	IsDraft       bool             `json:"is_draft"`
	CreatedAt     string           `json:"created_at"`
	ClosedAt      *string          `json:"closed_at"`
	MergedAt      *string          `json:"merged_at"`
	Author        *string          `json:"author"`
	AuthorIsBot   bool             `json:"author_is_bot"`
	MergedBy      *string          `json:"merged_by"`
	BaseRef       string           `json:"base_ref"`
	HeadRef       string           `json:"head_ref"`
	Additions     int              `json:"additions"`
	Deletions     int              `json:"deletions"`
	ChangedFiles  int              `json:"changed_files"`
	ClosingIssues []ClosingIssue   `json:"closing_issues"`
	Reviews       []ReviewerReview `json:"reviews"`  // the reviewer's, oldest first
	Comments      []Comment        `json:"comments"` // the reviewer's conversation comments, oldest first
}

// Review states, as ReviewerReview records them.
const (
	ReviewApproved         = "approved"
	ReviewChangesRequested = "changes_requested"
	ReviewCommented        = "commented"
	ReviewDismissed        = "dismissed"
)

// ReviewerReview is one review the reviewer submitted, with its inline comments.
type ReviewerReview struct {
	ID          string          `json:"id"`
	URL         string          `json:"url"`
	State       string          `json:"state"` // ReviewApproved, ReviewChangesRequested, ReviewCommented or ReviewDismissed
	SubmittedAt string          `json:"submitted_at"`
	Body        string          `json:"body"`
	Comments    []ReviewComment `json:"comments"`
}

// ReviewComment is a comment on a line of a change request's diff.
type ReviewComment struct {
	ID        string  `json:"id"`
	URL       string  `json:"url"`
	Path      string  `json:"path"`
	Body      string  `json:"body"`
	CreatedAt string  `json:"created_at"`
	InReplyTo *string `json:"in_reply_to"` // the comment this answers, in a thread
}

// Comment is a comment on a change request's conversation.
type Comment struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

type ClosingIssue struct {
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
}

type PRCommit struct {
	SHA         string  `json:"sha"`
	Headline    string  `json:"headline"`
	AuthoredAt  string  `json:"authored_at"`
	AuthorName  *string `json:"author_name"`
	AuthorEmail *string `json:"author_email"`
	AuthorLogin *string `json:"author_login"`
	IsMerge     bool    `json:"is_merge"` // more than one parent: a sync with the base branch, not work
}

type PRFile struct {
	Path       string `json:"path"`
	Additions  int    `json:"additions"`
	Deletions  int    `json:"deletions"`
	ChangeType string `json:"change_type"`
}
