package collect

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The inventory lists the org's repos by name, ignoring case; repos whose names differ only in
// case keep the host's order.
func TestInventorySortsReposByName(t *testing.T) {
	host := &fakeHost{repos: []Repo{{Name: "web"}, {Name: "API"}, {Name: "billing"}, {Name: "api"}}}
	h := newTestHarness(t, Tools{Host: host})
	if err := h.inventory(context.Background()); err != nil {
		t.Fatal(err)
	}
	repos, err := h.loadRepos()
	var names []string
	for _, r := range repos {
		names = append(names, r.Name)
	}
	if want := []string{"API", "api", "billing", "web"}; err != nil || !reflect.DeepEqual(names, want) {
		t.Errorf("repos.json names = %q, %v, want %q", names, err, want)
	}
}

func TestInventoryOfAnEmptyOrg(t *testing.T) {
	h := newTestHarness(t, Tools{Host: &fakeHost{}})
	if err := h.inventory(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(h.raw, "org", "repos.json")); err != nil || string(got) != "[]\n" {
		t.Errorf("repos.json = %q, %v, want []", got, err)
	}
}
