package scrape

import (
	"context"
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/marleypowell/footprint/internal/catalog"
	"github.com/marleypowell/footprint/internal/codehost"
	"github.com/marleypowell/footprint/internal/tracker"
)

// In-memory adapters: the second adapter at each seam. They deliberately use another tool's
// conventions (GitLab-style hosting, Cortex-style descriptors, Linear-style keys) so the tests
// show the core assumes nothing about GitHub, Backstage or Jira.

type fakeHost struct {
	remotes    map[string]string // repo name -> local repository to fetch from
	repos      []codehost.Repo
	prs        []codehost.PullRequest
	attributed []codehost.AttributedCommit
}

var _ codehost.Host = (*fakeHost)(nil)

func (f *fakeHost) Account(context.Context) (codehost.Account, error) {
	return codehost.Account{Login: "jdoe", ID: 7, Name: "Jane Doe", NoReplyEmails: []string{"7-jdoe@users.noreply.code.example"}}, nil
}

func (f *fakeHost) Repos(context.Context) ([]codehost.Repo, error) { return f.repos, nil }

func (f *fakeHost) PullRequests(context.Context, string) ([]codehost.PullRequest, error) {
	return f.prs, nil
}

func (f *fakeHost) AttributedCommits(_ context.Context, _ string, limit int) ([]codehost.AttributedCommit, error) {
	if limit > 0 && limit < len(f.attributed) {
		return f.attributed[:limit], nil
	}
	return f.attributed, nil
}

func (f *fakeHost) Remote(repo codehost.Repo) codehost.Remote {
	return codehost.Remote{URL: f.remotes[repo.Name], PullRequestRefspec: "+refs/merge-requests/*/head:refs/pull/*/head"}
}

func (f *fakeHost) CommitURL(repo codehost.Repo, sha string) string {
	return repo.URL + "/-/commit/" + sha
}

var mergeRequestRE = regexp.MustCompile(`See merge request [\w./-]+!(\d+)`)

func (f *fakeHost) PullRequestNumbers(message string) []int {
	var numbers []int
	for _, m := range mergeRequestRE.FindAllStringSubmatch(message, -1) {
		n, _ := strconv.Atoi(m[1])
		numbers = append(numbers, n)
	}
	return numbers
}

func (f *fakeHost) RepoPath(ref string) (string, bool) {
	rest, ok := strings.CutPrefix(ref, "https://code.example/")
	if !ok {
		return "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}

// fakeCatalog reads cortex.json files: a list of {tag, category, title, description, repo, part_of}.
type fakeCatalog struct{}

func (fakeCatalog) Scan(ctx context.Context, src catalog.Source, resolve catalog.RepoResolver) (catalog.Result, error) {
	var res catalog.Result
	paths, err := src.List(ctx)
	if err != nil {
		return res, err
	}
	for _, p := range paths {
		if path.Base(p) != "cortex.json" {
			continue
		}
		content, err := src.Read(ctx, p)
		if err != nil {
			return res, err
		}
		res.Files = append(res.Files, catalog.File{Path: p, Content: content})
		var descriptors []struct {
			Tag, Category, Title, Description, Repo string
			PartOf                                  []string `json:"part_of"`
		}
		if err := json.Unmarshal(content, &descriptors); err != nil {
			res.Errors = append(res.Errors, catalog.ParseError{Path: p, Error: err.Error()})
			continue
		}
		for i, d := range descriptors {
			e := catalog.Entity{Ref: d.Tag, Kind: d.Category, Category: d.Category, Title: ptr(d.Title),
				Description: ptr(d.Description), FoundInRepo: src.Repo(), Path: p, DocIndex: i, PartOf: orEmpty(d.PartOf),
				RepoSignals: catalog.Signals{Links: []string{}}, Raw: json.RawMessage(`{"x-cortex-tag":"` + d.Tag + `"}`)}
			if repo, ok := resolve(d.Repo); ok {
				e.SourceRepo, e.SourceRepoBasis = &repo, ptr("x-cortex-git")
			}
			res.Entities = append(res.Entities, e)
		}
	}
	return res, nil
}

// fakeTracker finds Linear-style ENG-123 keys.
type fakeTracker struct{}

var linearKeyRE = regexp.MustCompile(`(?i)\beng-(\d+)\b`)

func (fakeTracker) Keys(texts ...string) []string {
	set := map[string]bool{}
	for _, text := range texts {
		for _, m := range linearKeyRE.FindAllStringSubmatch(text, -1) {
			set["ENG-"+m[1]] = true
		}
	}
	keys := []string{}
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (fakeTracker) Projects() []tracker.Project {
	return []tracker.Project{{Key: "ENG", Name: "Engineering", Type: "team"}}
}
