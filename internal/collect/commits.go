package collect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/marleypowell/footprint/internal/par"
	"github.com/marleypowell/footprint/internal/tracker"
)

const (
	// detailFormat brackets each commit with \x1e ... \x1d; the diff output follows the \x1d.
	detailFormat      = "%x1e%H%x1f%P%x1f%an%x1f%ae%x1f%aI%x1f%cn%x1f%ce%x1f%cI%x1f%s%x1f%b%x1f%B%x1d"
	maxFilesPerCommit = 5000
	maxRefsPerKind    = 50
)

// Commit is one line of <data>/repos/<repo>/commits.jsonl. docs/data-schema.md defines the fields.
type Commit struct {
	Repo                       string       `json:"repo"`
	SHA                        string       `json:"sha"`
	URL                        string       `json:"url"`
	Roles                      []string     `json:"roles"`
	MatchedBy                  MatchedBy    `json:"matched_by"`
	InUserPRs                  []int        `json:"in_user_prs"`
	MergeCommitForUserPRs      []int        `json:"merge_commit_for_user_prs"`
	Author                     Signature    `json:"author"`
	Committer                  Signature    `json:"committer"`
	CoAuthors                  []Person     `json:"co_authors"`
	Subject                    string       `json:"subject"`
	Body                       string       `json:"body"`
	Message                    string       `json:"message"`
	Parents                    []string     `json:"parents"`
	IsMerge                    bool         `json:"is_merge"`
	OnDefaultBranch            bool         `json:"on_default_branch"`
	PRNumbersInMessage         []int        `json:"pr_numbers_in_message"`
	MigratedPRNumbersInMessage []int        `json:"migrated_pr_numbers_in_message"`
	IssueKeys                  []string     `json:"issue_keys"`
	BranchIssueKeys            []string     `json:"branch_issue_keys"` // those of issue_keys only branch names supply
	Stats                      *CommitStats `json:"stats"`
	Files                      []FileChange `json:"files"`
	FilesTruncated             bool         `json:"files_truncated"`
	PatchID                    *string      `json:"patch_id"`
	Branches                   []string     `json:"branches"`
	PullRequestRefs            []int        `json:"pull_request_refs"`
	Tags                       []string     `json:"tags"`
	ContainingRefCounts        RefCounts    `json:"containing_ref_counts"`
}

// MatchedBy records, per role, how the user was matched ("email", "name", or "authored_pr").
type MatchedBy struct {
	Author    string `json:"author,omitempty"`
	Committer string `json:"committer,omitempty"`
	CoAuthor  string `json:"co-author,omitempty"`
	PRAuthor  string `json:"pr-author,omitempty"`
}

func (m MatchedBy) roles() []string {
	roles := []string{}
	for _, r := range []struct{ name, basis string }{
		{"author", m.Author}, {"committer", m.Committer}, {"co-author", m.CoAuthor}, {"pr-author", m.PRAuthor},
	} {
		if r.basis != "" {
			roles = append(roles, r.name)
		}
	}
	return roles
}

type Signature struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Date  string `json:"date"`
}

type CommitStats struct {
	Files       int `json:"files"`
	Additions   int `json:"additions"`
	Deletions   int `json:"deletions"`
	BinaryFiles int `json:"binary_files"`
}

// FileChange is one changed path. Additions and Deletions are null for binary files.
type FileChange struct {
	Path       string `json:"path"`
	OldPath    string `json:"old_path,omitempty"`
	Status     string `json:"status"`
	Similarity *int   `json:"similarity,omitempty"`
	Binary     bool   `json:"binary"`
	Additions  *int   `json:"additions"`
	Deletions  *int   `json:"deletions"`
}

type RefCounts struct {
	Branches        int `json:"branches"`
	PullRequestRefs int `json:"pull_request_refs"`
	Tags            int `json:"tags"`
}

// commits extracts the user's commits from every mirrored repo.
func (h *harness) commits(ctx context.Context) error {
	repos, err := h.mirroredRepos()
	if err != nil {
		return err
	}
	repos = h.selected(repos)
	issueKeys, err := h.issueKeys(ctx)
	if err != nil {
		return err
	}
	var done, total, active atomic.Int64
	err = par.ForEach(ctx, 6, repos, func(ctx context.Context, _ int, r Repo) error {
		rows, err := h.extractCommits(ctx, r, issueKeys)
		if err != nil {
			return fmt.Errorf("%s: %w", r.Name, err)
		}
		out := filepath.Join(h.repoDir(r.Name), "commits.jsonl")
		if len(rows) > 0 {
			if err := writeJSONL(out, rows); err != nil {
				return err
			}
		} else if err := os.Remove(out); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		i := done.Add(1)
		total.Add(int64(len(rows)))
		if len(rows) > 0 {
			active.Add(1)
			logf("commits %d/%d %s: %d", i, len(repos), r.Name, len(rows))
		}
		return nil
	})
	if err != nil {
		return err
	}
	logf("commits: %d commits in %d repos", total.Load(), active.Load())
	return nil
}

