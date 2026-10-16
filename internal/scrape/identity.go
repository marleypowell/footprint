package scrape

import (
	"fmt"
	"regexp"
	"strings"
)

// identity decides whether a git (name, email) pair is the user.
type identity struct {
	emails    map[string]bool
	names     map[string]bool
	noreply   *regexp.Regexp
	discovery *regexp.Regexp
	pattern   string // the discovery pattern as written, for `git log -E --grep`
}

func newIdentity(cfg *Config) (*identity, error) {
	id := &identity{emails: map[string]bool{}, names: map[string]bool{}, pattern: cfg.Identity.DiscoveryPattern}
	for _, e := range cfg.Identity.Emails {
		id.emails[strings.ToLower(strings.TrimSpace(e))] = true
	}
	for _, n := range cfg.Identity.Names {
		id.names[strings.ToLower(strings.TrimSpace(n))] = true
	}
	// GitHub's noreply address, with or without the numeric id prefix.
	id.noreply = regexp.MustCompile(`(?i)^(\d+\+)?` + regexp.QuoteMeta(cfg.GitHubLogin) + `@users\.noreply\.github\.com$`)
	if id.pattern == "" {
		id.pattern = regexp.QuoteMeta(cfg.GitHubLogin)
	}
	discovery, err := regexp.Compile("(?i)" + id.pattern)
	if err != nil {
		return nil, fmt.Errorf("identity.discovery_pattern: %w", err)
	}
	id.discovery = discovery
	return id, nil
}

// match says how a (name, email) pair matches the configured identity: "email", "name", or "".
func (id *identity) match(name, email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if id.emails[email] || id.noreply.MatchString(email) {
		return "email"
	}
	if id.names[strings.ToLower(strings.TrimSpace(name))] {
		return "name"
	}
	return ""
}

// resembles reports identities worth listing in the identities report: matched, or matching
// the discovery pattern.
func (id *identity) resembles(name, email string) bool {
	return id.discovery.MatchString(name) || id.discovery.MatchString(email) || id.match(name, email) != ""
}

// Person is a name/email pair without a date (Co-authored-by trailers).
type Person struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

var (
	coauthorRE  = regexp.MustCompile(`(?im)^\s*co-authored-by:\s*(.+?)\s*$`)
	nameEmailRE = regexp.MustCompile(`^(.*?)\s*<([^>]*)>$`)
)

// parseCoauthors returns every Co-authored-by trailer in a message, wherever it sits.
func parseCoauthors(message string) []Person {
	people := []Person{}
	for _, m := range coauthorRE.FindAllStringSubmatch(message, -1) {
		if ne := nameEmailRE.FindStringSubmatch(m[1]); ne != nil {
			people = append(people, Person{Name: strings.TrimSpace(ne[1]), Email: strings.TrimSpace(ne[2])})
		} else {
			people = append(people, Person{Name: m[1]})
		}
	}
	return people
}
