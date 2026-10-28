package github

import (
	"context"
	"fmt"
	"strings"

	"github.com/marleypowell/footprint/internal/codehost"
	"github.com/marleypowell/footprint/internal/logx"
)

// GitHub's commenter: search matches review bodies and inline comments as well as conversation
// comments: on a real org, every pull request reviewed-by: an account found was also commenter:
// it. One search finds them all.
const (
	reviewCommentFields = `id url path body createdAt replyTo { id }`
	commentFields       = `id url body createdAt author { login }`
	reviewFields        = `id url state submittedAt body
    comments(first: 30) { totalCount pageInfo { hasNextPage endCursor } nodes { ` + reviewCommentFields + ` } }`
	reviewedFields = `
  number title body url state isDraft createdAt closedAt mergedAt
  repository { name }
  author { login __typename }
  mergedBy { login }
  baseRefName headRefName additions deletions changedFiles
  closingIssuesReferences(first: 20) { nodes { number title url repository { nameWithOwner } } }
  reviews(first: 20, author: $login) { totalCount pageInfo { hasNextPage endCursor } nodes { ` + reviewFields + ` } }
  comments(first: 100) { totalCount pageInfo { hasNextPage endCursor } nodes { ` + commentFields + ` } }
`
	searchReviewedQuery = `
query($q: String!, $cursor: String, $login: String!) {
  search(query: $q, type: ISSUE, first: 20, after: $cursor) {
    issueCount
    pageInfo { hasNextPage endCursor }
    nodes { ... on PullRequest { ` + reviewedFields + ` } }
  }
}`
	reviewsPageQuery = `
query($owner: String!, $name: String!, $number: Int!, $cursor: String, $login: String!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviews(first: 50, after: $cursor, author: $login) { totalCount pageInfo { hasNextPage endCursor } nodes { ` + reviewFields + ` } }
    }
  }
}`
	reviewCommentsPageQuery = `
query($id: ID!, $cursor: String) {
  node(id: $id) {
    ... on PullRequestReview {
      comments(first: 100, after: $cursor) { totalCount pageInfo { hasNextPage endCursor } nodes { ` + reviewCommentFields + ` } }
    }
  }
}`
)

type reviewCommentNode struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Path      string `json:"path"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
	ReplyTo   *struct {
		ID string `json:"id"`
	} `json:"replyTo"`
}

type commentNode struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
	Author    *actor `json:"author"`
}

type reviewNode struct {
	ID          string                        `json:"id"`
	URL         string                        `json:"url"`
	State       string                        `json:"state"`
	SubmittedAt *string                       `json:"submittedAt"`
	Body        string                        `json:"body"`
	Comments    connection[reviewCommentNode] `json:"comments"`
}

type reviewedNode struct {
	Number     int      `json:"number"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	URL        string   `json:"url"`
	State      string   `json:"state"`
	IsDraft    bool     `json:"isDraft"`
	CreatedAt  string   `json:"createdAt"`
	ClosedAt   *string  `json:"closedAt"`
	MergedAt   *string  `json:"mergedAt"`
	Repository nameNode `json:"repository"`
	Author     *struct {
		Login    string `json:"login"`
		Typename string `json:"__typename"`
	} `json:"author"`
	MergedBy                *actor `json:"mergedBy"`
	BaseRefName             string `json:"baseRefName"`
	HeadRefName             string `json:"headRefName"`
	Additions               int    `json:"additions"`
	Deletions               int    `json:"deletions"`
	ChangedFiles            int    `json:"changedFiles"`
	ClosingIssuesReferences connection[struct {
		Number     int       `json:"number"`
		Title      string    `json:"title"`
		URL        string    `json:"url"`
		Repository ownerNode `json:"repository"`
	}] `json:"closingIssuesReferences"`
	Reviews  connection[reviewNode]  `json:"reviews"`
	Comments connection[commentNode] `json:"comments"`
}

// reviewStates maps GitHub's submitted review states to the neutral ones; PENDING (never
// submitted) maps to none.
var reviewStates = map[string]string{
	"APPROVED": codehost.ReviewApproved, "CHANGES_REQUESTED": codehost.ReviewChangesRequested,
	"COMMENTED": codehost.ReviewCommented, "DISMISSED": codehost.ReviewDismissed,
}

func (h *Host) Reviewed(ctx context.Context, reviewer string) ([]codehost.ReviewedPullRequest, error) {
	api, err := h.client(ctx)
	if err != nil {
		return nil, err
	}
	slices, err := searchSlices(ctx, api, fmt.Sprintf("is:pr commenter:%s org:%s -author:%s", reviewer, h.org, reviewer))
	if err != nil {
		return nil, err
	}
	var out []codehost.ReviewedPullRequest
	for _, s := range slices {
		var cursor *string
		got := 0
		for {
			var data struct {
				Search connection[reviewedNode] `json:"search"`
			}
			vars := map[string]any{"q": s.query, "cursor": cursor, "login": reviewer}
			if err := api.graphql(ctx, searchReviewedQuery, vars, &data); err != nil {
				return nil, err
			}
			for _, n := range data.Search.Nodes {
				if n.Number == 0 { // non-PR results decode as empty objects
					continue
				}
				pr, err := h.flattenReviewed(ctx, api, n, reviewer)
				if err != nil {
					return nil, err
				}
				out = append(out, pr)
				got++
			}
			if !data.Search.PageInfo.HasNextPage {
				break
			}
			cursor = data.Search.PageInfo.EndCursor
		}
		_, span, _ := strings.Cut(s.query, " created:")
		logx.Printf("github: reviewed pull requests created %s -> %d/%d", span, got, s.count)
	}
	return out, nil
}

