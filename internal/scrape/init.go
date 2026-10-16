package scrape

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/marleypowell/footprint"
	"github.com/marleypowell/footprint/internal/codehost"
)

// identitySample is how many attributed commits init reads to find the user's commit emails.
const identitySample = 600

// Init writes the config file for the user authenticated with host. Commit emails come from the
// commits the host already attributes to the account in the org.
func Init(ctx context.Context, opts Options, host codehost.Host) error {
	if fileExists(opts.ConfigPath) && !opts.Force {
		return fmt.Errorf("%s already exists; pass --force to overwrite it", opts.ConfigPath)
	}
	if opts.Org == "" {
		return errors.New("init needs --org <org>")
	}
	account, err := host.Account(ctx)
	if err != nil {
		return err
	}
	sample, err := host.AttributedCommits(ctx, account.Login, identitySample)
	if err != nil {
		return err
	}

	type seenKey struct{ name, email string }
	counts := map[seenKey]int{}
	var order []seenKey
	for _, c := range sample {
		k := seenKey{c.AuthorName, strings.ToLower(c.AuthorEmail)}
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
	}
	emails := map[string]bool{}
	for _, e := range account.NoReplyEmails {
		emails[strings.ToLower(e)] = true
	}
	names := map[string]bool{account.Login: true}
	for _, k := range order {
		emails[k.email] = true
		if n := strings.TrimSpace(k.name); strings.Contains(n, " ") {
			names[n] = true
		}
	}
	if n := strings.TrimSpace(account.Name); strings.Contains(n, " ") {
		names[n] = true
	}
	tokens := map[string]bool{strings.ToLower(account.Login): true}
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
	if !strings.EqualFold(opts.Org, cfg.Org) { // the example's catalog and tracker settings are for its org only
		cfg.Catalog = Tool{Kind: cfg.Catalog.Kind}
		cfg.IssueTracker = Tool{Kind: cfg.IssueTracker.Kind}
	}
	cfg.Org, cfg.Login = opts.Org, account.Login
	if opts.CodeHost != cfg.CodeHost.Kind {
		cfg.CodeHost = Tool{Kind: opts.CodeHost}
	}
	cfg.Identity = IdentityConfig{Emails: sortedKeys(emails), Names: sortedKeys(names), DiscoveryPattern: strings.Join(pattern, "|")}
	if err := writeJSON(opts.ConfigPath, cfg); err != nil {
		return err
	}

	logf("init: wrote %s for %s in %s", opts.ConfigPath, account.Login, opts.Org)
	sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })
	for _, k := range order {
		logf("init:   %4d commits as %s <%s>", counts[k], k.name, k.email)
	}
	if len(order) == 0 {
		logf("init: the code host attributes no commits in this org to the account; add your commit emails by hand")
	}
	logf("init: review identity.names (drop any you share with a colleague), then run `all`; the identities " +
		"and verify stages report anything still missed")
	return nil
}
