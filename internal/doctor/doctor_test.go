package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/marleypowell/footprint/internal/collect"
	"github.com/marleypowell/footprint/internal/tracker"
)

var (
	collected = time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	jira      = "linear"
	notion    = "notion"
	me        = collect.IdentityConfig{Emails: []string{"jane@acme.test"}, Names: []string{"Jane Doe"}, DiscoveryPattern: "jane|doe"}
)

// fixture is a complete dataset: every check passes.
type fixture struct {
	t     *testing.T
	in    Inputs
	index collect.Index
}

func newFixture(t *testing.T) *fixture {
	dir := t.TempDir()
	f := &fixture{t: t, in: Inputs{
		DataDir: filepath.Join(dir, "raw"), CacheDir: filepath.Join(dir, "cache"),
		Config: &collect.Config{Org: "acme", Login: "jdoe", Identity: me,
			IssueTracker: &collect.Tool{Kind: jira}, Wiki: &collect.Tool{Kind: notion}},
		Env:   []EnvNeed{{Tool: "linear", AnyOf: []string{"LINEAR_TOKEN"}}},
		IsSet: func(name string) bool { return name == "LINEAR_TOKEN" },
		Now:   collected.Add(24 * time.Hour),
	}}
	f.index = collect.Index{GeneratedAt: stamp(collected), Org: "acme", Login: "jdoe", Identity: me,
		Sources:     collect.Sources{CodeHost: "gitlab", Catalog: "cortex", IssueTracker: &jira, Wiki: &notion},
		WikiBySpace: []collect.IndexGroup{{Name: "ENG"}}}
	f.write("index.json", f.index, collected)
	f.write("repos/pay/commits.jsonl", collect.Commit{Repo: "pay", SHA: "c1"}, collected.Add(-time.Hour))
	f.write("repos/pay/prs.jsonl", map[string]any{"number": 1}, collected.Add(-time.Hour))
	f.write("org/verification.json", collect.Verification{GeneratedAt: stamp(collected), HostAttributedCommits: 1,
		Missing: []collect.MissingCommit{}}, collected)
	matched := "email"
	f.write("org/identities.json", collect.IdentitiesReport{GeneratedAt: stamp(collected), Configured: me,
		Identities: []collect.IdentityEntry{{Name: "Jane Doe", Email: "jane@acme.test", MatchedByConfig: &matched, Author: 3}}}, collected)
	f.write("org/repos.json", []collect.Repo{{Name: "pay"}, {Name: "empty", IsEmpty: true}}, collected)
	f.write("issues/unresolved.json", []collect.UnresolvedKey{}, collected)
	f.write("wiki/ENG.jsonl", map[string]any{"id": "1"}, collected)
	f.cache("mirror_status.json", map[string]collect.MirrorStatus{"pay": {Name: "pay", OK: true, FetchedAt: stamp(collected.Add(-2 * time.Hour))}})
	return f
}

func stamp(t time.Time) string { return t.Format("2006-01-02T15:04:05+00:00") }

func (f *fixture) write(rel string, v any, mtime time.Time) {
	f.t.Helper()
	path := filepath.Join(f.in.DataDir, filepath.FromSlash(rel))
	f.put(path, v)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) cache(rel string, v any) { f.put(filepath.Join(f.in.CacheDir, rel), v) }

func (f *fixture) put(path string, v any) {
	f.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) run() map[string]Check {
	f.t.Helper()
	r, err := Run(f.in)
	if err != nil {
		f.t.Fatal(err)
	}
	out := map[string]Check{}
	for _, c := range r.Checks {
		out[c.ID] = c
	}
	return out
}

func expect(t *testing.T, c Check, status Status, contains ...string) {
	t.Helper()
	all := strings.Join(append([]string{c.Summary, c.Fix, c.Snippet}, c.Details...), "\n")
	if c.Status != status {
		t.Errorf("%s: status %s, want %s:\n%s", c.ID, c.Status, status, all)
	}
	for _, s := range contains {
		if !strings.Contains(all, s) {
			t.Errorf("%s: no %q in:\n%s", c.ID, s, all)
		}
	}
}

func TestComplete(t *testing.T) {
	f := newFixture(t)
	r, err := Run(f.in)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range r.Checks {
		if c.Status != Pass {
			t.Errorf("%s: %s: %s %v", c.ID, c.Status, c.Summary, c.Details)
		}
	}
	if r.Failed() {
		t.Error("a complete dataset failed")
	}
	again, _ := Run(f.in)
	if !reflect.DeepEqual(r, again) {
		t.Error("two runs differ")
	}
}

