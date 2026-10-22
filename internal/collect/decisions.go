package collect

import (
	"context"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/marleypowell/footprint/internal/tracker"
)

var (
	// decisionRE matches decision records by the common layouts: a Markdown file in a directory
	// named adr, adrs, decisions, decision-records or architecture-decisions (adr-tools, MADR,
	// log4brains), at any depth, or a file named ADR-<n>.
	decisionRE = regexp.MustCompile(`(?i)((^|/)(adrs?|decisions|decision[-_]records?|architecture[-_]decisions?)/[^/]+|(^|/)adr[-_]?\d+[^/]*)\.md$`)
	// notDecisionRE is the index and template files kept beside them.
	notDecisionRE = regexp.MustCompile(`(?i)^(readme|index|toc|_?template|.*[-_]template)\.md$`)
)

// Decision is one line of <data>/repos/<repo>/decisions.jsonl: an architecture decision record on
// the default branch whose history has a commit of the user's. docs/data-schema.md defines the
// fields.
type Decision struct {
	Repo      string            `json:"repo"`
	Path      string            `json:"path"`
	URL       string            `json:"url"`
	Title     string            `json:"title"`
	IssueKeys []string          `json:"issue_keys"`
	CreatedAt string            `json:"created_at"`
	Versions  []DecisionVersion `json:"versions"`
	Bytes     int               `json:"bytes"`
	Truncated bool              `json:"truncated"`
	Body      string            `json:"body"`
}

// DecisionVersion is one commit on the default branch that changed a decision record.
type DecisionVersion struct {
	SHA     string    `json:"sha"`
	Path    string    `json:"path"` // the file's path in that commit, which renames change
	Author  Signature `json:"author"`
	Subject string    `json:"subject"`
	Roles   []string  `json:"roles"` // the user's roles in it, as in commits.jsonl; [] when not theirs
}

// decisions finds the repo's decision records on its default branch and keeps those with a commit
// of the user's in their history, oldest version first.
func (h *harness) decisions(ctx context.Context, repo Repo, commits []Commit, issueKeys tracker.KeyFunc) ([]Decision, error) {
	mirror, branch := h.mirrorPath(repo.Name), repo.DefaultBranchName()
	ref := "refs/heads/" + branch
	if branch == "" || !hasRef(ctx, mirror, ref) {
		return nil, nil
	}
	roles := map[string][]string{}
	for _, c := range commits {
		roles[c.SHA] = c.Roles
	}
	tree, err := lsTree(ctx, mirror, ref)
	if err != nil {
		return nil, err
	}
	var out []Decision
	for _, file := range tree {
		if !isDecision(file) {
			continue
		}
		versions, err := fileHistory(ctx, mirror, ref, file)
		if err != nil {
			return nil, err
		}
		mine := false
		for i := range versions {
			versions[i].Roles = orEmpty(slices.Clone(roles[versions[i].SHA]))
			mine = mine || len(versions[i].Roles) > 0
		}
		if !mine {
			continue
		}
		blob, err := git(ctx, mirror, nil, "cat-file", "blob", ref+":"+file)
		if err != nil {
			return nil, err
		}
		body := strings.ToValidUTF8(string(blob[:min(len(blob), maxDocBytes)]), "�")
		title := markdownTitle(body, file)
		out = append(out, Decision{
			Repo: repo.Name, Path: file, URL: h.tools.Host.FileURL(repo, branch, file), Title: title,
			IssueKeys: issueKeys(title + "\n" + body), CreatedAt: versions[0].Author.Date, Versions: versions,
			Bytes: len(blob), Truncated: len(blob) > maxDocBytes, Body: body,
		})
	}
	return out, nil
}

func isDecision(file string) bool {
	return decisionRE.MatchString(file) && !notDecisionRE.MatchString(path.Base(file))
}

// fileHistory is every non-merge commit at ref that changed file, following renames, oldest first.
func fileHistory(ctx context.Context, mirror, ref, file string) ([]DecisionVersion, error) {
	out, err := git(ctx, mirror, nil, "log", "--no-merges", "--follow", "--name-only", "-z",
		"--format=%x1e%H%x1f%an%x1f%ae%x1f%aI%x1f%s%x1f", ref, "--", file)
	if err != nil {
		return nil, err
	}
	var versions []DecisionVersion
	for _, chunk := range strings.Split(string(out), "\x1e") {
		f := strings.SplitN(chunk, "\x1f", 6)
		if len(f) != 6 {
			continue
		}
		versions = append(versions, DecisionVersion{
			SHA: f[0], Path: strings.Trim(f[5], "\x00\n"), Author: Signature{Name: f[1], Email: f[2], Date: f[3]}, Subject: f[4],
		})
	}
	slices.Reverse(versions)
	return versions, nil
}

// markdownTitle is the document's first level-one heading, after any front matter, or else its
// file name.
func markdownTitle(body, file string) string {
	lines := strings.Split(body, "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				lines = lines[i+1:]
				break
			}
		}
	}
	for _, line := range lines {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "# ") {
			return strings.TrimSpace(t[2:])
		}
	}
	return strings.TrimSuffix(path.Base(file), path.Ext(file))
}
