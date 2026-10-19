// Package jira is the issue tracker adapter for Jira Cloud: PROJ-123 keys of the site's projects,
// found case-insensitively in commit messages, branch names and pull requests, and the REST API (v3)
// for the projects and issues themselves.
package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/marleypowell/footprint/internal/atlassian"
	"github.com/marleypowell/footprint/internal/tracker"
)

// Tracker finds keys of the site's projects, and loads issues from one Jira site. Only real
// projects' keys count: a bare KEY-123 pattern matches too much (UTF-8, ISO-8601, dotnet-8...).
type Tracker struct {
	site  string
	extra []string // project keys the API does not list

	once   sync.Once
	api    *atlassian.Client
	apiErr error

	projectsOnce sync.Once
	projects     []tracker.Project
	keys         map[string]bool
	projectsErr  error
}

var _ tracker.Tracker = (*Tracker)(nil)

type settings struct {
	// Site is the Jira Cloud site, e.g. "example.atlassian.net".
	Site string `json:"site"`
	// ExtraProjectKeys are keys that issues were moved out of, in projects since deleted: the API
	// lists live and archived projects, with their previous keys, but not these.
	ExtraProjectKeys []string `json:"extra_project_keys"`
}

// New returns the adapter. settings is the issue_tracker config section.
func New(raw json.RawMessage) (*Tracker, error) {
	var s settings
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("jira settings: %w", err)
		}
	}
	t := &Tracker{site: s.Site}
	for _, k := range s.ExtraProjectKeys {
		t.extra = append(t.extra, strings.ToUpper(k))
	}
	return t, nil
}

// Projects lists every project the account can browse, live or archived, sorted by key.
func (t *Tracker) Projects(ctx context.Context) ([]tracker.Project, error) {
	t.projectsOnce.Do(func() { t.projects, t.projectsErr = t.loadProjects(ctx) })
	return t.projects, t.projectsErr
}

func (t *Tracker) loadProjects(ctx context.Context) ([]tracker.Project, error) {
	api, err := t.client(ctx)
	if err != nil {
		return nil, err
	}
	type project struct {
		Key            string   `json:"key"`
		Name           string   `json:"name"`
		ProjectTypeKey string   `json:"projectTypeKey"`
		Archived       bool     `json:"archived"`
		ProjectKeys    []string `json:"projectKeys"` // every key the project has had, current included
	}
	raw, err := allPages[project](ctx, api, "/rest/api/3/project/search", "values", 100,
		url.Values{"status": {"live", "archived"}, "expand": {"projectKeys"}})
	if err != nil {
		return nil, err
	}
	projects := make([]tracker.Project, 0, len(raw))
	t.keys = map[string]bool{}
	for _, p := range raw {
		previous := []string{}
		for _, k := range p.ProjectKeys {
			if k != p.Key {
				previous = append(previous, k)
			}
			t.keys[strings.ToUpper(k)] = true
		}
		slices.Sort(previous)
		t.keys[strings.ToUpper(p.Key)] = true
		projects = append(projects, tracker.Project{Key: p.Key, Name: p.Name, Type: p.ProjectTypeKey,
			Archived: p.Archived, PreviousKeys: previous})
	}
	if len(projects) == 0 {
		return nil, errors.New("jira: the account can browse no projects, so no issue keys would be found")
	}
	for _, k := range t.extra {
		t.keys[k] = true
	}
	slices.SortFunc(projects, func(a, b tracker.Project) int { return strings.Compare(a.Key, b.Key) })
	return projects, nil
}

func (t *Tracker) Keys(ctx context.Context) (tracker.KeyFunc, error) {
	if _, err := t.Projects(ctx); err != nil {
		return nil, err
	}
	return t.findKeys, nil
}

// client returns the API client, connecting on first use.
func (t *Tracker) client(ctx context.Context) (*atlassian.Client, error) {
	t.once.Do(func() {
		if t.site == "" {
			t.apiErr = errors.New(`jira: issue_tracker.site is not set (e.g. "example.atlassian.net")`)
			return
		}
		t.api, t.apiErr = atlassian.Connect(ctx, t.site, atlassian.Jira)
	})
	return t.api, t.apiErr
}

