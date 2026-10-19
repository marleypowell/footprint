package collect

import (
	"context"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/marleypowell/footprint/internal/par"
)

// MirrorStatus records one repo's last mirror attempt (<cache>/mirror_status.json).
type MirrorStatus struct {
	Name      string  `json:"name"`
	OK        bool    `json:"ok"`
	Seconds   float64 `json:"seconds"`
	SizeBytes *int64  `json:"size_bytes,omitempty"`
	Branches  *int    `json:"branches,omitempty"`
	Tags      *int    `json:"tags,omitempty"`
	PullHeads *int    `json:"pull_heads,omitempty"`
	FetchedAt string  `json:"fetched_at,omitempty"`
	Error     string  `json:"error,omitempty"`
}

func (h *harness) mirrorStatusPath() string { return filepath.Join(h.cache, "mirror_status.json") }

// mirror bare-clones (or incrementally fetches) every non-empty repo: branches, tags and the
// code host's change-request heads (as refs/pull/<n>/head), so commits from squash-merged PRs
// with deleted branches stay reachable.
func (h *harness) mirror(ctx context.Context) error {
	all, err := h.loadRepos()
	if err != nil {
		return err
	}
	var repos []Repo
	for _, r := range h.selected(all) {
		if !r.IsEmpty {
			repos = append(repos, r)
		}
	}
	sort.SliceStable(repos, func(i, j int) bool { // biggest first packs the workers better
		return deref(repos[i].DiskUsageKB) > deref(repos[j].DiskUsageKB)
	})
	status := map[string]MirrorStatus{}
	if _, err := readJSON(h.mirrorStatusPath(), &status); err != nil {
		return err
	}
	store, err := h.refStore(ctx)
	if err != nil {
		return err
	}
	results := make(chan MirrorStatus)
	go func() {
		defer close(results)
		_ = par.ForEach(ctx, h.cfg.Mirror.Parallelism, repos, func(ctx context.Context, _ int, r Repo) error {
			results <- h.mirrorOne(ctx, r, store)
			return nil // failures are recorded and can be retried with --only
		})
	}()
	done := 0
	for res := range results {
		done++
		status[res.Name] = res
		mark := "ok "
		if !res.OK {
			mark = "ERR"
		}
		logf("mirror %d/%d %s %s (%.1fs) %s", done, len(repos), mark, res.Name, res.Seconds, truncate(res.Error, 200))
		if err := writeJSON(h.mirrorStatusPath(), status); err != nil {
			logf("mirror: saving status: %v", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	failed := 0
	for _, s := range status {
		if !s.OK {
			failed++
		}
	}
	logf("mirror: %d ok, %d failed", len(status)-failed, failed)
	return nil
}

// refStore is how mirrors keep their refs. git's usual store writes a file per ref, so on a
// filesystem that ignores letter case (Windows, and macOS by default) two refs that differ only in
// case (a tag V1.0 beside v1.0) are the same file, and the fetch fails. There, mirrors use the
// reftable store, which keeps refs in tables: new ones are made with it (git 2.45 or newer), and
// older ones are moved to it (git 2.46 or newer).
type refStore struct {
	reftable bool // make new mirrors with the reftable store
	migrate  bool // move existing mirrors to it
}

// caseInsensitive reports whether file names in dir ignore letter case. Tests replace it.
var caseInsensitive = func(dir string) (bool, error) {
	f, err := os.CreateTemp(dir, "case-probe-")
	if err != nil {
		return false, err
	}
	name := f.Name()
	f.Close()
	defer os.Remove(name)
	_, err = os.Stat(filepath.Join(dir, strings.ToUpper(filepath.Base(name))))
	return err == nil, nil
}

func (h *harness) refStore(ctx context.Context) (refStore, error) {
	if err := os.MkdirAll(h.mirrors, 0o755); err != nil {
		return refStore{}, err
	}
	insensitive, err := caseInsensitive(h.mirrors)
	if err != nil || !insensitive {
		return refStore{}, err
	}
	major, minor, err := gitVersion(ctx)
	if err != nil {
		return refStore{}, err
	}
	switch at := func(ma, mi int) bool { return major > ma || major == ma && minor >= mi }; {
	case at(2, 46):
		return refStore{reftable: true, migrate: true}, nil
	case at(2, 45):
		return refStore{reftable: true}, nil
	}
	logf("mirror: this filesystem ignores letter case, so with git %d.%d a repo with refs that differ only "+
		"in case (a tag V1.0 beside v1.0) fails to mirror; git 2.46 or newer keeps them", major, minor)
	return refStore{}, nil
}

var gitVersionRE = regexp.MustCompile(`git version (\d+)\.(\d+)`)

// gitVersion is the installed git's major and minor version ("git version 2.47.1.windows.1").
func gitVersion(ctx context.Context) (major, minor int, err error) {
	cmd := exec.CommandContext(ctx, "git", "version")
	cmd.Env = baseGitEnv
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}
	m := gitVersionRE.FindStringSubmatch(string(out))
	if m == nil {
		return 0, 0, fmt.Errorf("unrecognised git version %q", strings.TrimSpace(string(out)))
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, nil
}

// moveToReftable moves a mirror made with git's files ref store to the reftable store.
func moveToReftable(ctx context.Context, path string) error {
	out, err := git(ctx, path, nil, "rev-parse", "--show-ref-format")
	if err != nil || strings.TrimSpace(string(out)) != "files" {
		return err
	}
	_, err = git(ctx, path, nil, "refs", "migrate", "--ref-format=reftable")
	return err
}

func (h *harness) mirrorOne(ctx context.Context, r Repo, store refStore) MirrorStatus {
	path := h.mirrorPath(r.Name)
	start := time.Now()
	status := MirrorStatus{Name: r.Name}
	remote := h.tools.Host.Remote(r)
	err := func() error {
		if !fileExists(filepath.Join(path, "HEAD")) {
			if err := os.RemoveAll(path); err != nil { // half-initialised by an interrupted run
				return err
			}
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			initArgs := []string{"init", "--bare", "--quiet"}
			if store.reftable {
				initArgs = append(initArgs, "--ref-format=reftable")
			}
			setup := [][]string{
				initArgs,
				{"remote", "add", "origin", remote.URL},
				{"config", "remote.origin.fetch", "+refs/heads/*:refs/heads/*"},
				{"config", "--add", "remote.origin.fetch", "+refs/tags/*:refs/tags/*"},
			}
			if h.cfg.Mirror.FetchPullRefs && remote.PullRequestRefspec != "" {
				setup = append(setup, []string{"config", "--add", "remote.origin.fetch", remote.PullRequestRefspec})
			}
			setup = append(setup, []string{"config", "gc.auto", "0"})
			for _, args := range setup {
				if _, err := git(ctx, path, nil, args...); err != nil {
					return err
				}
			}
		} else if store.migrate {
			if err := moveToReftable(ctx, path); err != nil {
				logf("mirror %s: moving to the reftable ref store: %v", r.Name, err) // the fetch may work anyway
			}
		}
		if _, err := runGit(ctx, gitEnv(remote.GitConfig...), path, nil, "fetch", "--prune", "--quiet", "origin"); err != nil {
			return err
		}
		if b := r.DefaultBranchName(); b != "" {
			if _, err := git(ctx, path, nil, "symbolic-ref", "HEAD", "refs/heads/"+b); err != nil {
				return err
			}
		}
		out, err := git(ctx, path, nil, "for-each-ref", "--format=%(refname)")
		if err != nil {
			return err
		}
		var branches, tags, pulls int
		for _, ref := range strings.Fields(string(out)) {
			switch {
			case strings.HasPrefix(ref, "refs/heads/"):
				branches++
			case strings.HasPrefix(ref, "refs/tags/"):
				tags++
			case strings.HasPrefix(ref, "refs/pull/"):
				pulls++
			}
		}
		size, err := dirSize(path)
		if err != nil {
			return err
		}
		status.SizeBytes, status.Branches, status.Tags, status.PullHeads = &size, &branches, &tags, &pulls
		status.FetchedAt = nowISO()
		return nil
	}()
	status.OK = err == nil
	if err != nil {
		status.Error = err.Error()
	}
	status.Seconds = math.Round(time.Since(start).Seconds()*10) / 10
	return status
}

func dirSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}
