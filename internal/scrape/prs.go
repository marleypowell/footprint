package scrape

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/marleypowell/footprint/internal/codehost"
)

// PullRequestRecord is one line of <data>/repos/<repo>/prs.jsonl: a PR the user authored, with
// the issue keys it mentions.
type PullRequestRecord struct {
	codehost.PullRequest
	IssueKeys []string `json:"issue_keys"`
}

// prs fetches every PR the user authored in the org, with its commits and files.
func (h *harness) prs(ctx context.Context) error {
	issueKeys, err := h.issueKeys(ctx)
	if err != nil {
		return err
	}
	prs, err := h.tools.Host.PullRequests(ctx, h.cfg.Login)
	if err != nil {
		return err
	}
	byRepo := map[string]map[int]PullRequestRecord{}
	for _, pr := range prs {
		if byRepo[pr.Repo] == nil {
			byRepo[pr.Repo] = map[int]PullRequestRecord{}
		}
		byRepo[pr.Repo][pr.Number] = PullRequestRecord{PullRequest: pr, IssueKeys: issueKeys(pr.Title, pr.Body, pr.HeadRef)}
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
		numbers := make([]int, 0, len(byRepo[repo]))
		for n := range byRepo[repo] {
			numbers = append(numbers, n)
		}
		sort.Ints(numbers)
		rows := make([]PullRequestRecord, len(numbers))
		for i, n := range numbers {
			rows[i] = byRepo[repo][n]
		}
		if err := writeJSONL(filepath.Join(h.repoDir(repo), "prs.jsonl"), rows); err != nil {
			return err
		}
		total += len(rows)
	}
	logf("prs: %d PRs in %d repos", total, len(byRepo))
	return nil
}
