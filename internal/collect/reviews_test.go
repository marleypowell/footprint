package collect

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/marleypowell/footprint/internal/codehost"
)

// reviews keeps other people's PRs that the user reviewed or commented on, with the user's reviews
// and comments oldest first, and counts them in org/reviews.json.
func TestReviews(t *testing.T) {
	host := &fakeHost{reviewed: []codehost.ReviewedPullRequest{
		{Repo: "api", Number: 4, Title: "Queue retries for ENG-42", Author: ptr("other"),
			Reviews: []codehost.ReviewerReview{
				{ID: "r2", SubmittedAt: "2024-01-02T00:00:00Z"},
				{ID: "r1", SubmittedAt: "2024-01-01T00:00:00Z", Comments: []codehost.ReviewComment{{ID: "rc1"}, {ID: "rc2"}}},
				{ID: "r0", SubmittedAt: "2024-01-01T00:00:00Z"}, // the same time as r1: by id
			},
			Comments: []codehost.Comment{{ID: "c2", CreatedAt: "2024-01-03T00:00:00Z"}, {ID: "c1", CreatedAt: "2024-01-01T00:00:00Z"}}},
		{Repo: "api", Number: 5, Title: "the user's own", Author: ptr("JDoe"), Comments: []codehost.Comment{{ID: "c3"}}}, // in prs.jsonl instead
		{Repo: "api", Number: 6, Title: "requested, never reviewed", Author: ptr("other")},
		{Repo: "web", Number: 2, Title: "by a deleted account", Comments: []codehost.Comment{{ID: "c4"}}}, // no author: not the user's
	}}
	h := newTestHarness(t, Tools{Host: host, Tracker: &fakeTracker{}})
	writeRows(t, h, "repos/old/reviews.jsonl", ReviewRecord{ReviewedPullRequest: codehost.ReviewedPullRequest{Repo: "old", Number: 1}})
	if err := h.reviews(context.Background()); err != nil {
		t.Fatal(err)
	}
	api, err := readJSONL[ReviewRecord](filepath.Join(h.repoDir("api"), "reviews.jsonl"))
	if err != nil || len(api) != 1 || api[0].Number != 4 {
		t.Fatalf("api reviews = %+v, %v, want only PR 4", api, err)
	}
	var reviews, comments []string
	for _, r := range api[0].Reviews {
		reviews = append(reviews, r.ID)
	}
	for _, c := range api[0].Comments {
		comments = append(comments, c.ID)
	}
	if !reflect.DeepEqual(reviews, []string{"r0", "r1", "r2"}) || !reflect.DeepEqual(comments, []string{"c1", "c2"}) {
		t.Errorf("reviews %q, comments %q; want oldest first, ties by id", reviews, comments)
	}
	if !reflect.DeepEqual(api[0].IssueKeys, []string{"ENG-42"}) {
		t.Errorf("issue keys = %q, want [ENG-42]", api[0].IssueKeys)
	}
	if web, err := readJSONL[ReviewRecord](filepath.Join(h.repoDir("web"), "reviews.jsonl")); err != nil || len(web) != 1 {
		t.Errorf("web reviews = %+v, %v", web, err)
	}
	if fileExists(filepath.Join(h.repoDir("old"), "reviews.jsonl")) {
		t.Error("a repo without the user's reviews kept reviews.jsonl")
	}
	var s ReviewsSummary
	if ok, err := readJSON(filepath.Join(h.raw, "org", "reviews.json"), &s); !ok || err != nil {
		t.Fatalf("reviews.json: %v %v", ok, err)
	}
	got := []int{s.PRs, s.Reviews, s.ReviewComments, s.Comments, s.Repos}
	if want := []int{2, 3, 2, 3, 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("summary PRs, reviews, review comments, comments, repos = %v, want %v", got, want)
	}
}

// With no reviews found, reviews.json still says the stage ran, so none reads as none.
func TestReviewsNoneFound(t *testing.T) {
	h := newTestHarness(t, Tools{Host: &fakeHost{}})
	if err := h.reviews(context.Background()); err != nil {
		t.Fatal(err)
	}
	var s ReviewsSummary
	if ok, err := readJSON(filepath.Join(h.raw, "org", "reviews.json"), &s); !ok || err != nil || s.PRs != 0 || s.GeneratedAt == "" {
		t.Errorf("reviews.json = %+v, %v %v", s, ok, err)
	}
}
