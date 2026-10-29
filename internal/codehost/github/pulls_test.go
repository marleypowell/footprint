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

// A search result node becomes the neutral record. Nested logins and names are flattened; what
// GitHub leaves null (a deleted "ghost" author, an unmerged PR, a commit email not linked to an
// account) stays nil; a requested reviewer is a user's login or a team's name, or nil when GitHub
// no longer says who; and every list is non-null, even when GitHub sends none.
func TestFlatten(t *testing.T) {
	tests := []struct {
		name, node string
		want       codehost.PullRequest
	}{
		{"a merged pull request", `{
  "number": 42, "title": "Add retries", "body": "Closes #5", "url": "https://github.com/acme/pay/pull/42",
  "state": "MERGED", "isDraft": false, "createdAt": "2024-04-01T00:00:00Z", "updatedAt": "2024-04-05T00:00:00Z",
  "closedAt": "2024-04-04T00:00:00Z", "mergedAt": "2024-04-04T00:00:00Z",
  "repository": {"name": "pay"}, "author": {"login": "jdoe"}, "mergedBy": {"login": "sam"},
  "baseRefName": "main", "headRefName": "jdoe/retries", "headRefOid": "bbb", "mergeCommit": {"oid": "ccc"},
  "additions": 120, "deletions": 30, "changedFiles": 2, "reviewDecision": "APPROVED",
  "labels": {"nodes": [{"name": "enhancement"}]},
  "assignees": {"nodes": [{"login": "jdoe"}]},
  "comments": {"totalCount": 4},
  "reviews": {"totalCount": 2, "nodes": [
    {"author": {"login": "sam"}, "state": "APPROVED", "submittedAt": "2024-04-03T00:00:00Z"},
    {"author": null, "state": "COMMENTED", "submittedAt": "2024-04-02T00:00:00Z"}]},
  "reviewRequests": {"nodes": [
    {"requestedReviewer": {"login": "lee"}}, {"requestedReviewer": {"name": "platform"}},
    {"requestedReviewer": {}}, {"requestedReviewer": null}]},
  "closingIssuesReferences": {"nodes": [
    {"number": 5, "title": "Retries", "url": "https://github.com/acme/pay/issues/5", "repository": {"nameWithOwner": "acme/pay"}}]},
  "commits": {"totalCount": 2, "nodes": [
    {"commit": {"oid": "aaa", "messageHeadline": "Add retries", "authoredDate": "2024-04-01T00:00:00Z",
                "author": {"name": "Jane Doe", "email": "jdoe@acme.test", "user": {"login": "jdoe"}}}},
    {"commit": {"oid": "bbb", "messageHeadline": "Fix the test", "authoredDate": "2024-04-02T00:00:00Z",
                "author": {"name": "jdoe", "email": "jdoe@laptop.local", "user": null}}}]},
  "files": {"totalCount": 2, "nodes": [
    {"path": "queue.go", "additions": 100, "deletions": 30, "changeType": "MODIFIED"},
    {"path": "queue_test.go", "additions": 20, "deletions": 0, "changeType": "ADDED"}]}
}`, codehost.PullRequest{
			Repo: "pay", Number: 42, URL: "https://github.com/acme/pay/pull/42", Title: "Add retries", Body: "Closes #5",
			State: "MERGED", CreatedAt: "2024-04-01T00:00:00Z", UpdatedAt: "2024-04-05T00:00:00Z",
			ClosedAt: ptr("2024-04-04T00:00:00Z"), MergedAt: ptr("2024-04-04T00:00:00Z"),
			Author: ptr("jdoe"), MergedBy: ptr("sam"), BaseRef: "main", HeadRef: "jdoe/retries", HeadSHA: "bbb",
			MergeCommitSHA: ptr("ccc"), Additions: 120, Deletions: 30, ChangedFiles: 2, ReviewDecision: ptr("APPROVED"),
			Labels: []string{"enhancement"}, Assignees: []string{"jdoe"}, CommentCount: 4, ReviewCount: 2,
			Reviews: []codehost.Review{
				{Author: ptr("sam"), State: "APPROVED", SubmittedAt: ptr("2024-04-03T00:00:00Z")},
				{Author: nil, State: "COMMENTED", SubmittedAt: ptr("2024-04-02T00:00:00Z")},
			},
			RequestedReviewers: []*string{ptr("lee"), ptr("platform"), nil, nil},
			ClosingIssues: []codehost.ClosingIssue{
				{Repo: "acme/pay", Number: 5, Title: "Retries", URL: "https://github.com/acme/pay/issues/5"}},
			CommitCount: 2,
			Commits: []codehost.PRCommit{
				{SHA: "aaa", Headline: "Add retries", AuthoredAt: "2024-04-01T00:00:00Z",
					AuthorName: ptr("Jane Doe"), AuthorEmail: ptr("jdoe@acme.test"), AuthorLogin: ptr("jdoe")},
				{SHA: "bbb", Headline: "Fix the test", AuthoredAt: "2024-04-02T00:00:00Z",
					AuthorName: ptr("jdoe"), AuthorEmail: ptr("jdoe@laptop.local")},
			},
			FileCount: 2,
			Files: []codehost.PRFile{
				{Path: "queue.go", Additions: 100, Deletions: 30, ChangeType: "MODIFIED"},
				{Path: "queue_test.go", Additions: 20, ChangeType: "ADDED"},
			},
		}},
		{"an open draft by a deleted account", `{
  "number": 3, "title": "WIP", "url": "https://github.com/acme/web/pull/3", "state": "OPEN", "isDraft": true,
  "createdAt": "2024-05-01T00:00:00Z", "updatedAt": "2024-05-01T00:00:00Z", "closedAt": null, "mergedAt": null,
  "repository": {"name": "web"}, "author": null, "mergedBy": null, "mergeCommit": null, "reviewDecision": null,
  "baseRefName": "main", "headRefName": "wip", "headRefOid": "ddd"
}`, codehost.PullRequest{
			Repo: "web", Number: 3, URL: "https://github.com/acme/web/pull/3", Title: "WIP", State: "OPEN", IsDraft: true,
			CreatedAt: "2024-05-01T00:00:00Z", UpdatedAt: "2024-05-01T00:00:00Z", BaseRef: "main", HeadRef: "wip", HeadSHA: "ddd",
			Labels: []string{}, Assignees: []string{}, Reviews: []codehost.Review{}, RequestedReviewers: []*string{},
			ClosingIssues: []codehost.ClosingIssue{}, Commits: []codehost.PRCommit{}, Files: []codehost.PRFile{},
		}},
	}
	for _, tt := range tests {
		var n prNode
		if err := json.Unmarshal([]byte(tt.node), &n); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		// Everything fits inline, so flatten makes no request (and has no client to make one with).
		got, err := testHost(t).flatten(context.Background(), nil, n)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: flatten() =\n %+v\nwant\n %+v", tt.name, got, tt.want)
		}
	}
}

