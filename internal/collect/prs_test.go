package collect

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/codehost"
)

func prNumbers(rows []PullRequestRecord) []int {
	var numbers []int
	for _, r := range rows {
		numbers = append(numbers, r.Number)
	}
	return numbers
}

// prs writes the user's PRs per repo, by number, with the issue keys their title, body and branch
// name; a repo where the user has no PRs any more loses its prs.jsonl, and only that.
func TestPRs(t *testing.T) {
	host := &fakeHost{prs: []codehost.PullRequest{
		{Repo: "web", Number: 12, Title: "ENG-5: dark mode"},
		{Repo: "api", Number: 3, Title: "Retry sends", Body: "Fixes eng-9", HeadRef: "feature/ENG-10-retries"},
		{Repo: "api", Number: 1, Title: "First"},
	}}
	h := newTestHarness(t, Tools{Host: host, Tracker: &fakeTracker{}})
	writeRows(t, h, "repos/old/prs.jsonl", PullRequestRecord{PullRequest: codehost.PullRequest{Repo: "old", Number: 1}})
	writeRows(t, h, "repos/old/commits.jsonl", Commit{Repo: "old", SHA: "a"})
	if err := h.prs(context.Background()); err != nil {
		t.Fatal(err)
	}
	api, err := readJSONL[PullRequestRecord](filepath.Join(h.repoDir("api"), "prs.jsonl"))
	if err != nil || !reflect.DeepEqual(prNumbers(api), []int{1, 3}) {
		t.Fatalf("api PRs = %v, %v, want [1 3]", prNumbers(api), err)
	}
	if want := []string{"ENG-10", "ENG-9"}; !reflect.DeepEqual(api[1].IssueKeys, want) {
		t.Errorf("PR 3 issue keys = %q, want %q (from body and branch name)", api[1].IssueKeys, want)
	}
	if web, err := readJSONL[PullRequestRecord](filepath.Join(h.repoDir("web"), "prs.jsonl")); err != nil || !reflect.DeepEqual(web[0].IssueKeys, []string{"ENG-5"}) {
		t.Errorf("web PRs = %+v, %v", web, err)
	}
	if fileExists(filepath.Join(h.repoDir("old"), "prs.jsonl")) || !fileExists(filepath.Join(h.repoDir("old"), "commits.jsonl")) {
		t.Error("a repo without the user's PRs should lose prs.jsonl and keep its other files")
	}
}

func TestPRsWithoutAnIssueTracker(t *testing.T) {
	host := &fakeHost{prs: []codehost.PullRequest{{Repo: "api", Number: 3, Title: "ENG-5: dark mode"}}}
	h := newTestHarness(t, Tools{Host: host})
	if err := h.prs(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(h.repoDir("api"), "prs.jsonl"))
	if err != nil || !strings.Contains(string(got), `"issue_keys":[]`) {
		t.Errorf("prs.jsonl = %s, %v; want issue_keys [] (not null) without a tracker", got, err)
	}
}
