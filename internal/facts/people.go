package facts

import (
	"regexp"
	"strings"

	"github.com/marleypowell/footprint/internal/collect"
)

// Author classes: who wrote a commit, relative to the user.
const (
	Self      = "self"
	Agent     = "agent"     // an AI coding agent (Claude, Copilot), driven by whoever owns the PR
	Bot       = "bot"       // automation: dependency updates, CI, PR completion services
	Colleague = "colleague" // anyone else
	CoAuthor  = "co-authored"
)

var (
	agentEmails = map[string]bool{"noreply@anthropic.com": true}
	agentRE     = regexp.MustCompile(`(?i)^(claude|copilot|copilot-swe-agent(\[bot\])?|devin-ai-integration\[bot\]|cursor(agent)?)$|\+copilot@users\.noreply\.github\.com$`)
	botRE       = regexp.MustCompile(`(?i)\[bot\]$|^(github|dependabot|renovate|github-actions)$|build service\b`)
)

// people tells the user's own identities from agents, bots and colleagues.
type people struct {
	emails map[string]bool
	names  map[string]bool
}

func newPeople(id collect.IdentityConfig) *people {
	p := &people{emails: map[string]bool{}, names: map[string]bool{}}
	for _, e := range id.Emails {
		p.emails[strings.ToLower(e)] = true
	}
	for _, n := range id.Names {
		p.names[strings.ToLower(n)] = true
	}
	return p
}

// class is the author class of name <email>.
func (p *people) class(name, email string) string {
	switch email = strings.ToLower(email); {
	case p.emails[email] || p.names[strings.ToLower(name)]:
		return Self
	case agentEmails[email] || agentRE.MatchString(name) || agentRE.MatchString(email):
		return Agent
	case botRE.MatchString(name) || strings.HasSuffix(email, "[bot]@users.noreply.github.com"):
		return Bot
	}
	return Colleague
}