func TestNoDataset(t *testing.T) {
	r, err := Run(Inputs{DataDir: t.TempDir(), Now: collected})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Checks) != 1 || !r.Failed() || r.Checks[0].Fix != "footprint all" {
		t.Errorf("report: %+v", r)
	}
}

func TestVerify(t *testing.T) {
	f := newFixture(t)
	f.write("org/verification.json", collect.Verification{GeneratedAt: stamp(collected), HostAttributedCommits: 5,
		AttributedMissingFromDataset: 3, Missing: []collect.MissingCommit{
			{Repo: "pay", SHA: "a", Author: "Jane D <jd@old.test>"},
			{Repo: "web-ui", SHA: "b", Author: "Jane D <jd@old.test>"},
			{Repo: "shop", SHA: "c", Author: "Jane Doe <JANE@acme.test>"},
		}}, collected)
	c := f.run()["verify"]
	expect(t, c, Fail, "3 of the 5", "jd@old.test>: 2 commits in pay, web-ui; the email is not in identity.emails",
		"the email is configured", "footprint mirror -only shop", "footprint commits projects verify issues")
	if !strings.Contains(c.Snippet, `"jane@acme.test",`) || !strings.Contains(c.Snippet, `"jd@old.test"`) || strings.Contains(c.Snippet, "JANE") {
		t.Errorf("snippet:\n%s", c.Snippet)
	}

	os.Remove(filepath.Join(f.in.DataDir, "org", "verification.json"))
	expect(t, f.run()["verify"], Fail, "footprint verify")
}

func TestIdentities(t *testing.T) {
	f := newFixture(t)
	matched := "email"
	f.write("org/identities.json", collect.IdentitiesReport{GeneratedAt: stamp(collected), Configured: me,
		Identities: []collect.IdentityEntry{
			{Name: "Jane Doe", Email: "jane@acme.test", MatchedByConfig: &matched},
			{Name: "jdoe", Email: "123+jdoe@users.noreply.example", Author: 4, Repos: []string{"pay"}},
			{Name: "Janet Doerr", Email: "janet@acme.test", Author: 9},
			{Name: "J. Doe", Email: "jd@laptop.local", Committer: 2},
			{Name: "Jane Doe", Email: "jane@new.test"}, // the config gained this email since
			{Name: "Doe Bot", Email: "bot@acme.test"},
		}}, collected)
	f.in.Config.Identity.Emails = append(f.in.Config.Identity.Emails, "jane@new.test")
	f.in.Config.Identity.NotMe = []string{"BOT@acme.test"}
	c := f.run()["identities"]
	expect(t, c, Warn, "3 identities", "123+jdoe@users.noreply.example>: author 4, committer 0, co-author 0, in 1 repos; the address names the login jdoe",
		"jd@laptop.local>: author 0, committer 2, co-author 0, in 0 repos; the same name as Jane Doe, with an initial",
		"Janet Doerr <janet@acme.test>", "identity.not_me")
	if strings.Contains(strings.Join(c.Details, "\n"), "bot@") || strings.Contains(strings.Join(c.Details, "\n"), "new.test") {
		t.Errorf("a dismissed or configured identity is listed: %v", c.Details)
	}
	if !strings.Contains(c.Snippet, "123+jdoe@users.noreply.example") || strings.Contains(c.Snippet, "janet") {
		t.Errorf("snippet:\n%s", c.Snippet)
	}
}

func TestResemblance(t *testing.T) {
	for _, tc := range []struct{ name, email, want string }{
		{"Someone", "jane@laptop.local", "the same local part as jane@acme.test"},
		{"Someone", "99+JDoe@users.noreply.example", "the address names the login jdoe"},
		{"doe, jane", "x@y.test", "the same name as Jane Doe, but for case, punctuation or order"},
		{"J Doe", "x@y.test", "the same name as Jane Doe, with an initial"},
		{"Janet Doerr", "janet@acme.test", ""},
		{"John Doe", "john@acme.test", ""},
	} {
		if got := resemblance(tc.name, tc.email, me, "jdoe"); got != tc.want {
			t.Errorf("%s <%s>: %q, want %q", tc.name, tc.email, got, tc.want)
		}
	}
}

