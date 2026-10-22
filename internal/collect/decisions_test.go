package collect

import "testing"

func TestDecisionPaths(t *testing.T) {
	for p, want := range map[string]bool{
		"docs/decisions/0001-use-queues.md": true, "doc/adr/0002-x.md": true, "apps/web/docs/decisions/0001-y.md": true,
		"docs/architecture-decisions/a.md": true, "ADR-007-cache.md": true, "docs/adr12.md": true, "ADRs/x.MD": true,
		"docs/decisions/README.md": false, "docs/adr/template.md": false, "docs/decisions/adr-template.md": false,
		"docs/decisions/img/flow.png": false, "docs/decisions/sub/0001-z.md": false, "docs/decision.md": false, "src/adrian.md": false,
	} {
		if got := isDecision(p); got != want {
			t.Errorf("isDecision(%q) = %v, want %v", p, got, want)
		}
	}
}
