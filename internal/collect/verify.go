package collect

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

// MissingCommit is a commit the code host attributes to the account that the dataset lacks.
type MissingCommit struct {
	Repo    string `json:"repo"`
	SHA     string `json:"sha"`
	Date    string `json:"date"`
	Author  string `json:"author"`
	Subject string `json:"subject"`
}

// Verification is <data>/org/verification.json.
type Verification struct {
	GeneratedAt                    string          `json:"generated_at"`
	Method                         string          `json:"method"`
	HostAttributedCommits          int             `json:"host_attributed_commits"`
	DatasetCommits                 int             `json:"dataset_commits"`
	DatasetAuthoredOnDefaultBranch int             `json:"dataset_authored_on_default_branch"`
	AttributedMissingFromDataset   int             `json:"attributed_commits_missing_from_dataset"`
	Missing                        []MissingCommit `json:"missing"`
}

// verify cross-checks the dataset against the code host's own attribution: every default-branch
// commit the host links to the account should be present. This catches identity variants the
// config does not know about.
func (h *harness) verify(ctx context.Context) error {
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
	attributed, err := h.tools.Host.AttributedCommits(ctx, h.cfg.Login, 0)
	if err != nil {
		return err
	}
	host := map[key]MissingCommit{}
	for _, c := range attributed {
		host[key{c.Repo, c.SHA}] = MissingCommit{Repo: c.Repo, SHA: c.SHA, Date: c.Date,
			Author: c.AuthorName + " <" + c.AuthorEmail + ">", Subject: c.Subject}
	}
	missing := []MissingCommit{}
	for k, c := range host {
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
	err = writeJSON(filepath.Join(h.raw, "org", "verification.json"), Verification{
		GeneratedAt:                    nowISO(),
		Method:                         "default-branch commits the code host attributes to the account, compared with the dataset",
		HostAttributedCommits:          len(host),
		DatasetCommits:                 len(ours),
		DatasetAuthoredOnDefaultBranch: authoredDefault,
		AttributedMissingFromDataset:   len(missing),
		Missing:                        missing,
	})
	if err != nil {
		return err
	}
	logf("verify: the code host attributes %d commits; %d missing from dataset of %d", len(host), len(missing), len(ours))
	return nil
}
