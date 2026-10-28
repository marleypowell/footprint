package github

import (
	"context"
	"encoding/json"
	"reflect"
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