// extractCommits finds every commit on any ref of a repo that involves the user.
func (h *harness) extractCommits(ctx context.Context, repo Repo, issueKeys tracker.KeyFunc) ([]Commit, error) {
	path := h.mirrorPath(repo.Name)
	roles, mentions, err := h.candidates(ctx, path)
	if err != nil {
		return nil, err
	}

	// commits in, or landing, the user's own PRs (agent commits, colleagues' pushes)
	prs, err := readJSONL[PullRequestRecord](filepath.Join(h.repoDir(repo.Name), "prs.jsonl"))
	if err != nil {
		return nil, err
	}
	inMyPRs, landsMyPRs := map[string][]int{}, map[string][]int{}
	for _, pr := range prs {
		for _, c := range pr.Commits {
			inMyPRs[c.SHA] = append(inMyPRs[c.SHA], pr.Number)
		}
		if sha := deref(pr.MergeCommitSHA); sha != "" {
			landsMyPRs[sha] = append(landsMyPRs[sha], pr.Number)
		}
	}
	wanted := map[string]bool{}
	for sha := range inMyPRs {
		wanted[sha] = true
	}
	for sha := range landsMyPRs {
		wanted[sha] = true
	}
	prSHAs := map[string]bool{}
	if len(wanted) > 0 {
		if prSHAs, err = existingCommits(ctx, path, sortedKeys(wanted)); err != nil {
			return nil, err
		}
		if missing := len(wanted) - len(prSHAs); missing > 0 { // merged into a deleted branch, or history rewritten
			logf("commits %s: %d PR commit(s) unreachable from any ref, skipped", repo.Name, missing)
		}
	}

	all := map[string]bool{}
	for sha := range roles {
		all[sha] = true
	}
	for sha := range mentions {
		all[sha] = true
	}
	for sha := range prSHAs {
		all[sha] = true
	}
	if len(all) == 0 {
		return nil, nil
	}
	candidates := sortedKeys(all)
	details, err := commitDetails(ctx, path, candidates)
	if err != nil {
		return nil, err
	}
	defaultRef := "refs/heads/" + repo.DefaultBranchName()
	onDefault := map[string]bool{}
	if repo.DefaultBranchName() != "" && hasRef(ctx, path, defaultRef) {
		out, err := git(ctx, path, nil, "rev-list", defaultRef)
		if err != nil {
			return nil, err
		}
		for _, sha := range strings.Fields(string(out)) {
			onDefault[sha] = true
		}
	}

	var rows []Commit
	for _, sha := range candidates {
		d, ok := details[sha]
		if !ok {
			return nil, fmt.Errorf("git log returned no record for %s", sha)
		}
		matched := roles[sha]
		coAuthors := parseCoauthors(d.message)
		for _, p := range coAuthors {
			if basis := h.id.match(p.Name, p.Email); basis != "" && matched.CoAuthor == "" {
				matched.CoAuthor = basis
			}
		}
		if prSHAs[sha] {
			matched.PRAuthor = "authored_pr"
		}
		if len(matched.roles()) == 0 { // the message mentioned the user for some other reason
			continue
		}
		isMerge := len(d.parents) > 1
		var stats *CommitStats
		if !isMerge {
			stats = &CommitStats{Files: len(d.files)}
			for _, f := range d.files {
				stats.Additions += deref(f.Additions)
				stats.Deletions += deref(f.Deletions)
				if f.Binary {
					stats.BinaryFiles++
				}
			}
		}
		rows = append(rows, Commit{
			Repo:                       repo.Name,
			SHA:                        sha,
			URL:                        h.tools.Host.CommitURL(repo, sha),
			Roles:                      matched.roles(),
			MatchedBy:                  matched,
			InUserPRs:                  sortedInts(inMyPRs[sha]),
			MergeCommitForUserPRs:      sortedInts(landsMyPRs[sha]),
			Author:                     d.author,
			Committer:                  d.committer,
			CoAuthors:                  coAuthors,
			Subject:                    d.subject,
			Body:                       d.body,
			Message:                    d.message,
			Parents:                    d.parents,
			IsMerge:                    isMerge,
			OnDefaultBranch:            onDefault[sha],
			PRNumbersInMessage:         orEmpty(h.tools.Host.PullRequestNumbers(d.message)),
			MigratedPRNumbersInMessage: orEmpty(h.tools.Host.MigratedPullRequestNumbers(d.message)),
			IssueKeys:                  issueKeys(d.message),
			BranchIssueKeys:            []string{},
			Stats:                      stats,
			Files:                      d.files[:min(len(d.files), maxFilesPerCommit)],
			FilesTruncated:             len(d.files) > maxFilesPerCommit,
		})
	}

	targets := map[string]bool{}
	var withDiff []string
	for _, r := range rows {
		if !r.OnDefaultBranch {
			targets[r.SHA] = true
		}
		if !r.IsMerge {
			withDiff = append(withDiff, r.SHA)
		}
	}
	containment, err := refContainment(ctx, path, defaultRef, targets)
	if err != nil {
		return nil, err
	}
	patches, err := patchIDs(ctx, path, withDiff)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		r := &rows[i]
		if id, ok := patches[r.SHA]; ok {
			r.PatchID = &id
		}
		refs := classifyRefs(containment[r.SHA])
		r.Branches, r.PullRequestRefs, r.Tags, r.ContainingRefCounts = refs.branches, refs.prs, refs.tags, refs.counts
		fromBranches := issueKeys(strings.Join(refs.branches, " "))
		r.IssueKeys, r.BranchIssueKeys = mergeSorted(r.IssueKeys, fromBranches), subtractSorted(fromBranches, r.IssueKeys)
	}
	sort.Slice(rows, func(i, j int) bool {
		ti, tj := parseTime(rows[i].Author.Date), parseTime(rows[j].Author.Date)
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return rows[i].SHA < rows[j].SHA
	})
	return rows, nil
}

