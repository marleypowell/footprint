package collect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/codehost"
)

// newWorkRepo makes an empty working repository on branch main, for a test to commit to.
func newWorkRepo(t *testing.T) *gitFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	f := &gitFixture{t: t, dir: filepath.Join(t.TempDir(), "work")}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.git(me, "init", "-q", "-b", "main")
	return f
}

// gitDir is the repository's .git directory, which the package's git helpers accept as they do a
// bare mirror.
func (f *gitFixture) gitDir() string { return filepath.Join(f.dir, ".git") }

// mirrorTo copies the repository, every ref included, to a bare mirror at path.
func (f *gitFixture) mirrorTo(path string) {
	f.t.Helper()
	f.git(me, "clone", "-q", "--mirror", f.dir, path)
}

func TestGitEnv(t *testing.T) {
	env := gitEnv(codehost.GitSetting{Key: "credential.helper", Value: "!host-cli auth git-credential"})
	// The pinned settings come first and an adapter's extras after them, all through
	// GIT_CONFIG_COUNT, which overrides the user's own git config.
	want := []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=log.showSignature", "GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=i18n.logOutputEncoding", "GIT_CONFIG_VALUE_1=UTF-8",
		"GIT_CONFIG_KEY_2=credential.helper", "GIT_CONFIG_VALUE_2=!host-cli auth git-credential",
	}
	if got := env[len(env)-len(want):]; !reflect.DeepEqual(got, want) {
		t.Errorf("gitEnv(extra) ends %q, want %q", got, want)
	}
	if got := gitEnv(); got[len(got)-5] != "GIT_CONFIG_COUNT=2" {
		t.Errorf("an adapter's extras leaked into the pinned settings: %q", got[len(got)-5:])
	}
}

func TestGitErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		err  *gitError
		want string
	}{
		{"only the first three args, the repo's base name, and stderr on one line",
			&gitError{repo: "/cache/mirrors/api.git", args: []string{"log", "--all", "--format=%H", "--", "x"}, code: 128,
				stderr: "fatal: bad\n   revision"},
			"git log --all --format=%H in api.git (exit 128): fatal: bad revision"},
		{"fewer than three args",
			&gitError{repo: "api.git", args: []string{"fsck"}, code: 1, stderr: ""},
			"git fsck in api.git (exit 1): "},
		{"long stderr (an HTML error page, say) cut at 500 bytes",
			&gitError{repo: "api.git", args: []string{"fetch"}, code: 128, stderr: strings.Repeat("x", 600)},
			"git fetch in api.git (exit 128): " + strings.Repeat("x", 500)},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("%s: Error() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestGitFailureCarriesTheExitCode(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	_, err := git(context.Background(), t.TempDir(), nil, "rev-parse", "HEAD") // not a repository
	var ge *gitError
	if !errors.As(err, &ge) || !strings.Contains(ge.stderr, "not a git repository") {
		t.Fatalf("git in a non-repository = %v, want a *gitError with git's stderr", err)
	}
	if got := exitCode(fmt.Errorf("wrapped: %w", err)); got != 128 {
		t.Errorf("exitCode(wrapped git failure) = %d, want 128", got)
	}
	if got := exitCode(errors.New("not git")); got != -1 {
		t.Errorf("exitCode(other error) = %d, want -1", got)
	}
}

func TestHasRef(t *testing.T) {
	f := newWorkRepo(t)
	f.write("a.txt", "one\n")
	f.commit(me, "add a")
	ctx := context.Background()
	for ref, want := range map[string]bool{"refs/heads/main": true, "main": true, "refs/heads/gone": false, "": false} {
		if got := hasRef(ctx, f.gitDir(), ref); got != want {
			t.Errorf("hasRef(%q) = %v, want %v", ref, got, want)
		}
	}
}

func TestLsTree(t *testing.T) {
	f := newWorkRepo(t)
	// -z keeps names as they are: without it git would quote the non-ASCII name.
	for _, name := range []string{"a b.txt", "docs/naïve.md", "src/pkg/main.go"} {
		f.write(name, "x\n")
	}
	f.commit(me, "add files")
	got, err := lsTree(context.Background(), f.gitDir(), "refs/heads/main")
	if want := []string{"a b.txt", "docs/naïve.md", "src/pkg/main.go"}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("lsTree = %q, %v, want %q", got, err, want)
	}
	if _, err := lsTree(context.Background(), f.gitDir(), "refs/heads/gone"); err == nil {
		t.Error("lsTree of a missing ref should fail")
	}
}

// gitSource is how a catalog adapter reads a repo: the default branch of its mirror, not a checkout.
func TestGitSource(t *testing.T) {
	f := newWorkRepo(t)
	f.write("main.go", "package main // TODO: tidy\n")
	f.write("pkg/util.go", "package pkg\n")
	f.write("notes.md", "TODO: write notes\n")
	f.commit(me, "add files")
	f.git(me, "checkout", "-q", "-b", "other")
	f.write("later.go", "// TODO: only on another branch\n")
	f.commit(me, "add later")
	ctx := context.Background()
	src := &gitSource{repo: "api", gitDir: f.gitDir(), ref: "refs/heads/main"}

	if got := src.Repo(); got != "api" {
		t.Errorf("Repo() = %q, want api", got)
	}
	if got, err := src.List(ctx); err != nil || !reflect.DeepEqual(got, []string{"main.go", "notes.md", "pkg/util.go"}) {
		t.Errorf("List() = %q, %v; want the default branch's files only", got, err)
	}
	tests := []struct {
		pattern string
		globs   []string
		want    []string
	}{
		{"TODO", nil, []string{"main.go", "notes.md"}},
		{"TODO", []string{"*.go"}, []string{"main.go"}},
		{"^package (main|pkg)$", []string{"*.go"}, []string{"pkg/util.go"}}, // an extended regex, per line
		{"FIXME", nil, nil}, // git grep exits 1 for no matches: that is no paths, not an error
	}
	for _, tt := range tests {
		if got, err := src.Grep(ctx, tt.pattern, tt.globs...); err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Grep(%q, %q) = %q, %v; want %q", tt.pattern, tt.globs, got, err, tt.want)
		}
	}
	if got, err := src.Read(ctx, "pkg/util.go"); err != nil || string(got) != "package pkg\n" {
		t.Errorf("Read(pkg/util.go) = %q, %v", got, err)
	}
	if _, err := src.Read(ctx, "later.go"); err == nil {
		t.Error("Read of a file only on another branch should fail")
	}
	bad := &gitSource{repo: "api", gitDir: f.gitDir(), ref: "refs/heads/gone"}
	if _, err := bad.Grep(ctx, "TODO"); err == nil {
		t.Error("Grep on a missing ref should fail, not read as no matches")
	}
}