// The search query fetches the first 100 commits and files of each pull request. When there are
// more, flatten refetches that connection from the start, page by page, and keeps the refetched
// nodes in place of the inline ones; a connection that fit inline is not fetched again.
func TestFlattenRefetchesLongConnections(t *testing.T) {
	commit := func(sha string) string {
		return fmt.Sprintf(`{"commit": {"oid": %q, "messageHeadline": "h", "authoredDate": "2024-04-01T00:00:00Z", "author": null}}`, sha)
	}
	var n prNode
	if err := json.Unmarshal([]byte(`{"number": 7, "repository": {"name": "pay"},
  "commits": {"totalCount": 3, "nodes": [`+commit("c1")+`]},
  "files": {"totalCount": 1, "nodes": [{"path": "a.go", "changeType": "ADDED"}]}}`), &n); err != nil {
		t.Fatal(err)
	}
	pages := map[any]string{
		nil:  `{"totalCount": 3, "pageInfo": {"hasNextPage": true, "endCursor": "p1"}, "nodes": [` + commit("c1") + `, ` + commit("c2") + `]}`,
		"p1": `{"totalCount": 3, "pageInfo": {"hasNextPage": false, "endCursor": "p2"}, "nodes": [` + commit("c3") + `]}`,
	}
	wantQuery := fmt.Sprintf(prPageQuery, "commits", prCommitFields)
	h, requests := fakeGraphQL(t, func(req gqlRequest) string {
		if req.Query != wantQuery {
			t.Errorf("refetched with query %q, want the commits page query", req.Query)
		}
		return `{"data": {"repository": {"pullRequest": {"commits": ` + pages[req.Variables["cursor"]] + `}}}}`
	})

	pr, err := testHost(t).flatten(context.Background(), fakeClient(h), n)
	if err != nil {
		t.Fatal(err)
	}
	var shas []string
	for _, c := range pr.Commits {
		shas = append(shas, c.SHA)
	}
	if want := []string{"c1", "c2", "c3"}; !reflect.DeepEqual(shas, want) || pr.CommitCount != 3 {
		t.Errorf("flatten() commits = %v (count %d), want %v (count 3)", shas, pr.CommitCount, want)
	}
	if len(pr.Files) != 1 || pr.Files[0].Path != "a.go" {
		t.Errorf("flatten() files = %+v, want the inline a.go", pr.Files)
	}
	var vars []map[string]any
	for _, r := range *requests {
		vars = append(vars, r.Variables)
	}
	want := []map[string]any{ // JSON numbers decode as float64
		{"owner": "acme", "name": "pay", "number": float64(7), "cursor": nil},
		{"owner": "acme", "name": "pay", "number": float64(7), "cursor": "p1"},
	}
	if !reflect.DeepEqual(vars, want) {
		t.Errorf("flatten() refetched with variables %v, want %v", vars, want)
	}
}

