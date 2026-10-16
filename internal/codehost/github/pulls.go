package github

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/marleypowell/footprint/internal/codehost"
	"github.com/marleypowell/footprint/internal/logx"
)

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
	// searchFloor is when GitHub launched: no pull request is older.
	searchFloorYear = 2008
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

func (h *Host) PullRequests(ctx context.Context, author string) ([]codehost.PullRequest, error) {
	api, err := h.client(ctx)
	if err != nil {
		return nil, err
	}
	nodes, err := h.searchPRs(ctx, api, fmt.Sprintf("is:pr author:%s org:%s", author, h.org))
	if err != nil {
		return nil, err
	}
	prs := make([]codehost.PullRequest, 0, len(nodes))
	for _, n := range nodes {
		pr, err := h.flatten(ctx, api, n)
		if err != nil {
			return nil, err
		}
		prs = append(prs, pr)
	}
	return prs, nil
}

type searchSlice struct {
	query string
	count int
}

// searchSlices splits created: date ranges until each holds at most 1000 results, the GitHub
// search cap.
func searchSlices(ctx context.Context, api *client, base string) ([]searchSlice, error) {
	type span struct{ lo, hi time.Time }
	pending := []span{{time.Date(searchFloorYear, 1, 1, 0, 0, 0, 0, time.UTC), tomorrow()}}
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
		if err := api.graphql(ctx, searchCountQuery, map[string]any{"q": q}, &data); err != nil {
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

func (h *Host) searchPRs(ctx context.Context, api *client, base string) ([]prNode, error) {
	slices, err := searchSlices(ctx, api, base)
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
			if err := api.graphql(ctx, searchPRsQuery, map[string]any{"q": s.query, "cursor": cursor}, &data); err != nil {
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
		logx.Printf("github: pull requests created %s -> %d/%d", span, got, s.count)
	}
	return all, nil
}

// allPages returns every node of a PR connection, refetching it page by page when it holds more
// than the 100 nodes fetched inline.
func allPages[T any](ctx context.Context, api *client, org string, pr prNode, conn, fields string, first connection[T]) ([]T, error) {
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
		if err := api.graphql(ctx, query, vars, &data); err != nil {
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

func (h *Host) flatten(ctx context.Context, api *client, n prNode) (codehost.PullRequest, error) {
	commits, err := allPages(ctx, api, h.org, n, "commits", prCommitFields, n.Commits)
	if err != nil {
		return codehost.PullRequest{}, err
	}
	files, err := allPages(ctx, api, h.org, n, "files", prFileFields, n.Files)
	if err != nil {
		return codehost.PullRequest{}, err
	}
	pr := codehost.PullRequest{
		Repo: n.Repository.Name, Number: n.Number, URL: n.URL, Title: n.Title, Body: n.Body, State: n.State,
		IsDraft: n.IsDraft, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, ClosedAt: n.ClosedAt, MergedAt: n.MergedAt,
		Author: login(n.Author), MergedBy: login(n.MergedBy), BaseRef: n.BaseRefName, HeadRef: n.HeadRefName,
		HeadSHA: n.HeadRefOid, Additions: n.Additions, Deletions: n.Deletions, ChangedFiles: n.ChangedFiles,
		ReviewDecision: n.ReviewDecision, Labels: []string{}, Assignees: []string{},
		CommentCount: n.Comments.TotalCount, ReviewCount: n.Reviews.TotalCount, Reviews: []codehost.Review{},
		RequestedReviewers: []*string{}, ClosingIssues: []codehost.ClosingIssue{},
		CommitCount: n.Commits.TotalCount, Commits: []codehost.PRCommit{}, FileCount: n.Files.TotalCount,
		Files: []codehost.PRFile{},
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
		pr.Reviews = append(pr.Reviews, codehost.Review{Author: login(r.Author), State: r.State, SubmittedAt: r.SubmittedAt})
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
		pr.ClosingIssues = append(pr.ClosingIssues, codehost.ClosingIssue{
			Repo: ci.Repository.NameWithOwner, Number: ci.Number, Title: ci.Title, URL: ci.URL})
	}
	for _, c := range commits {
		pc := codehost.PRCommit{SHA: c.Commit.Oid, Headline: c.Commit.MessageHeadline, AuthoredAt: c.Commit.AuthoredDate}
		if a := c.Commit.Author; a != nil {
			pc.AuthorName, pc.AuthorEmail, pc.AuthorLogin = a.Name, a.Email, login(a.User)
		}
		pr.Commits = append(pr.Commits, pc)
	}
	for _, f := range files {
		pr.Files = append(pr.Files, codehost.PRFile{Path: f.Path, Additions: f.Additions, Deletions: f.Deletions, ChangeType: f.ChangeType})
	}
	return pr, nil
}
