package scrape

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitEnv never prompts for credentials and routes github.com auth through the gh CLI, whatever
// helper (possibly a stale path) the user's global git config names. It also pins the settings
// the parsers depend on, so a user's git config cannot change the output format.
var gitEnv = append(os.Environ(),
	"GIT_TERMINAL_PROMPT=0",
	"GIT_CONFIG_COUNT=4",
	"GIT_CONFIG_KEY_0=credential.https://github.com.helper",
	"GIT_CONFIG_VALUE_0=",
	"GIT_CONFIG_KEY_1=credential.https://github.com.helper",
	"GIT_CONFIG_VALUE_1=!gh auth git-credential",
	"GIT_CONFIG_KEY_2=log.showSignature",
	"GIT_CONFIG_VALUE_2=false",
	"GIT_CONFIG_KEY_3=i18n.logOutputEncoding",
	"GIT_CONFIG_VALUE_3=UTF-8",
)

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
	cmd := exec.CommandContext(ctx, "git", append([]string{"--git-dir", repo}, args...)...)
	cmd.Env = gitEnv
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
