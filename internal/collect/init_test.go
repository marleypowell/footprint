package collect

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/marleypowell/footprint"
	"github.com/marleypowell/footprint/internal/codehost"
)

// accountHost is the fake code host signed in as another account.
type accountHost struct {
	*fakeHost
	account codehost.Account
}

func (h accountHost) Account(context.Context) (codehost.Account, error) { return h.account, nil }

func TestInit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	host := &fakeHost{attributed: []codehost.AttributedCommit{
		{AuthorName: "Jane Doe", AuthorEmail: "Jane.Doe@Example.com"},
		{AuthorName: "jane", AuthorEmail: "jane@laptop.local"},
	}}
	opts := Options{ConfigPath: path, Org: "org", CodeHost: "github"}
	if err := Init(context.Background(), opts, host); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Login != "jdoe" || cfg.Org != "org" {
		t.Errorf("login/org: %q %q", cfg.Login, cfg.Org)
	}
	if want := []string{"7-jdoe@users.noreply.code.example", "jane.doe@example.com", "jane@laptop.local"}; !reflect.DeepEqual(cfg.Identity.Emails, want) {
		t.Errorf("emails = %v, want %v", cfg.Identity.Emails, want)
	}
	if want := []string{"Jane Doe", "jdoe"}; !reflect.DeepEqual(cfg.Identity.Names, want) {
		t.Errorf("names = %v, want %v (single-word names are too ambiguous)", cfg.Identity.Names, want)
	}
	if cfg.Identity.DiscoveryPattern != "doe|jane|jdoe" {
		t.Errorf("pattern = %q", cfg.Identity.DiscoveryPattern)
	}
	checkKindsOnly := func(cfg *Config) {
		t.Helper()
		if string(cfg.Catalog.Settings) != `{"kind":"backstage"}` || cfg.IssueTracker == nil || string(cfg.IssueTracker.Settings) != `{"kind":"jira"}` ||
			cfg.Wiki == nil || string(cfg.Wiki.Settings) != `{"kind":"confluence"}` {
			got, _ := json.Marshal(cfg)
			t.Errorf("init --org %s should keep only the tool kinds: %s", cfg.Org, got)
		}
	}
	checkKindsOnly(cfg)
	if err := Init(context.Background(), opts, host); err == nil {
		t.Error("init should refuse to overwrite without --force")
	}

	// The example's own org gets the kinds only too: its settings are placeholders.
	var example Config
	if err := json.Unmarshal(footprint.ExampleConfig, &example); err != nil {
		t.Fatal(err)
	}
	opts.Org, opts.Force = example.Org, true
	if err := Init(context.Background(), opts, host); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadConfig(path); err != nil {
		t.Fatal(err)
	}
	checkKindsOnly(cfg)
}

// The identity comes from what the code host attributes to the account: every email it links
// commits to, every name with a space in it (a single word is too often a colleague's too), and a
// discovery pattern of the name parts long enough to be telling.
func TestInitIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	host := accountHost{
		account: codehost.Account{Login: "j.doe", Name: " Jane Q. Doe ", NoReplyEmails: []string{"7+J.Doe@users.noreply.code.example"}},
		fakeHost: &fakeHost{attributed: []codehost.AttributedCommit{
			{AuthorName: "Jane Doe", AuthorEmail: "JANE@Example.com"},
			{AuthorName: "Jane Doe", AuthorEmail: "jane@example.com"}, // the same, once lower-cased
			{AuthorName: "build", AuthorEmail: "ci@example.com"},      // the host says it is hers: the email counts, the name does not
		}},
	}
	if err := Init(context.Background(), Options{ConfigPath: path, Org: "acme", CodeHost: "github"}, host); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"7+j.doe@users.noreply.code.example", "ci@example.com", "jane@example.com"}; !reflect.DeepEqual(cfg.Identity.Emails, want) {
		t.Errorf("emails = %q, want %q (lower-cased, de-duplicated, sorted)", cfg.Identity.Emails, want)
	}
	if want := []string{"Jane Doe", "Jane Q. Doe", "j.doe"}; !reflect.DeepEqual(cfg.Identity.Names, want) {
		t.Errorf("names = %q, want %q (the login, and the account's names with a space, trimmed)", cfg.Identity.Names, want)
	}
	// The login is one token, its dot escaped; "Q" is too short to tell anyone apart.
	if want := `doe|j\.doe|jane`; cfg.Identity.DiscoveryPattern != want {
		t.Errorf("discovery_pattern = %q, want %q", cfg.Identity.DiscoveryPattern, want)
	}
}

// With no attributed commits, init still writes a config, from the account alone.
func TestInitWithoutAttributedCommits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Init(context.Background(), Options{ConfigPath: path, Org: "acme", CodeHost: "github"}, &fakeHost{}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	got := []any{cfg.Identity.Emails, cfg.Identity.Names, cfg.Identity.DiscoveryPattern}
	if want := []any{[]string{"7-jdoe@users.noreply.code.example"}, []string{"Jane Doe", "jdoe"}, "doe|jane|jdoe"}; !reflect.DeepEqual(got, want) {
		t.Errorf("identity = %q, want %q", got, want)
	}
}

func TestInitNeedsAnOrg(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Init(context.Background(), Options{ConfigPath: path, CodeHost: "github"}, &fakeHost{}); err == nil || !strings.Contains(err.Error(), "--org") {
		t.Errorf("Init without an org = %v, want a request for --org", err)
	}
	if fileExists(path) {
		t.Error("Init without an org wrote a config")
	}
}

// A code host other than the example's replaces its section; the same one keeps it as written.
func TestInitCodeHostKind(t *testing.T) {
	var example Config
	if err := json.Unmarshal(footprint.ExampleConfig, &example); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{example.CodeHost.Kind, "otherhost"} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := Init(context.Background(), Options{ConfigPath: path, Org: "acme", CodeHost: kind}, &fakeHost{}); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		want := `{"kind":"` + kind + `"}`
		if kind == example.CodeHost.Kind {
			want = string(example.CodeHost.Settings)
		}
		if got := string(cfg.CodeHost.Settings); got != want {
			t.Errorf("init --code-host %s: code_host = %s, want %s", kind, got, want)
		}
	}
}
