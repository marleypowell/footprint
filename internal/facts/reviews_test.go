package facts

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/codehost"
	"github.com/marleypowell/footprint/internal/collect"
)

func TestReviews(t *testing.T) {
	f := build(t, fixture(), Inputs{})
	var refs []string
	for _, r := range f.Reviews {
		refs = append(refs, r.Ref+" "+r.AuthorClass+" "+r.Item)
	}
	check(t, "reviews, and the items they count toward", refs, []string{
		"review+app#21 colleague ENG-1", "review+app#22 bot ", "review+app#23 colleague ", "review+app#24 colleague "})
	r := f.reviews["review+app#21"]
	check(t, "a review's counts", []any{r.Reviews, r.Comments, r.Words, r.First, r.Last, r.did()},
		[]any{ReviewStates{Approved: 1, ChangesRequested: 1}, 2, 8, "2024-01-10", "2024-01-11", "changes requested, approved; 2 comments"})

	it := f.items["ENG-1"]
	check(t, "an item lists the reviews naming it", it.Reviews, []string{"review+app#21"})
	check(t, "reviews count in an item's totals", []int{it.Totals.PRsReviewed, it.Totals.ChangesRequested, it.Totals.ReviewComments}, []int{1, 1, 2})
	for _, d := range it.days {
		if d == "2024-01-10" || d == "2024-01-11" {
			t.Errorf("review days are not the item's: %v", it.days)
		}
	}
	all := activeDays(f.events, All)
	if !slices.Contains(all, "2024-01-10") || !slices.Contains(all, "2024-04-05") {
		t.Errorf("review days are the user's active days: %v", all)
	}
	pack := string(renderPack(it.pack, it.Pack))
	if !strings.Contains(pack, "## Your reviews of others' PRs naming it (their work, not yours)\n\n"+
		"- 2024-01-10 review+app#21 · changes requested, approved; 2 comments · by a colleague · theirs 21: Needs a backoff before retrying / cap this / thanks\n") {
		t.Errorf("pack:\n%s", pack)
	}
	if strings.Contains(string(renderPack(f.items["ENG-2"].pack, "")), "reviews") {
		t.Error("a pack without reviews mentions them")
	}
	if b, _ := json.Marshal(f.items["ENG-2"].pack); strings.Contains(string(b), `"reviews"`) {
		t.Error("a pack without reviews has a reviews field, which changes its hash")
	}

	head := "---\nitem: ENG-1\ninputs: " + it.Pack + "\n---\n"
	var body strings.Builder
	for _, s := range NoteSections {
		body.WriteString("\n## " + s + "\n\nReviewed [[review+app#21]], not [[review+app#24]].\n")
	}
	findings := f.Check("notes/ENG-1.md", head+body.String())
	if len(findings) == 0 || !strings.Contains(findings[0].String(), "review+app#24") || strings.Contains(fmt.Sprint(findings), "review+app#21") {
		t.Errorf("findings: %v", findings)
	}
	final := f.Final("---\nwindow: all\n---\nI reviewed 2 PRs by 2 people [[review+app#21]].\n", true)
	check(t, "a review links to its PR", final, "I reviewed 2 PRs by 2 people [review+app#21](https://code.example/org/app/pull/21).\n")
	if fd := f.Check("draft.md", "---\nwindow: all\n---\nI reviewed 2 PRs and asked for changes once, in 2 comments [[review+app#21]].\n"); len(fd) > 0 {
		t.Errorf("review numbers in scope: %v", fd)
	}

	check(t, "not collected, once reviews are", f.Summary(All).Coverage.NotCollected[0], reviewsNotCollected)
	raw := fixture()
	raw.Reviews, raw.ReviewsRan = nil, false
	check(t, "not collected, before", build(t, raw, Inputs{}).Summary(All).Coverage.NotCollected[0], notCollected[0])
}

// did is what the user did on a PR, in a pack's words: the states of their reviews, strongest
// first, then their comments.
func TestReviewDid(t *testing.T) {
	for _, tc := range []struct {
		r    Review
		want string
	}{
		{Review{}, ""},
		{Review{Comments: 1}, "1 comment"},
		{Review{Comments: 3}, "3 comments"},
		{Review{Reviews: ReviewStates{Approved: 2}}, "approved"},
		{Review{Reviews: ReviewStates{Approved: 1, ChangesRequested: 2}, Comments: 2}, "changes requested, approved; 2 comments"},
		{Review{Reviews: ReviewStates{Commented: 1, Dismissed: 1}}, "commented, a review dismissed"},
	} {
		if got := tc.r.did(); got != tc.want {
			t.Errorf("did(%+v, %d comments) = %q, want %q", tc.r.Reviews, tc.r.Comments, got, tc.want)
		}
	}
	check(t, "reviewRef", reviewRef("app", 21), "review+app#21")
}

