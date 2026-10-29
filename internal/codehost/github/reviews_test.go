//go:build go1.25

package github

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/codehost"
)

// A search result decodes to the neutral record: GitHub's review states mapped, pending reviews
// left out, a bot author marked, and only the reviewer's conversation comments kept. Everything
// fits inline, so nothing is refetched.
func TestFlattenReviewed(t *testing.T) {
	var n reviewedNode
	err := json.Unmarshal([]byte(`{
  "number": 21, "title": "Queue retries", "url": "https://github.com/acme/pay/pull/21", "state": "MERGED",
  "createdAt": "2024-04-01T00:00:00Z", "mergedAt": "2024-04-04T00:00:00Z",
  "repository": {"name": "pay"}, "author": {"login": "dependabot", "__typename": "Bot"}, "mergedBy": {"login": "sam"},
  "headRefName": "retries",
  "closingIssuesReferences": {"nodes": [{"number": 5, "title": "Retries", "url": "u", "repository": {"nameWithOwner": "acme/pay"}}]},
  "reviews": {"totalCount": 3, "nodes": [
    {"id": "r1", "state": "CHANGES_REQUESTED", "submittedAt": "2024-04-02T00:00:00Z", "body": "Needs a backoff",
     "comments": {"totalCount": 1, "nodes": [{"id": "rc1", "path": "queue.go", "body": "cap this", "createdAt": "2024-04-02T00:00:00Z", "replyTo": {"id": "rc0"}}]}},
    {"id": "r2", "state": "APPROVED", "submittedAt": "2024-04-03T00:00:00Z", "comments": {"totalCount": 0, "nodes": []}},
    {"id": "r3", "state": "PENDING", "submittedAt": null, "comments": {"totalCount": 0, "nodes": []}}
  ]},
  "comments": {"totalCount": 2, "nodes": [
    {"id": "c1", "body": "thanks", "createdAt": "2024-04-03T00:00:00Z", "author": {"login": "JDoe"}},
    {"id": "c2", "body": "theirs", "createdAt": "2024-04-03T00:00:00Z", "author": {"login": "sam"}}
  ]}
}`), &n)
	if err != nil {
		t.Fatal(err)
	}
	pr, err := testHost(t).flattenReviewed(context.Background(), nil, n, "jdoe")
	if err != nil {
		t.Fatal(err)
	}
	want := codehost.ReviewedPullRequest{
		Repo: "pay", Number: 21, URL: "https://github.com/acme/pay/pull/21", Title: "Queue retries", State: "MERGED",
		CreatedAt: "2024-04-01T00:00:00Z", MergedAt: ptr("2024-04-04T00:00:00Z"), Author: ptr("dependabot"), AuthorIsBot: true,
		MergedBy: ptr("sam"), HeadRef: "retries",
		ClosingIssues: []codehost.ClosingIssue{{Repo: "acme/pay", Number: 5, Title: "Retries", URL: "u"}},
		Reviews: []codehost.ReviewerReview{
			{ID: "r1", State: codehost.ReviewChangesRequested, SubmittedAt: "2024-04-02T00:00:00Z", Body: "Needs a backoff",
				Comments: []codehost.ReviewComment{{ID: "rc1", Path: "queue.go", Body: "cap this", CreatedAt: "2024-04-02T00:00:00Z", InReplyTo: ptr("rc0")}}},
			{ID: "r2", State: codehost.ReviewApproved, SubmittedAt: "2024-04-03T00:00:00Z", Comments: []codehost.ReviewComment{}},
		},
		Comments: []codehost.Comment{{ID: "c1", Body: "thanks", CreatedAt: "2024-04-03T00:00:00Z"}},
	}
	if !reflect.DeepEqual(pr, want) {
		t.Errorf("got  %+v\nwant %+v", pr, want)
	}
}

func ptr(s string) *string { return &s }