func TestIssues(t *testing.T) {
	f := newFixture(t)
	missing := func(key, reason, err string) collect.UnresolvedKey {
		return collect.UnresolvedKey{Missing: tracker.Missing{Key: key, Reason: reason, Error: err}}
	}
	f.write("issues/unresolved.json", []collect.UnresolvedKey{missing("ENG-9", tracker.NotFound, "")}, collected)
	expect(t, f.run()["issues"], Pass, "1 keys were not found", "not found: ENG-9")
	f.write("issues/unresolved.json", []collect.UnresolvedKey{missing("ENG-9", tracker.NotFound, ""), missing("OPS-1", tracker.Hidden, "")}, collected)
	expect(t, f.run()["issues"], Warn, "hidden: OPS-1")
	f.write("issues/unresolved.json", []collect.UnresolvedKey{missing("OPS-1", tracker.Hidden, ""), missing("PAY-2", tracker.Failed, "HTTP 503")}, collected)
	expect(t, f.run()["issues"], Fail, "failed: PAY-2", "first error: HTTP 503", "footprint issues")

	f.in.Config.IssueTracker = nil
	expect(t, f.run()["issues"], Skip)
}

func TestPages(t *testing.T) {
	f := newFixture(t)
	os.RemoveAll(filepath.Join(f.in.DataDir, "wiki"))
	f.index.WikiBySpace = nil
	f.write("index.json", f.index, collected)
	expect(t, f.run()["pages"], Warn, "footprint pages")
	f.in.Config.Wiki = nil
	expect(t, f.run()["pages"], Skip)
}

func TestMirror(t *testing.T) {
	f := newFixture(t)
	f.write("org/repos.json", []collect.Repo{{Name: "pay"}, {Name: "shop"}, {Name: "web-ui"}}, collected)
	f.cache("mirror_status.json", map[string]collect.MirrorStatus{"pay": {Name: "pay", OK: true}, "web-ui": {Name: "web-ui", Error: "cannot lock ref"}})
	expect(t, f.run()["mirror"], Fail, "1 repos failed", "web-ui: cannot lock ref", "footprint mirror -only web-ui")
	f.cache("mirror_status.json", map[string]collect.MirrorStatus{"pay": {Name: "pay", OK: true}})
	expect(t, f.run()["mirror"], Warn, "2 non-empty repos", "shop, web-ui")
	os.RemoveAll(f.in.CacheDir)
	expect(t, f.run()["mirror"], Skip)
}

func TestStale(t *testing.T) {
	f := newFixture(t)
	f.write("repos/pay/commits.jsonl", collect.Commit{Repo: "pay", SHA: "c2"}, collected.Add(time.Hour))
	expect(t, f.run()["stale"], Warn, "commits were collected after verify ran", "after issues ran", "\nfootprint verify issues\n")

	f = newFixture(t)
	f.in.Config.Identity.Emails = []string{"JANE@acme.test", "jd@old.test"}
	expect(t, f.run()["stale"], Warn, "after the dataset was collected", "after the identities report",
		"footprint identities commits projects verify issues")
	f.in.Config.Identity.Emails = []string{" JANE@acme.test"}
	f.in.Config.Identity.NotMe = []string{"bot@acme.test"}
	expect(t, f.run()["stale"], Pass)

	f = newFixture(t)
	f.cache("mirror_status.json", map[string]collect.MirrorStatus{"pay": {Name: "pay", OK: true, FetchedAt: stamp(collected.Add(time.Hour))}})
	expect(t, f.run()["stale"], Warn, "the mirrors were fetched after commits ran")

	f = newFixture(t)
	f.in.Now = collected.Add(45 * 24 * time.Hour)
	f.in.Config.IssueTracker, f.in.Config.Wiki = nil, nil
	expect(t, f.run()["stale"], Warn, "last collected 45 days ago (re-run mirror prs commits projects verify)", "\nfootprint mirror prs commits projects verify\n")

	f = newFixture(t)
	f.in.Config = nil // a copied dataset: the identity it recorded, and no mirror state
	os.RemoveAll(f.in.CacheDir)
	expect(t, f.run()["stale"], Pass)
}

func TestEnv(t *testing.T) {
	f := newFixture(t)
	f.in.IsSet = func(string) bool { return false }
	expect(t, f.run()["env"], Warn, "linear needs LINEAR_TOKEN", ".env.example")
	f.in.Env = nil
	expect(t, f.run()["env"], Pass, "no configured tool needs a variable")
}

func TestText(t *testing.T) {
	var b strings.Builder
	r := Report{Checks: []Check{{ID: "verify", Status: Fail, Summary: "missing", Details: []string{"a"}, Fix: "footprint verify", Snippet: "\"emails\": [\n  \"x\"\n]"}}}
	if err := r.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	want := "FAIL verify: missing\n     - a\n     fix: footprint verify\n     in config.json, once the user confirms:\n" +
		"       \"emails\": [\n         \"x\"\n       ]\n"
	if b.String() != want {
		t.Errorf("text:\n%s\nwant:\n%s", b.String(), want)
	}
}