// Every reviewed PR with dated activity of the user's is a review: of a colleague, an agent or a
// bot (a bot's reviews counted apart), counted toward an item of the user's that it names, and
// never making one.
func TestBuildReviews(t *testing.T) {
	raw := oneRepo([]collect.Commit{
		commit("c1", "2024-01-02T10:00:00Z", jane, []string{"author"}, "ENG-1: mine", func(c *collect.Commit) { c.IssueKeys = []string{"ENG-1"} }),
	}, nil, []collect.IssueRecord{issueRec("ENG-1", 0, nil), issueRec("ENG-2", 0, nil)})
	reviewed := func(n int, author string, keys []string, reviews []codehost.ReviewerReview, comments ...codehost.Comment) collect.ReviewRecord {
		return collect.ReviewRecord{ReviewedPullRequest: codehost.ReviewedPullRequest{Repo: "app", Number: n, URL: "https://code.example/org/app/pull/" + itoa(n),
			Title: "theirs", State: "MERGED", Author: ptr(author), Reviews: reviews, Comments: comments}, IssueKeys: keys}
	}
	approved := []codehost.ReviewerReview{{State: codehost.ReviewApproved, SubmittedAt: "2024-01-05T10:00:00Z"}}
	raw.Reviews = []collect.ReviewRecord{
		reviewed(36, "sam", nil, []codehost.ReviewerReview{{State: codehost.ReviewDismissed, SubmittedAt: "2024-01-09T10:00:00Z",
			Comments: []codehost.ReviewComment{{Body: "nit", CreatedAt: "2024-01-10T10:00:00Z"}}}}),
		reviewed(31, "dependabot[bot]", nil, approved), // a bot by its name alone
		reviewed(32, "Copilot", nil, []codehost.ReviewerReview{{State: codehost.ReviewCommented, SubmittedAt: "2024-01-06T10:00:00Z", Body: " Looks  good\n to me "}}),
		reviewed(33, "sam", []string{"ENG-2"}, approved),
		reviewed(34, "sam", []string{"eng-1"}, []codehost.ReviewerReview{{State: codehost.ReviewChangesRequested, SubmittedAt: "2024-01-07T10:00:00Z"}}),
		reviewed(35, "sam", []string{"ENG-1"}, []codehost.ReviewerReview{{State: codehost.ReviewApproved}}), // no date: nothing to count
	}
	f := build(t, raw, Inputs{})
	var got []string
	for _, r := range f.Reviews {
		got = append(got, fmt.Sprintf("%s %s %q %s..%s %d", r.Ref, r.AuthorClass, r.Item, r.First, r.Last, r.Words))
	}
	check(t, "reviews", got, []string{
		`review+app#31 bot "" 2024-01-05..2024-01-05 0`,
		`review+app#32 agent "" 2024-01-06..2024-01-06 4`,
		`review+app#33 colleague "" 2024-01-05..2024-01-05 0`, // ENG-2 is no item of the user's
		`review+app#34 colleague "ENG-1" 2024-01-07..2024-01-07 0`,
		`review+app#36 colleague "" 2024-01-09..2024-01-10 1`,
	})
	check(t, "a bot's review is counted apart", kinds(f.reviews["review+app#31"].evs), []string{evBotReview, evActive})
	check(t, "a review requesting changes", kinds(f.reviews["review+app#34"].evs), []string{evChangesRequested, evActive})
	check(t, "a comment on the diff", []any{f.reviews["review+app#36"].did(), kinds(f.reviews["review+app#36"].evs)},
		[]any{"a review dismissed; 1 comment", []string{evReview, evActive, evReviewComment, evActive}})
	check(t, "the user's words, on one line", f.reviews["review+app#32"].texts, []packText{{Ref: "review+app#32", Day: "2024-01-06", Text: "Looks good to me"}})
	if _, ok := f.items["ENG-2"]; ok {
		t.Error("a review made an item")
	}
}