func (h *Host) flattenReviewed(ctx context.Context, api *client, n reviewedNode, reviewer string) (codehost.ReviewedPullRequest, error) {
	pr := codehost.ReviewedPullRequest{
		Repo: n.Repository.Name, Number: n.Number, URL: n.URL, Title: n.Title, Body: n.Body, State: n.State,
		IsDraft: n.IsDraft, CreatedAt: n.CreatedAt, ClosedAt: n.ClosedAt, MergedAt: n.MergedAt,
		MergedBy: login(n.MergedBy), BaseRef: n.BaseRefName, HeadRef: n.HeadRefName, Additions: n.Additions,
		Deletions: n.Deletions, ChangedFiles: n.ChangedFiles, ClosingIssues: []codehost.ClosingIssue{},
		Reviews: []codehost.ReviewerReview{}, Comments: []codehost.Comment{},
	}
	if a := n.Author; a != nil {
		pr.Author, pr.AuthorIsBot = &a.Login, a.Typename == "Bot"
	}
	for _, ci := range n.ClosingIssuesReferences.Nodes {
		pr.ClosingIssues = append(pr.ClosingIssues, codehost.ClosingIssue{
			Repo: ci.Repository.NameWithOwner, Number: ci.Number, Title: ci.Title, URL: ci.URL})
	}
	reviews, err := h.reviewPages(ctx, api, n, reviewer)
	if err != nil {
		return pr, err
	}
	for _, r := range reviews {
		state, ok := reviewStates[r.State]
		if !ok || r.SubmittedAt == nil {
			continue
		}
		comments, err := reviewCommentPages(ctx, api, r)
		if err != nil {
			return pr, err
		}
		rr := codehost.ReviewerReview{ID: r.ID, URL: r.URL, State: state, SubmittedAt: *r.SubmittedAt, Body: r.Body,
			Comments: []codehost.ReviewComment{}}
		for _, c := range comments {
			rc := codehost.ReviewComment{ID: c.ID, URL: c.URL, Path: c.Path, Body: c.Body, CreatedAt: c.CreatedAt}
			if c.ReplyTo != nil {
				rc.InReplyTo = &c.ReplyTo.ID
			}
			rr.Comments = append(rr.Comments, rc)
		}
		pr.Reviews = append(pr.Reviews, rr)
	}
	comments, err := allPages(ctx, api, h.org, prNode{Number: n.Number, Repository: n.Repository}, "comments", commentFields, n.Comments)
	if err != nil {
		return pr, err
	}
	for _, c := range comments { // the connection has no author filter
		if c.Author != nil && strings.EqualFold(c.Author.Login, reviewer) {
			pr.Comments = append(pr.Comments, codehost.Comment{ID: c.ID, URL: c.URL, Body: c.Body, CreatedAt: c.CreatedAt})
		}
	}
	return pr, nil
}

// reviewPages returns all of the reviewer's reviews of a pull request, refetching them when there
// are more than the search fetched inline.
func (h *Host) reviewPages(ctx context.Context, api *client, n reviewedNode, reviewer string) ([]reviewNode, error) {
	if n.Reviews.TotalCount <= len(n.Reviews.Nodes) {
		return n.Reviews.Nodes, nil
	}
	var nodes []reviewNode
	var cursor *string
	for {
		var data struct {
			Repository struct {
				PullRequest struct {
					Reviews connection[reviewNode] `json:"reviews"`
				} `json:"pullRequest"`
			} `json:"repository"`
		}
		vars := map[string]any{"owner": h.org, "name": n.Repository.Name, "number": n.Number, "cursor": cursor, "login": reviewer}
		if err := api.graphql(ctx, reviewsPageQuery, vars, &data); err != nil {
			return nil, err
		}
		page := data.Repository.PullRequest.Reviews
		nodes = append(nodes, page.Nodes...)
		if !page.PageInfo.HasNextPage {
			return nodes, nil
		}
		cursor = page.PageInfo.EndCursor
	}
}

// reviewCommentPages returns all of a review's inline comments.
func reviewCommentPages(ctx context.Context, api *client, r reviewNode) ([]reviewCommentNode, error) {
	if r.Comments.TotalCount <= len(r.Comments.Nodes) {
		return r.Comments.Nodes, nil
	}
	var nodes []reviewCommentNode
	var cursor *string
	for {
		var data struct {
			Node struct {
				Comments connection[reviewCommentNode] `json:"comments"`
			} `json:"node"`
		}
		if err := api.graphql(ctx, reviewCommentsPageQuery, map[string]any{"id": r.ID, "cursor": cursor}, &data); err != nil {
			return nil, err
		}
		nodes = append(nodes, data.Node.Comments.Nodes...)
		if !data.Node.Comments.PageInfo.HasNextPage {
			return nodes, nil
		}
		cursor = data.Node.Comments.PageInfo.EndCursor
	}
}
