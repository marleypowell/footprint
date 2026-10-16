package scrape

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PullRequest is one line of <data>/repos/<repo>/prs.jsonl: a PR authored by the user.
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
	Reviews            []PRReview     `json:"reviews"`
	RequestedReviewers []*string      `json:"requested_reviewers"`
	ClosingIssues      []ClosingIssue `json:"closing_issues"`
	JiraKeys           []string       `json:"jira_keys"`
	CommitCount        int            `json:"commit_count"`
	Commits            []PRCommit     `json:"commits"`
	FileCount          int            `json:"file_count"`
	Files              []PRFile       `json:"files"`
}

type PRReview struct {
	Author      *string `json:"author"`
	State       string  `json:"state"`
	SubmittedAt *string `json:"submitted_at"`
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
}

type PRFile struct {
	Path       string `json:"path"`
	Additions  int    `json:"additions"`
	Deletions  int    `json:"deletions"`
	ChangeType string `json:"change_type"`
}

const (
	prCommitFields = `commit { oid messageHeadline authoredDate author { name email user { login } } }`
	prFileFields   = `path additions deletions changeType`
	prFields       = `
  number title body url state isDraft createdAt updatedAt closedAt mergedAt
  repository { name }
  author { login }
  mergedBy { login }
  baseRefName headRefName headRefOid
  mergeCommit { oid }
  additions deletions changedFiles reviewDecision
  labels(first: 30) { nodes { name } }
  assignees(first: 10) { nodes { login } }
  comments { totalCount }
  reviews(first: 100) { totalCount nodes { author { login } state submittedAt } }
  reviewRequests(first: 20) { nodes { requestedReviewer { ... on User { login } ... on Team { name } } } }
  closingIssuesReferences(first: 20) { nodes { number title url repository { nameWithOwner } } }
  commits(first: 100) { totalCount nodes { ` + prCommitFields + ` } }
  files(first: 100) { totalCount nodes { ` + prFileFields + ` } }
`
	searchCountQuery = `query($q: String!) { search(query: $q, type: ISSUE, first: 1) { issueCount } }`
	searchPRsQuery   = `
query($q: String!, $cursor: String) {
  search(query: $q, type: ISSUE, first: 25, after: $cursor) {
    issueCount
    pageInfo { hasNextPage endCursor }
    nodes { ... on PullRequest { ` + prFields + ` } }
  }
}`
	prPageQuery = `
query($owner: String!, $name: String!, $number: Int!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      %s(first: 100, after: $cursor) { totalCount pageInfo { hasNextPage endCursor } nodes { %s } }
    }
  }
}`
)

type connection[T any] struct {
	TotalCount int      `json:"totalCount"`
	PageInfo   pageInfo `json:"pageInfo"`
	Nodes      []T      `json:"nodes"`
}

type actor struct {
	Login string `json:"login"`
}

func login(a *actor) *string {
	if a == nil {
		return nil
	}
	return &a.Login
}

type prCommitNode struct {
	Commit struct {
		Oid             string `json:"oid"`
		MessageHeadline string `json:"messageHeadline"`
		AuthoredDate    string `json:"authoredDate"`
		Author          *struct {
			Name  *string `json:"name"`
			Email *string `json:"email"`
			User  *actor  `json:"user"`
		} `json:"author"`
	} `json:"commit"`
}

type prFileNode struct {
	Path       string `json:"path"`
	Additions  int    `json:"additions"`
	Deletions  int    `json:"deletions"`
	ChangeType string `json:"changeType"`
}

