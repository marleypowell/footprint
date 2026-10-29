// Package github is the code host adapter for GitHub: GraphQL and REST calls made with the gh
// CLI's token, and GitHub's URL, ref and merge-message conventions (plus the merge messages of
// Azure DevOps, whose repos are imported into GitHub with their history).
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/marleypowell/footprint/internal/codehost"
)

// Host is one GitHub owner: an organization, or a user account (for a dataset of the user's own
// projects). It takes no settings yet (config: {"kind": "github"}).
type Host struct {
	org string // the owner's login, organization or user

	once   sync.Once
	api    *client
	apiErr error

	ownerOnce sync.Once
	qualifier string // "org" or "user": how a search names the owner
	ownerErr  error
}

const ownerQuery = `query($login: String!) { repositoryOwner(login: $login) { __typename } }`

// scope is the search qualifier for the owner's repositories: org:<login> for an organization,
// user:<login> for a user. The owner's type is asked once.
func (h *Host) scope(ctx context.Context, api *client) (string, error) {
	h.ownerOnce.Do(func() {
		var data struct {
			Owner *struct {
				Typename string `json:"__typename"`
			} `json:"repositoryOwner"`
		}
		if h.ownerErr = api.graphql(ctx, ownerQuery, map[string]any{"login": h.org}, &data); h.ownerErr != nil {
			return
		}
		switch {
		case data.Owner == nil:
			h.ownerErr = fmt.Errorf("github: %s is no organization or user this token can see", h.org)
		case data.Owner.Typename == "User":
			h.qualifier = "user"
		default:
			h.qualifier = "org"
		}
	})
	if h.ownerErr != nil {
		return "", h.ownerErr
	}
	return h.qualifier + ":" + h.org, nil
}

var _ codehost.Host = (*Host)(nil)

// New returns the adapter for org. settings is the code_host config section; it is checked to
// be an object but has no fields yet.
func New(org string, settings json.RawMessage) (*Host, error) {
	if org == "" {
		return nil, errors.New("github: org is required")
	}
	if len(settings) > 0 {
		var s map[string]any
		if err := json.Unmarshal(settings, &s); err != nil {
			return nil, fmt.Errorf("github settings: %w", err)
		}
	}
	return &Host{org: org}, nil
}

// client returns the API client, created on first use so the conventions need no token.
func (h *Host) client(ctx context.Context) (*client, error) {
	h.once.Do(func() { h.api, h.apiErr = newClient(ctx) })
	return h.api, h.apiErr
}

func (h *Host) Remote(repo codehost.Repo) codehost.Remote {
	return codehost.Remote{
		URL:                "https://github.com/" + repo.FullName + ".git",
		PullRequestRefspec: "+refs/pull/*/head:refs/pull/*/head",
		GitConfig: []codehost.GitSetting{
			// Drop whatever helper the user's git config names (possibly a stale path), then use gh.
			{Key: "credential.https://github.com.helper", Value: ""},
			{Key: "credential.https://github.com.helper", Value: "!gh auth git-credential"},
		},
	}
}

func (h *Host) CommitURL(repo codehost.Repo, sha string) string { return repo.URL + "/commit/" + sha }

func (h *Host) FileURL(repo codehost.Repo, branch, path string) string {
	return repo.URL + "/blob/" + escapePath(branch) + "/" + escapePath(path)
}

// escapePath escapes each segment of a slash-separated path for a URL.
func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

var pullRequestInMessage = []*regexp.Regexp{
	regexp.MustCompile(`(?m)\(#(\d+)\)\s*$`),               // squash merge: "Title (#123)"
	regexp.MustCompile(`(?m)^Merge pull request #(\d+)\b`), // merge commit
}

// migratedPullRequestInMessage finds the merges of hosts whose repos are imported into GitHub
// with their history.
var migratedPullRequestInMessage = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^Merged PR (\d+):`), // Azure DevOps: "Merged PR 123: title"
}

func (h *Host) PullRequestNumbers(message string) []int {
	return numbersIn(message, pullRequestInMessage)
}

func (h *Host) MigratedPullRequestNumbers(message string) []int {
	return numbersIn(message, migratedPullRequestInMessage)
}

// numbersIn collects the numbers that the patterns' first group captures, sorted and without
// repeats.
func numbersIn(message string, patterns []*regexp.Regexp) []int {
	seen := map[int]bool{}
	for _, re := range patterns {
		for _, m := range re.FindAllStringSubmatch(message, -1) {
			if n, err := strconv.Atoi(m[1]); err == nil {
				seen[n] = true
			}
		}
	}
	numbers := make([]int, 0, len(seen))
	for n := range seen {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	return numbers
}

var (
	repoURLRE  = regexp.MustCompile(`(?i)github\.com[/:]([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)`)
	repoSlugRE = regexp.MustCompile(`^([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)$`)
)

func (h *Host) RepoPath(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if m := repoURLRE.FindStringSubmatch(ref); m != nil {
		return m[1] + "/" + strings.TrimSuffix(m[2], ".git"), true
	}
	if m := repoSlugRE.FindStringSubmatch(ref); m != nil {
		return m[1] + "/" + m[2], true
	}
	return "", false
}

func (h *Host) Account(ctx context.Context) (codehost.Account, error) {
	api, err := h.client(ctx)
	if err != nil {
		return codehost.Account{}, err
	}
	var me struct {
		Login     string  `json:"login"`
		ID        int64   `json:"id"`
		Name      *string `json:"name"`
		CreatedAt string  `json:"created_at"`
	}
	if err := api.get(ctx, "/user", nil, &me); err != nil {
		return codehost.Account{}, err
	}
	account := codehost.Account{Login: me.Login, ID: me.ID, CreatedAt: me.CreatedAt}
	if me.Name != nil {
		account.Name = *me.Name
	}
	login := strings.ToLower(me.Login)
	account.NoReplyEmails = []string{ // web edits, with and (older accounts) without the id prefix
		fmt.Sprintf("%d+%s@users.noreply.github.com", me.ID, login),
		login + "@users.noreply.github.com",
	}
	return account, nil
}