// A review with more comments than the search fetched inline, and a PR with more reviews and
// conversation comments than it fetched, are refetched page by page, each by its own query.
func TestFlattenReviewedRefetchesLongConnections(t *testing.T) {
	var n reviewedNode
	if err := json.Unmarshal([]byte(`{"number": 7, "repository": {"name": "pay"}, "author": {"login": "sam", "__typename": "User"},
  "reviews": {"totalCount": 2, "nodes": [{"id": "r1", "state": "COMMENTED", "submittedAt": "2024-04-02T00:00:00Z",
    "comments": {"totalCount": 2, "nodes": [{"id": "rc1", "path": "a.go", "createdAt": "2024-04-02T00:00:00Z"}]}}]},
  "comments": {"totalCount": 2, "nodes": [{"id": "c1", "createdAt": "2024-04-03T00:00:00Z", "author": {"login": "jdoe"}}]}}`), &n); err != nil {
		t.Fatal(err)
	}
	commentsQuery := fmt.Sprintf(prPageQuery, "comments", commentFields)
	h, requests := fakeGraphQL(t, func(req gqlRequest) string {
		switch req.Query {
		case reviewsPageQuery:
			return `{"data": {"repository": {"pullRequest": {"reviews": {"totalCount": 2, "pageInfo": {"hasNextPage": false},
  "nodes": [{"id": "r1", "state": "COMMENTED", "submittedAt": "2024-04-02T00:00:00Z",
              "comments": {"totalCount": 2, "nodes": [{"id": "rc1", "path": "a.go"}]}},
            {"id": "r2", "state": "APPROVED", "submittedAt": "2024-04-04T00:00:00Z", "comments": {"totalCount": 0, "nodes": []}}]}}}}}`
		case reviewCommentsPageQuery:
			return `{"data": {"node": {"comments": {"totalCount": 2, "pageInfo": {"hasNextPage": false},
  "nodes": [{"id": "rc1", "path": "a.go"}, {"id": "rc2", "path": "b.go"}]}}}}`
		case commentsQuery:
			return `{"data": {"repository": {"pullRequest": {"comments": {"totalCount": 2, "pageInfo": {"hasNextPage": false},
  "nodes": [{"id": "c1", "author": {"login": "jdoe"}}, {"id": "c2", "author": {"login": "sam"}}]}}}}}`
		}
		t.Errorf("unexpected query %q", req.Query)
		return `{}`
	})
	pr, err := fakeHost(t, h).flattenReviewed(context.Background(), fakeClient(h), n, "jdoe")
	if err != nil {
		t.Fatal(err)
	}
	var reviews, comments []string
	for _, r := range pr.Reviews {
		reviews = append(reviews, r.ID+":"+r.State)
		for _, c := range r.Comments {
			reviews = append(reviews, "  "+c.ID)
		}
	}
	for _, c := range pr.Comments {
		comments = append(comments, c.ID)
	}
	if want := []string{"r1:commented", "  rc1", "  rc2", "r2:approved"}; !reflect.DeepEqual(reviews, want) {
		t.Errorf("reviews = %v, want %v: r1 with its comments refetched, then r2", reviews, want)
	}
	if want := []string{"c1"}; !reflect.DeepEqual(comments, want) {
		t.Errorf("comments = %v, want only the reviewer's %v", comments, want)
	}
	if len(*requests) != 3 {
		t.Errorf("%d requests, want one refetch of reviews, of the review's comments, and of the conversation", len(*requests))
	}
}

// Reviewed searches for the other people's PRs the account commented on (a review counts as one),
// reads every page of each result slice, passes the reviewer's login to the reviews filter, skips the
// non-PR results GitHub decodes as empty objects, and flattens each PR.
func TestReviewed(t *testing.T) {
	inBubble(t, "2024-03-10T12:00:00Z", func(t *testing.T) {
		const q = "is:pr commenter:jdoe org:acme -author:jdoe created:2008-01-01..2024-03-11"
		pages := map[any]string{
			nil: `{"issueCount": 2, "pageInfo": {"hasNextPage": true, "endCursor": "s1"},
  "nodes": [{"number": 1, "title": "One", "repository": {"name": "pay"}, "author": {"login": "sam", "__typename": "User"},
             "reviews": {"totalCount": 1, "nodes": [{"id": "r1", "state": "APPROVED", "submittedAt": "2024-04-02T00:00:00Z",
                         "comments": {"totalCount": 0, "nodes": []}}]}}, {}]}`,
			"s1": `{"issueCount": 2, "pageInfo": {"hasNextPage": false},
  "nodes": [{"number": 2, "title": "Two", "repository": {"name": "web"}, "author": {"login": "dependabot", "__typename": "Bot"}}]}`,
		}
		h, requests := fakeGraphQL(t, func(req gqlRequest) string {
			switch req.Query {
			case searchCountQuery:
				return `{"data": {"search": {"issueCount": 2}}}`
			case searchReviewedQuery:
				return `{"data": {"search": ` + pages[req.Variables["cursor"]] + `}}`
			}
			t.Errorf("unexpected query %q", req.Query)
			return `{}`
		})
		got, err := fakeHost(t, h).Reviewed(context.Background(), "jdoe")
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, pr := range got {
			names = append(names, fmt.Sprintf("%s#%d %s bot=%v reviews=%d", pr.Repo, pr.Number, pr.Title, pr.AuthorIsBot, len(pr.Reviews)))
		}
		if want := []string{"pay#1 One bot=false reviews=1", "web#2 Two bot=true reviews=0"}; !reflect.DeepEqual(names, want) {
			t.Errorf("Reviewed() = %v, want %v", names, want)
		}
		for _, r := range *requests {
			if r.Query == searchReviewedQuery && (r.Variables["q"] != q || r.Variables["login"] != "jdoe") {
				t.Errorf("searched with %v, want query %q and login jdoe", r.Variables, q)
			}
		}
		if !strings.Contains(searchReviewedQuery, "reviews(first: 20, author: $login)") {
			t.Error("the search must ask only for the reviewer's reviews")
		}
	})
}