type prNode struct {
	Number      int      `json:"number"`
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	URL         string   `json:"url"`
	State       string   `json:"state"`
	IsDraft     bool     `json:"isDraft"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
	ClosedAt    *string  `json:"closedAt"`
	MergedAt    *string  `json:"mergedAt"`
	Repository  nameNode `json:"repository"`
	Author      *actor   `json:"author"`
	MergedBy    *actor   `json:"mergedBy"`
	BaseRefName string   `json:"baseRefName"`
	HeadRefName string   `json:"headRefName"`
	HeadRefOid  string   `json:"headRefOid"`
	MergeCommit *struct {
		Oid string `json:"oid"`
	} `json:"mergeCommit"`
	Additions      int                  `json:"additions"`
	Deletions      int                  `json:"deletions"`
	ChangedFiles   int                  `json:"changedFiles"`
	ReviewDecision *string              `json:"reviewDecision"`
	Labels         connection[nameNode] `json:"labels"`
	Assignees      connection[actor]    `json:"assignees"`
	Comments       connection[struct{}] `json:"comments"`
	Reviews        connection[struct {
		Author      *actor  `json:"author"`
		State       string  `json:"state"`
		SubmittedAt *string `json:"submittedAt"`
	}] `json:"reviews"`
	ReviewRequests connection[struct {
		RequestedReviewer *struct {
			Login string `json:"login"`
			Name  string `json:"name"`
		} `json:"requestedReviewer"`
	}] `json:"reviewRequests"`
	ClosingIssuesReferences connection[struct {
		Number     int       `json:"number"`
		Title      string    `json:"title"`
		URL        string    `json:"url"`
		Repository ownerNode `json:"repository"`
	}] `json:"closingIssuesReferences"`
	Commits connection[prCommitNode] `json:"commits"`
	Files   connection[prFileNode]   `json:"files"`
}

// prs fetches every PR the user authored in the org, with its commits and files.
func (h *harness) prs(ctx context.Context) error {
	gh, err := h.github(ctx)
	if err != nil {
		return err
	}
	nodes, err := h.searchPRs(ctx, gh, fmt.Sprintf("is:pr author:%s org:%s", h.cfg.GitHubLogin, h.cfg.Org))
	if err != nil {
		return err
	}
	byRepo := map[string]map[int]PullRequest{}
	for _, n := range nodes {
		pr, err := h.flattenPR(ctx, gh, n)
		if err != nil {
			return err
		}
		if byRepo[pr.Repo] == nil {
			byRepo[pr.Repo] = map[int]PullRequest{}
		}
		byRepo[pr.Repo][pr.Number] = pr
	}
	entries, err := os.ReadDir(filepath.Join(h.raw, "repos"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, e := range entries { // repos where the user no longer has PRs
		if _, ok := byRepo[e.Name()]; !ok {
			if err := os.Remove(filepath.Join(h.raw, "repos", e.Name(), "prs.jsonl")); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	total := 0
	for _, repo := range sortedKeys(byRepo) {
		prs := byRepo[repo]
		numbers := make([]int, 0, len(prs))
		for n := range prs {
			numbers = append(numbers, n)
		}
		sort.Ints(numbers)
		rows := make([]PullRequest, len(numbers))
		for i, n := range numbers {
			rows[i] = prs[n]
		}
		if err := writeJSONL(filepath.Join(h.repoDir(repo), "prs.jsonl"), rows); err != nil {
			return err
		}
		total += len(rows)
	}
	logf("prs: %d PRs in %d repos", total, len(byRepo))
	return nil
}

type searchSlice struct {
	query string
	count int
}

// searchSlices splits created: date ranges until each holds at most 1000 results, the GitHub
// search cap.
func (h *harness) searchSlices(ctx context.Context, gh *githubClient, base string) ([]searchSlice, error) {
	type span struct{ lo, hi time.Time }
	pending := []span{{time.Date(h.cfg.SearchStartYear, 1, 1, 0, 0, 0, 0, time.UTC), tomorrow()}}
	var out []searchSlice
	for len(pending) > 0 {
		s := pending[0]
		pending = pending[1:]
		q := fmt.Sprintf("%s created:%s..%s", base, s.lo.Format(dateLayout), s.hi.Format(dateLayout))
		var data struct {
			Search struct {
				IssueCount int `json:"issueCount"`
			} `json:"search"`
		}
		if err := gh.graphql(ctx, searchCountQuery, map[string]any{"q": q}, &data); err != nil {
			return nil, err
		}
		switch count := data.Search.IssueCount; {
		case count > 1000 && s.lo.Before(s.hi):
			mid, next := splitDays(s.lo, s.hi)
			pending = append([]span{{s.lo, mid}, {next, s.hi}}, pending...)
		case count > 0:
			out = append(out, searchSlice{q, count})
		}
	}
	return out, nil
}

func (h *harness) searchPRs(ctx context.Context, gh *githubClient, base string) ([]prNode, error) {
	slices, err := h.searchSlices(ctx, gh, base)
	if err != nil {
		return nil, err
	}
	var all []prNode
	for _, s := range slices {
		var cursor *string
		got := 0
		for {
			var data struct {
				Search connection[prNode] `json:"search"`
			}
			if err := gh.graphql(ctx, searchPRsQuery, map[string]any{"q": s.query, "cursor": cursor}, &data); err != nil {
				return nil, err
			}
			for _, n := range data.Search.Nodes {
				if n.Number != 0 { // non-PR results decode as empty objects
					all = append(all, n)
					got++
				}
			}
			if !data.Search.PageInfo.HasNextPage {
				break
			}
			cursor = data.Search.PageInfo.EndCursor
		}
		_, span, _ := strings.Cut(s.query, " created:")
		logf("prs: %s -> %d/%d", span, got, s.count)
	}
	return all, nil
}

// allPages returns every node of a PR connection, refetching it page by page when it holds more
// than the 100 nodes fetched inline.
func allPages[T any](ctx context.Context, gh *githubClient, org string, pr prNode, conn, fields string, first connection[T]) ([]T, error) {
	if first.TotalCount <= len(first.Nodes) {
		return first.Nodes, nil
	}
	query := fmt.Sprintf(prPageQuery, conn, fields)
	var nodes []T
	var cursor *string
	for {
		var data struct {
			Repository struct {
				PullRequest map[string]connection[T] `json:"pullRequest"`
			} `json:"repository"`
		}
		vars := map[string]any{"owner": org, "name": pr.Repository.Name, "number": pr.Number, "cursor": cursor}
		if err := gh.graphql(ctx, query, vars, &data); err != nil {
			return nil, err
		}
		page := data.Repository.PullRequest[conn]
		nodes = append(nodes, page.Nodes...)
		if !page.PageInfo.HasNextPage {
			return nodes, nil
		}
		cursor = page.PageInfo.EndCursor
	}
}

func (h *harness) flattenPR(ctx context.Context, gh *githubClient, n prNode) (PullRequest, error) {
	commits, err := allPages(ctx, gh, h.cfg.Org, n, "commits", prCommitFields, n.Commits)
	if err != nil {
		return PullRequest{}, err
	}
	files, err := allPages(ctx, gh, h.cfg.Org, n, "files", prFileFields, n.Files)
	if err != nil {
		return PullRequest{}, err
	}
	pr := PullRequest{
		Repo: n.Repository.Name, Number: n.Number, URL: n.URL, Title: n.Title, Body: n.Body, State: n.State,
		IsDraft: n.IsDraft, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, ClosedAt: n.ClosedAt, MergedAt: n.MergedAt,
		Author: login(n.Author), MergedBy: login(n.MergedBy), BaseRef: n.BaseRefName, HeadRef: n.HeadRefName,
		HeadSHA: n.HeadRefOid, Additions: n.Additions, Deletions: n.Deletions, ChangedFiles: n.ChangedFiles,
		ReviewDecision: n.ReviewDecision, Labels: []string{}, Assignees: []string{},
		CommentCount: n.Comments.TotalCount, ReviewCount: n.Reviews.TotalCount, Reviews: []PRReview{},
		RequestedReviewers: []*string{}, ClosingIssues: []ClosingIssue{},
		JiraKeys:    h.jira.find(n.Title, n.Body, n.HeadRefName),
		CommitCount: n.Commits.TotalCount, Commits: []PRCommit{}, FileCount: n.Files.TotalCount, Files: []PRFile{},
	}
	if n.MergeCommit != nil {
		pr.MergeCommitSHA = &n.MergeCommit.Oid
	}
	for _, l := range n.Labels.Nodes {
		pr.Labels = append(pr.Labels, l.Name)
	}
	for _, a := range n.Assignees.Nodes {
		pr.Assignees = append(pr.Assignees, a.Login)
	}
	for _, r := range n.Reviews.Nodes {
		pr.Reviews = append(pr.Reviews, PRReview{Author: login(r.Author), State: r.State, SubmittedAt: r.SubmittedAt})
	}
	for _, rr := range n.ReviewRequests.Nodes {
		var who *string
		if r := rr.RequestedReviewer; r != nil && r.Login != "" {
			who = &r.Login
		} else if r != nil && r.Name != "" {
			who = &r.Name
		}
		pr.RequestedReviewers = append(pr.RequestedReviewers, who)
	}
	for _, ci := range n.ClosingIssuesReferences.Nodes {
		pr.ClosingIssues = append(pr.ClosingIssues, ClosingIssue{Repo: ci.Repository.NameWithOwner, Number: ci.Number, Title: ci.Title, URL: ci.URL})
	}
	for _, c := range commits {
		pc := PRCommit{SHA: c.Commit.Oid, Headline: c.Commit.MessageHeadline, AuthoredAt: c.Commit.AuthoredDate}
		if a := c.Commit.Author; a != nil {
			pc.AuthorName, pc.AuthorEmail, pc.AuthorLogin = a.Name, a.Email, login(a.User)
		}
		pr.Commits = append(pr.Commits, pc)
	}
	for _, f := range files {
		pr.Files = append(pr.Files, PRFile{Path: f.Path, Additions: f.Additions, Deletions: f.Deletions, ChangeType: f.ChangeType})
	}
	return pr, nil
}
