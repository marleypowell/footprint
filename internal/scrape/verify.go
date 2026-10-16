package scrape

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"time"
)

// MissingCommit is a commit GitHub attributes to the account that the dataset lacks.
type MissingCommit struct {
	Repo    string `json:"repo"`
	SHA     string `json:"sha"`
	Date    string `json:"date"`
	Author  string `json:"author"`
	Subject string `json:"subject"`
}

// verify cross-checks the dataset against GitHub's own attribution: every default-branch commit
// that commit search links to the account should be present. This catches identity variants the
// config does not know about.
func (h *harness) verify(ctx context.Context) error {
	gh, err := h.github(ctx)
	if err != nil {
		return err
	}
	type key struct{ repo, sha string }
	ours := map[key]Commit{}
	entries, err := os.ReadDir(filepath.Join(h.raw, "repos"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		rows, err := readJSONL[Commit](filepath.Join(h.raw, "repos", e.Name(), "commits.jsonl"))
		if err != nil {
			return err
		}
		for _, r := range rows {
			ours[key{r.Repo, r.SHA}] = r
		}
	}

	type span struct{ lo, hi time.Time }
	pending := []span{{time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), tomorrow()}}
	github := map[key]MissingCommit{}
	for len(pending) > 0 {
		s := pending[0]
		pending = pending[1:]
		q := fmt.Sprintf("author:%s org:%s author-date:%s..%s", h.cfg.GitHubLogin, h.cfg.Org,
			s.lo.Format(dateLayout), s.hi.Format(dateLayout))
		var found []MissingCommit
		for page := 1; ; page++ {
			res, err := gh.searchCommits(ctx, url.Values{"q": {q}, "per_page": {"100"}, "page": {strconv.Itoa(page)}})
			if err != nil {
				return err
			}
			if page == 1 && res.TotalCount > 1000 && s.lo.Before(s.hi) { // over the search cap: halve the range
				mid, next := splitDays(s.lo, s.hi)
				pending = append([]span{{s.lo, mid}, {next, s.hi}}, pending...)
				break
			}
			for _, it := range res.Items {
				found = append(found, MissingCommit{
					Repo: it.Repository.Name, SHA: it.SHA, Date: it.Commit.Author.Date,
					Author:  it.Commit.Author.Name + " <" + it.Commit.Author.Email + ">",
					Subject: firstLine(it.Commit.Message),
				})
			}
			if len(res.Items) < 100 || page == 10 {
				for _, c := range found {
					github[key{c.Repo, c.SHA}] = c
				}
				logf("verify: %s..%s -> %d/%d", s.lo.Format(dateLayout), s.hi.Format(dateLayout), len(found), res.TotalCount)
				break
			}
		}
	}
	missing := []MissingCommit{}
	for k, c := range github {
		if _, ok := ours[k]; !ok {
			missing = append(missing, c)
		}
	}
	sort.Slice(missing, func(i, j int) bool {
		if missing[i].Date != missing[j].Date {
			return missing[i].Date < missing[j].Date
		}
		return missing[i].Repo+missing[i].SHA < missing[j].Repo+missing[j].SHA
	})
	authoredDefault := 0
	for _, r := range ours {
		if r.OnDefaultBranch && slices.Contains(r.Roles, "author") {
			authoredDefault++
		}
	}
	err = writeJSON(filepath.Join(h.raw, "org", "verification.json"), struct {
		GeneratedAt                    string          `json:"generated_at"`
		Method                         string          `json:"method"`
		GitHubSearchCommits            int             `json:"github_search_commits"`
		DatasetCommits                 int             `json:"dataset_commits"`
		DatasetAuthoredOnDefaultBranch int             `json:"dataset_authored_on_default_branch"`
		GitHubCommitsMissingFromData   int             `json:"github_commits_missing_from_dataset"`
		Missing                        []MissingCommit `json:"missing"`
	}{
		GeneratedAt:                    nowISO(),
		Method:                         "GitHub commit search (default branches, commits GitHub links to the account) vs dataset",
		GitHubSearchCommits:            len(github),
		DatasetCommits:                 len(ours),
		DatasetAuthoredOnDefaultBranch: authoredDefault,
		GitHubCommitsMissingFromData:   len(missing),
		Missing:                        missing,
	})
	if err != nil {
		return err
	}
	logf("verify: GitHub links %d commits; %d missing from dataset of %d", len(github), len(missing), len(ours))
	return nil
}
