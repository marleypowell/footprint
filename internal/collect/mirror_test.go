package collect

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/codehost"
)

func TestGitVersion(t *testing.T) {
	for out, want := range map[string][2]string{
		"git version 2.55.0\n":                 {"2", "55"},
		"git version 2.47.1.windows.1\n":       {"2", "47"},
		"git version 2.39.5 (Apple Git-154)\n": {"2", "39"},
	} {
		if m := gitVersionRE.FindStringSubmatch(out); m == nil || m[1] != want[0] || m[2] != want[1] {
			t.Errorf("%q: got %v, want %v", out, m, want)
		}
	}
}

// On a filesystem that ignores letter case, mirrors keep refs in the reftable store, so refs that
// differ only in case can both be fetched: new mirrors are made with it, older ones moved to it.
func TestMirrorRefStore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	if major, minor, err := gitVersion(ctx); err != nil || major == 2 && minor < 46 || major < 2 {
		t.Skipf("the reftable ref store needs git 2.46 (have %d.%d, %v)", major, minor, err)
	}
	root := t.TempDir()
	f := &gitFixture{t: t, dir: filepath.Join(root, "work")}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.git(me, "init", "-q", "-b", "main")
	f.write("a.txt", "one\n")
	f.commit(me, "add a")
	f.git(me, "tag", "V1.0")
	f.git(me, "tag", "v1.0") // the same file as V1.0 to git's files store, on such a filesystem

	mainBranch := "main"
	host := &fakeHost{remotes: map[string]string{"repo": f.dir}, repos: []codehost.Repo{{Name: "repo", FullName: "org/repo",
		URL: "https://code.example/org/repo", DefaultBranch: &mainBranch, CreatedAt: "2024-01-01T00:00:00Z",
		Languages: []codehost.Language{}, Topics: []string{}}}}
	cfg := &Config{Org: "org", Login: "jdoe", Identity: IdentityConfig{Emails: []string{"jane.doe@example.com"}, DiscoveryPattern: "jane"},
		CodeHost: Tool{Kind: "fakehost"}, Catalog: Tool{Kind: "fakecatalog"}, Mirror: MirrorConfig{Parallelism: 1}}
	opts := Options{DataDir: filepath.Join(root, "raw"), CacheDir: filepath.Join(root, "cache")}
	mirror := filepath.Join(opts.CacheDir, "mirrors", "repo.git")
	defer func(probe func(string) (bool, error)) { caseInsensitive = probe }(caseInsensitive)
	run := func(insensitive bool) (format, tags string) {
		t.Helper()
		caseInsensitive = func(string) (bool, error) { return insensitive, nil }
		if err := Run(ctx, opts, cfg, Tools{Host: host, Catalog: fakeCatalog{}}, []string{"inventory", "mirror"}); err != nil {
			t.Fatal(err)
		}
		out, err := git(ctx, mirror, nil, "rev-parse", "--show-ref-format")
		if err != nil {
			t.Fatal(err)
		}
		refs, err := git(ctx, mirror, nil, "for-each-ref", "--format=%(refname)", "refs/tags")
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out)), strings.Join(strings.Fields(string(refs)), " ")
	}
	check := func(what string, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %q, want %q", what, got, want)
		}
	}
	format, _ := run(false)
	check("a filesystem that tells case apart keeps git's usual store", format, "files")
	format, tags := run(true)
	check("an existing mirror moves to reftable", format, "reftable")
	check("tags", tags, "refs/tags/V1.0 refs/tags/v1.0")
	if err := os.RemoveAll(mirror); err != nil {
		t.Fatal(err)
	}
	format, tags = run(true)
	check("a new mirror is made with reftable", format, "reftable")
	check("tags", tags, "refs/tags/V1.0 refs/tags/v1.0")
}