func (t *Tracker) Account(ctx context.Context) (tracker.Account, error) {
	api, err := t.client(ctx)
	if err != nil {
		return tracker.Account{}, err
	}
	var me struct {
		AccountID    string `json:"accountId"`
		DisplayName  string `json:"displayName"`
		EmailAddress string `json:"emailAddress"`
	}
	if err := api.Get(ctx, "/rest/api/3/myself", nil, &me); err != nil {
		return tracker.Account{}, err
	}
	return tracker.Account{ID: me.AccountID, Name: me.DisplayName, Email: me.EmailAddress}, nil
}

// Assigned searches with JQL's WAS operator, which matches the current and every past assignee.
func (t *Tracker) Assigned(ctx context.Context, account tracker.Account) ([]string, error) {
	if account.ID == "" {
		return nil, errors.New("jira: the account has no ID")
	}
	api, err := t.client(ctx)
	if err != nil {
		return nil, err
	}
	quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(account.ID)
	body := map[string]any{
		"jql":    `assignee WAS "` + quoted + `" ORDER BY created ASC`,
		"fields": []string{"key"}, "maxResults": 5000, // Jira pages ids and keys the largest
		"includeArchivedProjects": true,
	}
	keys := []string{}
	for {
		var page struct {
			Issues []struct {
				ID  string `json:"id"`
				Key string `json:"key"`
			} `json:"issues"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := api.Post(ctx, "/rest/api/3/search/jql", body, &page); err != nil {
			return nil, err
		}
		for _, issue := range page.Issues {
			if issue.Key == "" {
				return nil, fmt.Errorf("jira: the search returned issue %s without its key", issue.ID)
			}
			keys = append(keys, issue.Key)
		}
		if page.NextPageToken == "" { // no token: the last page, whatever its size
			return keys, nil
		}
		body["nextPageToken"] = page.NextPageToken
	}
}

var (
	// Jira issue numbers never have leading zeros, which also rules out analyzer IDs like ACME0901.
	keyRE       = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]{1,9}-[1-9][0-9]{0,5}`)
	keyNoDashRE = regexp.MustCompile(`[A-Za-z]{4,10}[1-9][0-9]{2,5}`) // "SHOP3481" typos
	guidRE      = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
)

func (t *Tracker) findKeys(texts ...string) []string {
	found := map[string]bool{}
	add := func(project, number string) {
		if project = strings.ToUpper(project); t.keys[project] {
			found[project+"-"+number] = true
		}
	}
	for _, text := range texts {
		text = guidRE.ReplaceAllString(text, " ")
		for _, m := range findBounded(keyRE, text) {
			i := strings.IndexByte(m, '-')
			add(m[:i], m[i+1:])
		}
		for _, m := range findBounded(keyNoDashRE, text) {
			i := strings.IndexAny(m, "123456789")
			add(m[:i], m[i:])
		}
	}
	keys := make([]string, 0, len(found))
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// findBounded returns the matches of re that are not preceded by an ASCII letter or digit and not
// followed by a digit: the lookarounds RE2 lacks. A rejected match resumes the search one byte
// later, as a backtracking engine would.
func findBounded(re *regexp.Regexp, text string) []string {
	var out []string
	for pos := 0; pos < len(text); {
		loc := re.FindStringIndex(text[pos:])
		if loc == nil {
			break
		}
		start, end := pos+loc[0], pos+loc[1]
		if (start > 0 && isASCIIAlnum(text[start-1])) || (end < len(text) && isDigit(text[end])) {
			pos = start + 1
			continue
		}
		out = append(out, text[start:end])
		pos = end
	}
	return out
}

func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isASCIIAlnum(c byte) bool { return isDigit(c) || (c|0x20 >= 'a' && c|0x20 <= 'z') }