// candidates is pass 1: author/committer matches by sha, plus the shas whose message mentions the
// user (possible Co-authored-by trailers, checked in pass 2).
func (h *harness) candidates(ctx context.Context, path string) (map[string]MatchedBy, map[string]bool, error) {
	out, err := git(ctx, path, nil, "log", "--all", "--format=%H%x1f%an%x1f%ae%x1f%cn%x1f%ce")
	if err != nil {
		return nil, nil, err
	}
	roles := map[string]MatchedBy{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, "\x1f")
		if len(f) != 5 {
			continue
		}
		m := MatchedBy{Author: h.id.match(f[1], f[2]), Committer: h.id.match(f[3], f[4])}
		if m.Author != "" || m.Committer != "" {
			roles[f[0]] = m
		}
	}
	out, err = git(ctx, path, nil, "log", "--all", "-i", "-E", "--grep="+h.id.pattern, "--format=%H")
	if err != nil {
		return nil, nil, err
	}
	mentions := map[string]bool{}
	for _, sha := range strings.Fields(string(out)) {
		mentions[sha] = true
	}
	return roles, mentions, nil
}

type commitDetail struct {
	parents                []string
	author, committer      Signature
	subject, body, message string
	files                  []FileChange
}

// commitDetails is pass 2: full message, parents and per-file stats for the given commits.
func commitDetails(ctx context.Context, path string, shas []string) (map[string]commitDetail, error) {
	out, err := git(ctx, path, []byte(strings.Join(shas, "\n")), "log", "--no-walk=unsorted", "--stdin",
		"--format="+detailFormat, "--raw", "--numstat", "-z", "-M", "--diff-merges=off")
	if err != nil {
		return nil, err
	}
	details := map[string]commitDetail{}
	for _, chunk := range strings.Split(string(out), "\x1e") {
		if strings.Trim(chunk, "\x00\n") == "" {
			continue
		}
		header, diff, _ := strings.Cut(chunk, "\x1d")
		f := strings.SplitN(header, "\x1f", 11)
		if len(f) != 11 {
			return nil, fmt.Errorf("unexpected git log record %.80q", header)
		}
		details[f[0]] = commitDetail{
			parents:   strings.Fields(f[1]),
			author:    Signature{Name: f[2], Email: f[3], Date: f[4]},
			committer: Signature{Name: f[5], Email: f[6], Date: f[7]},
			subject:   f[8],
			body:      strings.TrimSpace(f[9]),
			message:   strings.TrimRight(f[10], "\n"),
			files:     parseDiff(diff),
		}
	}
	return details, nil
}

