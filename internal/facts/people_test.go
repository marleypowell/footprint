package facts

import (
	"testing"

	"github.com/marleypowell/footprint/internal/collect"
)

// Who wrote a commit, relative to the user: their identities first, then AI agents, bots, and
// everyone else.
func TestPeopleClass(t *testing.T) {
	p := newPeople(collect.IdentityConfig{Emails: []string{"Jane@Example.com", "jdoe@users.noreply.github.com"}, Names: []string{"Jane Doe", "jdoe"}})
	for _, tc := range []struct{ name, email, want string }{
		// the user, by any configured email or name, in any case
		{"Someone", "jane@example.com", Self},
		{"Someone", "JANE@EXAMPLE.COM", Self},
		{"jdoe", "", Self},
		{"JANE DOE", "laptop@localhost", Self},
		// the user's identity wins over a pattern: their config says so
		{"Claude", "jdoe@users.noreply.github.com", Self},

		// AI coding agents, by email or by a name that is the agent's alone
		{"Anyone", "noreply@anthropic.com", Agent},
		{"Claude", "claude@example.com", Agent},
		{"Copilot", "", Agent},
		{"copilot-swe-agent[bot]", "", Agent},
		{"devin-ai-integration[bot]", "", Agent},
		{"cursoragent", "", Agent},
		{"Someone", "198982749+Copilot@users.noreply.github.com", Agent},
		{"Claude Shannon", "claude.shannon@example.com", Colleague}, // a person who shares the name

		// automation
		{"dependabot[bot]", "", Bot},
		{"renovate", "", Bot},
		{"GitHub", "noreply@github.com", Bot},
		{"github-actions", "", Bot},
		{"Project Collection Build Service (acme)", "", Bot},
		{"ci", "49699333+ci[bot]@users.noreply.github.com", Bot},

		{"Other Person", "other@example.com", Colleague},
		{"", "", Colleague},
	} {
		if got := p.class(tc.name, tc.email); got != tc.want {
			t.Errorf("class(%q, %q) = %s, want %s", tc.name, tc.email, got, tc.want)
		}
	}
}
