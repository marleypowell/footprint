package scrape

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/marleypowell/footprint/internal/codehost"
)

// pinnedGitConfig keeps git's output in the format the parsers depend on, whatever the user's
// own git config says.
var pinnedGitConfig = []codehost.GitSetting{
	{Key: "log.showSignature", Value: "false"},
	{Key: "i18n.logOutputEncoding", Value: "UTF-8"},
}

// gitEnv never prompts for credentials, pins the parsers' settings, and adds extra settings (a
// code host's credential helper, say) after them.
func gitEnv(extra ...codehost.GitSetting) []string {
	settings := append(slices.Clone(pinnedGitConfig), extra...)
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_COUNT="+strconv.Itoa(len(settings)))
	for i, s := range settings {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, s.Key), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, s.Value))
	}
	return env
}

var baseGitEnv = gitEnv()

type gitError struct {
	repo   string
	args   []string
	code   int
	stderr string
}

func (e *gitError) Error() string {
	return fmt.Sprintf("git %s in %s (exit %d): %s", strings.Join(e.args[:min(len(e.args), 3)], " "),
		filepath.Base(e.repo), e.code, truncate(oneLine(e.stderr), 500))
}

// git runs git against a bare repository and returns its stdout.
func git(ctx context.Context, repo string, stdin []byte, args ...string) ([]byte, error) {
	return runGit(ctx, baseGitEnv, repo, stdin, args...)
}

func runGit(ctx context.Context, env []string, repo string, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--git-dir", repo}, args...)...)
	cmd.Env = env
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return nil, fmt.Errorf("running git: %w", err)
		}
		return stdout.Bytes(), &gitError{repo: repo, args: args, code: exit.ExitCode(), stderr: strings.TrimSpace(stderr.String())}
	}
	return stdout.Bytes(), nil
}

func exitCode(err error) int {
	var ge *gitError
	if errors.As(err, &ge) {
		return ge.code
	}
	return -1
}

func hasRef(ctx context.Context, repo, ref string) bool {
	out, err := git(ctx, repo, nil, "rev-parse", "--verify", "--quiet", ref)
	return err == nil && len(bytes.TrimSpace(out)) > 0
}

// lsTree lists every file path at ref.
func lsTree(ctx context.Context, repo, ref string) ([]string, error) {
	out, err := git(ctx, repo, nil, "ls-tree", "-r", "-z", "--name-only", ref)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// gitSource is catalog.Source over a mirror's default branch.
type gitSource struct{ repo, gitDir, ref string }

func (s *gitSource) Repo() string { return s.repo }

func (s *gitSource) List(ctx context.Context) ([]string, error) { return lsTree(ctx, s.gitDir, s.ref) }

func (s *gitSource) Grep(ctx context.Context, pattern string, globs ...string) ([]string, error) {
	args := append([]string{"grep", "-l", "-z", "-I", "-E", pattern, s.ref, "--"}, globs...)
	out, err := git(ctx, s.gitDir, nil, args...)
	if err != nil && exitCode(err) != 1 { // 1 = no matches
		return nil, err
	}
	var paths []string
	for _, item := range strings.Split(string(out), "\x00") {
		if _, p, ok := strings.Cut(item, ":"); ok { // "<ref>:<path>"
			paths = append(paths, p)
		}
	}
	return paths, nil
}

func (s *gitSource) Read(ctx context.Context, path string) ([]byte, error) {
	return git(ctx, s.gitDir, nil, "cat-file", "blob", s.ref+":"+path)
}
