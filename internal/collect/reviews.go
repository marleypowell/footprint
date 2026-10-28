package collect

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marleypowell/footprint/internal/codehost"
)

// ReviewRecord is one line of <data>/repos/<repo>/reviews.jsonl: a PR someone else opened that the
// user reviewed or commented on, with the issue keys it mentions.
type ReviewRecord struct {
	codehost.ReviewedPullRequest
	IssueKeys []string `json:"issue_keys"`
}

// ReviewsSummary is <data>/org/reviews.json. Its presence says the reviews were collected, so that
// none found reads as none rather than unknown.
type ReviewsSummary struct {
	GeneratedAt    string `json:"generated_at"`
	PRs            int    `json:"prs"`
	Reviews        int    `json:"reviews"`
	ReviewComments int    `json:"review_comments"` // inline, on lines of the diff
	Comments       int    `json:"comments"`        // on the conversation
	Repos          int    `json:"repos"`
}

// reviews fetches every PR in the org that someone else opened and the user reviewed or commented
// on, with only the user's reviews and comments.
func (h *harness) reviews(ctx context.Context) error {
	issueKeys, err := h.issueKeys(ctx)
	if err != nil {
		return err
	}
	reviewed, err := h.tools.Host.Reviewed(ctx, h.cfg.Login)
	if err != nil {
		return err
	}
	byRepo := map[string]map[int]ReviewRecord{}
	for _, pr := range reviewed {
		if pr.Author != nil && strings.EqualFold(*pr.Author, h.cfg.Login) { // the user's own: prs.jsonl
			continue
		}
		if len(pr.Reviews) == 0 && len(pr.Comments) == 0 {
			continue
		}
		sort.SliceStable(pr.Reviews, func(i, j int) bool {
			a, b := pr.Reviews[i], pr.Reviews[j]
			return a.SubmittedAt < b.SubmittedAt || a.SubmittedAt == b.SubmittedAt && a.ID < b.ID
		})
		sort.SliceStable(pr.Comments, func(i, j int) bool {
			a, b := pr.Comments[i], pr.Comments[j]
			return a.CreatedAt < b.CreatedAt || a.CreatedAt == b.CreatedAt && a.ID < b.ID
		})
		if byRepo[pr.Repo] == nil {
			byRepo[pr.Repo] = map[int]ReviewRecord{}
		}
		byRepo[pr.Repo][pr.Number] = ReviewRecord{ReviewedPullRequest: pr, IssueKeys: issueKeys(pr.Title, pr.Body, pr.HeadRef)}
	}
	entries, err := os.ReadDir(filepath.Join(h.raw, "repos"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, e := range entries { // repos where the user no longer has reviews
		if _, ok := byRepo[e.Name()]; !ok {
			if err := os.Remove(filepath.Join(h.raw, "repos", e.Name(), "reviews.jsonl")); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	summary := ReviewsSummary{GeneratedAt: nowISO(), Repos: len(byRepo)}
	for _, repo := range sortedKeys(byRepo) {
		numbers := make([]int, 0, len(byRepo[repo]))
		for n := range byRepo[repo] {
			numbers = append(numbers, n)
		}
		sort.Ints(numbers)
		rows := make([]ReviewRecord, len(numbers))
		for i, n := range numbers {
			rows[i] = byRepo[repo][n]
			summary.Reviews += len(rows[i].Reviews)
			summary.Comments += len(rows[i].Comments)
			for _, r := range rows[i].Reviews {
				summary.ReviewComments += len(r.Comments)
			}
		}
		if err := writeJSONL(filepath.Join(h.repoDir(repo), "reviews.jsonl"), rows); err != nil {
			return err
		}
		summary.PRs += len(rows)
	}
	if err := writeJSON(filepath.Join(h.raw, "org", "reviews.json"), summary); err != nil {
		return err
	}
	logf("reviews: %d PRs by others in %d repos: %d reviews, %d inline and %d conversation comments",
		summary.PRs, summary.Repos, summary.Reviews, summary.ReviewComments, summary.Comments)
	return nil
}
