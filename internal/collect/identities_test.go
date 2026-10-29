package collect

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

// The identities report lists every (name, email) variant that is the configured user or
// resembles them (the discovery pattern), with its commit counts per role across every repo, so
// variants the config misses show up as matched_by_config null.
func TestIdentities(t *testing.T) {
	api, web := newWorkRepo(t), newWorkRepo(t)
	api.write("a.txt", "1\n")
	api.commit(me, "one")
	api.write("b.txt", "2\n")
	api.commit(me, "two")
	api.write("c.txt", "3\n")
	api.commit(identityEnv("Jane Doe", "jane@laptop.local", "Other Person", "other@example.com"), "from the laptop")
	api.write("d.txt", "4\n")
	api.commit(other, "pairing\n\nCo-authored-by: J Doe <jdoe@old.example>")
	api.write("e.txt", "5\n")
	api.commit(other, "unrelated")
	web.write("a.txt", "1\n")
	web.commit(identityEnv("Jane Doe", "Jane.Doe@Example.com", "Jane Doe", "Jane.Doe@Example.com"), "same person, other case")

	h := newTestHarness(t, Tools{})
	writeInventory(t, h, testRepo("api"), testRepo("web"))
	api.mirrorTo(h.mirrorPath("api"))
	web.mirrorTo(h.mirrorPath("web"))
	if err := h.identities(context.Background()); err != nil {
		t.Fatal(err)
	}
	var report IdentitiesReport
	if ok, err := readJSON(filepath.Join(h.raw, "org", "identities.json"), &report); !ok || err != nil {
		t.Fatalf("identities.json: %v %v", ok, err)
	}
	want := []IdentityEntry{
		// emails differing only in case are one variant, named as first seen
		{Name: "Jane Doe", Email: "jane.doe@example.com", MatchedByConfig: ptr("email"), Author: 3, Committer: 3, Repos: []string{"api", "web"}},
		{Name: "Jane Doe", Email: "jane@laptop.local", MatchedByConfig: ptr("name"), Author: 1, Repos: []string{"api"}},
		{Name: "J Doe", Email: "jdoe@old.example", CoAuthor: 1, Repos: []string{"api"}}, // the config does not count it
	}
	if !reflect.DeepEqual(report.Identities, want) {
		t.Errorf("identities:\n got %s\nwant %s", mustJSON(t, report.Identities), mustJSON(t, want))
	}
	if !reflect.DeepEqual(report.Configured, h.cfg.Identity) {
		t.Errorf("configured = %+v, want the identity config the stage ran with", report.Configured)
	}
}

func TestIdentityCountsKeepFirstSeenOrder(t *testing.T) {
	c := &identityCounts{counts: map[identityKey]int{}}
	b, a := identityKey{"author", "B", "b@x"}, identityKey{"author", "A", "a@x"}
	for _, k := range []identityKey{b, a, b} {
		c.add(k)
	}
	if !reflect.DeepEqual(c.order, []identityKey{b, a}) || c.counts[b] != 2 || c.counts[a] != 1 {
		t.Errorf("identityCounts = %+v, want B (2) then A (1)", c)
	}
}
