package scrape

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
	"unicode"
	"unicode/utf8"

	"github.com/marleypowell/footprint"
)

// runInit writes the config file for the gh-authenticated user. Commit emails come from the commits
// GitHub already attributes to the account in the org, so no `user:email` token scope is needed.
func runInit(ctx context.Context, opts Options) error {
	if fileExists(opts.ConfigPath) && !opts.Force {
		return fmt.Errorf("%s already exists; pass --force to overwrite it", opts.ConfigPath)
	}
	if opts.Org == "" {
		return errors.New("init needs --org <github-org>")
	}
	gh, err := newGitHubClient(ctx)
	if err != nil {
		return err
	}
	var me struct {
		Login     string  `json:"login"`
		ID        int64   `json:"id"`
		Name      *string `json:"name"`
		CreatedAt string  `json:"created_at"`
	}
	if err := gh.get(ctx, "/user", nil, &me); err != nil {
		return err
	}

	type seenKey struct{ name, email string }
	counts := map[seenKey]int{}
	var order []seenKey
	for _, direction := range []string{"desc", "asc"} { // newest and oldest commits, where email changes show up
		for page := 1; page <= 3; page++ {
			res, err := gh.searchCommits(ctx, url.Values{
				"q": {fmt.Sprintf("author:%s org:%s", me.Login, opts.Org)}, "sort": {"author-date"},
				"order": {direction}, "per_page": {"100"}, "page": {strconv.Itoa(page)},
			})
			if err != nil {
				return err
			}
			for _, it := range res.Items {
				k := seenKey{it.Commit.Author.Name, strings.ToLower(it.Commit.Author.Email)}
				if counts[k] == 0 {
					order = append(order, k)
				}
				counts[k]++
			}
			if len(res.Items) < 100 {
				break
			}
		}
	}

	emails := map[string]bool{strings.ToLower(fmt.Sprintf("%d+%s@users.noreply.github.com", me.ID, me.Login)): true}
	names := map[string]bool{me.Login: true}
	for _, k := range order {
		emails[k.email] = true
		if n := strings.TrimSpace(k.name); strings.Contains(n, " ") {
			names[n] = true
		}
	}
	if n := strings.TrimSpace(deref(me.Name)); strings.Contains(n, " ") {
		names[n] = true
	}
	tokens := map[string]bool{strings.ToLower(me.Login): true}
	for n := range names {
		for _, t := range strings.FieldsFunc(n, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' }) {
			if utf8.RuneCountInString(t) >= 3 {
				tokens[strings.ToLower(t)] = true
			}
		}
	}
	pattern := sortedKeys(tokens)
	for i, t := range pattern {
		pattern[i] = regexp.QuoteMeta(t)
	}

	var cfg Config
	if err := json.Unmarshal(footprint.ExampleConfig, &cfg); err != nil {
		return fmt.Errorf("embedded config.example.json: %w", err)
	}
	if !strings.EqualFold(opts.Org, cfg.Org) { // the example's catalog and Jira settings are for its org only
		cfg.CatalogHubRepos = []string{}
		cfg.Jira = JiraConfig{ExtraProjectKeys: []string{}}
	}
	cfg.Org, cfg.GitHubLogin, cfg.GitHubUserID = opts.Org, me.Login, me.ID
	if year, err := strconv.Atoi(truncate(me.CreatedAt, 4)); err == nil {
		cfg.SearchStartYear = year // nobody authored a PR before their account existed
	}
	cfg.Identity = IdentityConfig{Emails: sortedKeys(emails), Names: sortedKeys(names), DiscoveryPattern: strings.Join(pattern, "|")}
	if err := writeJSON(opts.ConfigPath, cfg); err != nil {
		return err
	}

	logf("init: wrote %s for %s in %s", opts.ConfigPath, me.Login, opts.Org)
	sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })
	for _, k := range order {
		logf("init:   %4d commits as %s <%s>", counts[k], k.name, k.email)
	}
	if len(order) == 0 {
		logf("init: GitHub attributes no commits in this org to the account; add your commit emails by hand")
	}
	logf("init: review identity.names (drop any you share with a colleague), then run `all`; the identities " +
		"and verify stages report anything still missed")
	return nil
}