// parseDiff reads one commit's `--raw --numstat -z` output: raw entries
// (":modes shas STATUS\0path\0", or for renames/copies ":... R100\0old\0new\0") followed by
// numstat entries ("add\tdel\tpath\0", or "add\tdel\t\0old\0new\0"; "-" counts mean binary).
func parseDiff(section string) []FileChange {
	tokens := strings.Split(section, "\x00")
	files := []FileChange{}
	numstat := map[string][2]string{}
	for i := 0; i < len(tokens); {
		tok := strings.TrimLeft(tokens[i], "\n")
		switch {
		case tok == "":
			i++
		case strings.HasPrefix(tok, ":"):
			fields := strings.Split(tok, " ")
			status := fields[len(fields)-1]
			switch {
			case status == "":
				i++
			case (status[0] == 'R' || status[0] == 'C') && i+2 < len(tokens):
				similarity, _ := strconv.Atoi(status[1:])
				files = append(files, FileChange{Path: tokens[i+2], OldPath: tokens[i+1], Status: status[:1], Similarity: &similarity})
				i += 3
			case i+1 < len(tokens):
				files = append(files, FileChange{Path: tokens[i+1], Status: status[:1]})
				i += 2
			default:
				i++
			}
		default:
			parts := strings.SplitN(tok, "\t", 3)
			switch {
			case len(parts) < 3:
				i++
			case parts[2] != "":
				numstat[parts[2]] = [2]string{parts[0], parts[1]}
				i++
			case i+2 < len(tokens): // rename or copy: the new path is the second of the two
				numstat[tokens[i+2]] = [2]string{parts[0], parts[1]}
				i += 3
			default:
				i++
			}
		}
	}
	for k := range files {
		counts, ok := numstat[files[k].Path]
		if !ok {
			counts = [2]string{"-", "-"}
		}
		files[k].Binary = counts[0] == "-"
		files[k].Additions = countPtr(counts[0])
		files[k].Deletions = countPtr(counts[1])
	}
	return files
}

func countPtr(s string) *int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &n
}

// patchIDs maps each commit to `git patch-id --stable`, which is equal for the same change
// rebased or cherry-picked onto another base. Merges and empty commits get no id.
func patchIDs(ctx context.Context, path string, shas []string) (map[string]string, error) {
	ids := map[string]string{}
	if len(shas) == 0 {
		return ids, nil
	}
	logCmd := exec.CommandContext(ctx, "git", "--git-dir", path, "log", "--no-walk=unsorted", "--stdin", "-p",
		"--format=commit %H", "--diff-merges=off", "--no-color", "--no-ext-diff", "--no-textconv")
	idCmd := exec.CommandContext(ctx, "git", "--git-dir", path, "patch-id", "--stable")
	logCmd.Env, idCmd.Env = baseGitEnv, baseGitEnv
	logCmd.Stdin = strings.NewReader(strings.Join(shas, "\n"))
	pipe, err := logCmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var out, logErr, idErr bytes.Buffer
	idCmd.Stdin, idCmd.Stdout, idCmd.Stderr = pipe, &out, &idErr
	logCmd.Stderr = &logErr
	if err := idCmd.Start(); err != nil {
		return nil, err
	}
	if err := logCmd.Run(); err != nil {
		_ = idCmd.Wait()
		return nil, fmt.Errorf("git log -p in %s: %w: %s", filepath.Base(path), err, truncate(logErr.String(), 500))
	}
	if err := idCmd.Wait(); err != nil {
		return nil, fmt.Errorf("git patch-id in %s: %w: %s", filepath.Base(path), err, truncate(idErr.String(), 500))
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			ids[f[1]] = f[0]
		}
	}
	return ids, nil
}

func existingCommits(ctx context.Context, path string, shas []string) (map[string]bool, error) {
	out, err := git(ctx, path, []byte(strings.Join(shas, "\n")), "cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		return nil, err
	}
	found := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if sha, kind, ok := strings.Cut(line, " "); ok && kind == "commit" {
			found[sha] = true
		}
	}
	return found, nil
}

func sortedInts(s []int) []int {
	out := slices.Clone(s)
	sort.Ints(out)
	return orEmpty(out)
}

// subtractSorted is the strings of a that are not in b, sorted.
func subtractSorted(a, b []string) []string {
	set := map[string]bool{}
	for _, s := range a {
		set[s] = true
	}
	for _, s := range b {
		delete(set, s)
	}
	return sortedKeys(set)
}

func mergeSorted(a, b []string) []string {
	set := map[string]bool{}
	for _, s := range a {
		set[s] = true
	}
	for _, s := range b {
		set[s] = true
	}
	return sortedKeys(set)
}