// GitHub search returns at most 1000 results, so searchSlices counts the matches in the whole
// created: range (from 2008, when GitHub launched, to tomorrow), halves any range over the cap,
// depth first, and drops ranges with no matches. A single day over the cap cannot be halved and is
// kept as it is.
func TestSearchSlices(t *testing.T) {
	const base = "is:pr author:jdoe org:acme"
	type slice struct {
		span  string
		count int
	}
	tests := []struct {
		name   string
		counts map[string]int // matches per created: range; the clock reads 2008-01-03
		want   []slice
	}{
		{"a range under the cap is searched whole",
			map[string]int{"2008-01-01..2008-01-04": 40},
			[]slice{{"2008-01-01..2008-01-04", 40}}},
		{"exactly the cap still fits",
			map[string]int{"2008-01-01..2008-01-04": 1000},
			[]slice{{"2008-01-01..2008-01-04", 1000}}},
		{"no matches leave nothing to search",
			map[string]int{"2008-01-01..2008-01-04": 0},
			nil},
		{"a range over the cap is halved until each part fits, and empty parts dropped",
			map[string]int{
				"2008-01-01..2008-01-04": 1500,
				"2008-01-01..2008-01-02": 1200, "2008-01-03..2008-01-04": 0,
				"2008-01-01..2008-01-01": 700, "2008-01-02..2008-01-02": 500,
			},
			[]slice{{"2008-01-01..2008-01-01", 700}, {"2008-01-02..2008-01-02", 500}}},
		{"a single day over the cap is kept whole",
			map[string]int{
				"2008-01-01..2008-01-04": 1800,
				"2008-01-01..2008-01-02": 1500, "2008-01-03..2008-01-04": 300,
				"2008-01-01..2008-01-01": 1400, "2008-01-02..2008-01-02": 100,
			},
			[]slice{{"2008-01-01..2008-01-01", 1400}, {"2008-01-02..2008-01-02", 100}, {"2008-01-03..2008-01-04", 300}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inBubble(t, "2008-01-03T12:00:00Z", func(t *testing.T) {
				h, _ := fakeGraphQL(t, func(req gqlRequest) string {
					q, _ := req.Variables["q"].(string)
					rest, span, _ := strings.Cut(q, " created:")
					count, ok := tt.counts[span]
					if req.Query != searchCountQuery || rest != base || !ok {
						t.Errorf("unexpected count request %q", q)
					}
					return fmt.Sprintf(`{"data": {"search": {"issueCount": %d}}}`, count)
				})
				got, err := searchSlices(context.Background(), fakeClient(h), base)
				if err != nil {
					t.Fatal(err)
				}
				var want []searchSlice
				for _, s := range tt.want {
					want = append(want, searchSlice{base + " created:" + s.span, s.count})
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("searchSlices() = %v, want %v", got, want)
				}
			})
		})
	}
}

// PullRequests searches the org for the author's pull requests: it counts them, then pages
// through each slice 25 at a time by cursor. Search can also return issues, which decode as empty
// nodes and are skipped.
func TestPullRequests(t *testing.T) {
	inBubble(t, "2024-03-10T12:00:00Z", func(t *testing.T) {
		const q = "is:pr author:jdoe org:acme created:2008-01-01..2024-03-11"
		pages := map[any]string{
			nil: `{"issueCount": 2, "pageInfo": {"hasNextPage": true, "endCursor": "s1"},
  "nodes": [{"number": 1, "title": "One", "repository": {"name": "pay"}}, {}]}`,
			"s1": `{"issueCount": 2, "pageInfo": {"hasNextPage": false, "endCursor": "s2"},
  "nodes": [{"number": 2, "title": "Two", "repository": {"name": "web"}}]}`,
		}
		h, requests := fakeGraphQL(t, func(req gqlRequest) string {
			switch req.Query {
			case searchCountQuery:
				return `{"data": {"search": {"issueCount": 2}}}`
			case searchPRsQuery:
				return `{"data": {"search": ` + pages[req.Variables["cursor"]] + `}}`
			}
			t.Errorf("unexpected query %q", req.Query)
			return `{}`
		})
		prs, err := fakeHost(t, h).PullRequests(context.Background(), "jdoe")
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, pr := range prs {
			got = append(got, fmt.Sprintf("%s#%d %s", pr.Repo, pr.Number, pr.Title))
		}
		if want := []string{"pay#1 One", "web#2 Two"}; !reflect.DeepEqual(got, want) {
			t.Errorf("PullRequests() = %v, want %v", got, want)
		}
		var vars []map[string]any
		for _, r := range *requests {
			vars = append(vars, r.Variables)
		}
		want := []map[string]any{{"q": q}, {"q": q, "cursor": nil}, {"q": q, "cursor": "s1"}}
		if !reflect.DeepEqual(vars, want) {
			t.Errorf("PullRequests() sent variables %v, want %v", vars, want)
		}
	})
}
