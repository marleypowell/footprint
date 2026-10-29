package collect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/marleypowell/footprint/internal/catalog"
	"github.com/marleypowell/footprint/internal/filekind"
	"github.com/marleypowell/footprint/internal/par"
)

// Limits on what is blamed: a file bigger than either is left out, as a data dump rather than code.
const (
	maxBlameLines = 5000
	maxBlobBytes  = 1 << 20
	blobBatch     = 200  // blobs read per git cat-file
	progressEvery = 1000 // files blamed between progress lines
)

// Ownership is <data>/repos/<repo>/ownership.json: who wrote the lines the default branch has now,
// in the files the user ever changed. Blame credits a line to whoever changed it last, so a
// colleague's rename or a formatting pass takes it (unless the repo lists the commit in its
// .git-blame-ignore-revs): these are the lines as they stand, not a measure of value.
type Ownership struct {
	Repo          string           `json:"repo"`
	DefaultBranch string           `json:"default_branch"`
	Head          string           `json:"head"`    // the commit blamed
	HeadAt        string           `json:"head_at"` // its date: every number is as of then
	GeneratedAt   string           `json:"generated_at"`
	Flags         []string         `json:"flags"` // git blame's
	IgnoredRevs   []IgnoredRev     `json:"ignored_revs"`
	Lines         int              `json:"lines"` // of the text files at head, those left out below excluded
	Dirs          []OwnedDir       `json:"dirs"`
	Files         []OwnedFile      `json:"files"`
	Skipped       OwnershipSkipped `json:"skipped"`
}

// IgnoredRev is a commit blame passes over.
type IgnoredRev struct {
	SHA    string `json:"sha"`
	Source string `json:"source"` // repo_file (.git-blame-ignore-revs) or config
}

// OwnedDir totals the text files under a directory: the first two levels, the directories holding a
// catalog descriptor of the repo's software, at any depth; "(root)" for files at the top.
type OwnedDir struct {
	Path  string `json:"path"`
	Files int    `json:"files"`
	Lines int    `json:"lines"`
}

// OwnedFile is a file the user ever changed, as blamed. Lines is Commits' lines and OtherLines.
type OwnedFile struct {
	Path       string        `json:"path"`
	Lines      int           `json:"lines"`
	Commits    []OwnedCommit `json:"commits"`     // the user's commits (those in commits.jsonl, and the commits landing their PRs)
	OtherLines int           `json:"other_lines"` // everyone else's
	Others     []OwnedAuthor `json:"others"`      // ... by author; empty when the config sets ownership.others to false
}

type OwnedCommit struct {
	SHA   string `json:"sha"`
	Lines int    `json:"lines"`
}

// OwnedAuthor is one identity's lines of a file: who blame names, with the mailmap applied.
type OwnedAuthor struct {
	Name   string `json:"name"`
	Email  string `json:"email"`
	Lines  int    `json:"lines"`
	LastAt string `json:"last_at"` // the author time of the newest of those lines' commits
}

// OwnershipSkipped counts what is left out of Lines, and why.
type OwnershipSkipped struct {
	Generated SkippedFiles `json:"generated"` // by filekind: lockfiles, generated code, vendored directories
	Binary    SkippedFiles `json:"binary"`
	Large     SkippedFiles `json:"large"`  // over 5,000 lines or 1 MiB (the lines of the latter are not counted)
	Failed    SkippedFiles `json:"failed"` // blame failed
}

type SkippedFiles struct {
	Files int `json:"files"`
	Lines int `json:"lines"`
}

// OwnershipConfig is the config's ownership section.
type OwnershipConfig struct {
	Parallelism int      `json:"parallelism"` // files blamed at once; 0 is the CPUs, up to 8
	Copies      *bool    `json:"copies"`      // blame -C: follow lines copied from other files; default true
	IgnoreRevs  []string `json:"ignore_revs"` // commits blame passes over in every repo: formatting passes, say
	Others      *bool    `json:"others"`      // keep the names and emails of other authors; default true
}

func (c OwnershipConfig) copies() bool { return c.Copies == nil || *c.Copies }
func (c OwnershipConfig) others() bool { return c.Others == nil || *c.Others }

func (c OwnershipConfig) parallelism() int {
	if c.Parallelism > 0 {
		return c.Parallelism
	}
	return min(max(runtime.NumCPU(), 1), 8)
}

func (c OwnershipConfig) flags() []string {
	flags := []string{"-w", "-M"}
	if c.copies() {
		flags = append(flags, "-C")
	}
	return flags
}

func (h *harness) ownershipCachePath(repo string) string {
	return filepath.Join(h.cache, "ownership", fileName(repo)+".json")
}

// ownership blames, in every repo the user committed to, the files they ever changed.
func (h *harness) ownership(ctx context.Context) error {
	repos, err := h.mirroredRepos()
	if err != nil {
		return err
	}
	repos = h.selected(repos)
	total, done := 0, 0
	for _, r := range repos {
		out := filepath.Join(h.repoDir(r.Name), "ownership.json")
		own, err := h.ownRepo(ctx, r)
		if err != nil {
			return fmt.Errorf("%s: %w", r.Name, err)
		}
		if own == nil {
			if err := os.Remove(out); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			continue
		}
		if err := writeJSON(out, own); err != nil {
			return err
		}
		done++
		total += len(own.Files)
		logf("ownership %d/%d %s: %d files, %d lines", done, len(repos), r.Name, len(own.Files), own.Lines)
	}
	logf("ownership: %d files blamed in %d repos", total, done)
	return nil
}

type treeFile struct {
	path string
	blob string
	size int
}

// headFiles lists the files (not links or submodules) at ref.
func headFiles(ctx context.Context, mirror, ref string) ([]treeFile, error) {
	out, err := git(ctx, mirror, nil, "ls-tree", "-r", "-l", "-z", ref)
	if err != nil {
		return nil, err
	}
	var files []treeFile
	for _, entry := range strings.Split(string(out), "\x00") {
		meta, p, ok := strings.Cut(entry, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 4 || f[1] != "blob" || f[0] == "120000" {
			continue
		}
		size, _ := strconv.Atoi(f[3])
		files = append(files, treeFile{path: p, blob: f[2], size: size})
	}
	return files, nil
}

// userShas are the commits that are the user's: every commit of commits.jsonl, and the commits of
// their PRs, merge commits and all.
func (h *harness) userShas(repo string) (map[string]bool, error) {
	shas := map[string]bool{}
	commits, err := readJSONL[Commit](filepath.Join(h.repoDir(repo), "commits.jsonl"))
	if err != nil {
		return nil, err
	}
	for _, c := range commits {
		shas[c.SHA] = true
	}
	prs, err := readJSONL[PullRequestRecord](filepath.Join(h.repoDir(repo), "prs.jsonl"))
	if err != nil {
		return nil, err
	}
	for _, pr := range prs {
		if sha := deref(pr.MergeCommitSHA); sha != "" {
			shas[sha] = true
		}
		for _, c := range pr.Commits {
			shas[c.SHA] = true
		}
	}
	return shas, nil
}

// touchedFiles are the files at head that one of shas changed, under that name or an earlier one:
// it walks the default branch's history from the newest commit back, following renames. A file
// deleted and created again under the same name is followed as one.
func touchedFiles(log []byte, shas map[string]bool, head map[string]bool) map[string]bool {
	touched := map[string]bool{}
	alias := map[string]string{} // a name's file at head; "" for a name no file at head ever had
	resolve := func(name string) string {
		if h, ok := alias[name]; ok {
			return h
		}
		if head[name] {
			return name
		}
		return ""
	}
	// the log is \x01<sha>\x01 and then that commit's changes, for each commit
	parts := strings.Split(string(log), "\x01")
	for i := 1; i+1 < len(parts); i += 2 {
		sha, body := parts[i], parts[i+1]
		// git -z writes a NUL, then a newline, before the first status: the header ends in both
		tokens := strings.Split(strings.TrimRight(strings.TrimLeft(body, "\x00\n"), "\x00"), "\x00")
		mine := shas[sha]
		for i := 0; i < len(tokens); i++ {
			status := tokens[i]
			if status == "" || i+1 >= len(tokens) {
				break
			}
			if status[0] == 'R' || status[0] == 'C' {
				if i+2 >= len(tokens) {
					break
				}
				old, name := tokens[i+1], tokens[i+2]
				i += 2
				h := resolve(name)
				if mine && h != "" {
					touched[h] = true
				}
				if status[0] == 'R' {
					alias[name] = ""
					if h != "" {
						alias[old] = h
					}
				}
				continue
			}
			i++
			name := tokens[i]
			h := resolve(name)
			if mine && h != "" && status != "D" {
				touched[h] = true
			}
			if status == "A" {
				alias[name] = "" // before this commit the name was no file
			}
		}
	}
	return touched
}

// readBlobs reads the blobs, in batches, and records the size in lines of each text one.
func readBlobs(ctx context.Context, mirror string, blobs []string, into map[string]blobInfo) error {
	for len(blobs) > 0 {
		n := min(blobBatch, len(blobs))
		batch := blobs[:n]
		blobs = blobs[n:]
		out, err := git(ctx, mirror, []byte(strings.Join(batch, "\n")+"\n"), "cat-file", "--batch")
		if err != nil {
			return err
		}
		for _, oid := range batch {
			nl := bytes.IndexByte(out, '\n')
			if nl < 0 {
				return fmt.Errorf("cat-file: no header for %s", oid)
			}
			f := strings.Fields(string(out[:nl]))
			out = out[nl+1:]
			if len(f) != 3 || f[1] != "blob" {
				into[oid] = blobInfo{} // missing: counted as empty
				continue
			}
			size, _ := strconv.Atoi(f[2])
			if size > len(out) {
				return fmt.Errorf("cat-file: %s is cut short", oid)
			}
			lines, binary := countLines(out[:size])
			into[oid] = blobInfo{Lines: lines, Binary: binary}
			out = out[size+1:] // the newline after the content
		}
	}
	return nil
}

// ignoredRevs are the commits to pass over: the repo's .git-blame-ignore-revs at ref, and the
// config's, those that exist in the repo. The file for git blame is written under the cache.
func (h *harness) ignoredRevs(ctx context.Context, repo, mirror, ref string) ([]IgnoredRev, string, error) {
	var revs []IgnoredRev
	seen := map[string]bool{}
	add := func(shas []string, source string) {
		for _, sha := range shas {
			if !seen[sha] && hasRef(ctx, mirror, sha+"^{commit}") {
				seen[sha] = true
				revs = append(revs, IgnoredRev{SHA: sha, Source: source})
			}
		}
	}
	if out, err := git(ctx, mirror, nil, "show", ref+":.git-blame-ignore-revs"); err == nil {
		add(parseIgnoreRevs(string(out)), "repo_file")
	}
	add(parseIgnoreRevs(strings.Join(h.cfg.Ownership.IgnoreRevs, "\n")), "config")
	slices.SortFunc(revs, func(a, b IgnoredRev) int { return strings.Compare(a.SHA, b.SHA) })
	if len(revs) == 0 {
		return []IgnoredRev{}, "", nil
	}
	var b strings.Builder
	for _, r := range revs {
		b.WriteString(r.SHA + "\n")
	}
	file := filepath.Join(h.cache, "ownership", fileName(repo)+".ignore-revs")
	return revs, file, writeFileAtomic(file, []byte(b.String()))
}

// ownRepo blames the files of one repo, or returns nil when there is nothing to: the user has no
// commits in it, or it has no default branch in the mirror.
func (h *harness) ownRepo(ctx context.Context, repo Repo) (*Ownership, error) {
	mirror := h.mirrorPath(repo.Name)
	branch := repo.DefaultBranchName()
	ref := "refs/heads/" + branch
	if branch == "" || !hasRef(ctx, mirror, ref) || !fileExists(filepath.Join(h.repoDir(repo.Name), "commits.jsonl")) {
		return nil, nil
	}
	shas, err := h.userShas(repo.Name)
	if err != nil {
		return nil, err
	}
	files, err := headFiles(ctx, mirror, ref)
	if err != nil {
		return nil, err
	}
	headSet := map[string]bool{}
	for _, f := range files {
		headSet[f.path] = true
	}
	log, err := git(ctx, mirror, nil, "log", "--no-merges", "-M", "--name-status", "-z", "--format=%x01%H%x01", ref)
	if err != nil {
		return nil, err
	}
	touched := touchedFiles(log, shas, headSet)

	headOut, err := git(ctx, mirror, nil, "log", "-1", "--format=%H%x00%cI", ref)
	if err != nil {
		return nil, err
	}
	head, headAt, _ := strings.Cut(strings.TrimSpace(string(headOut)), "\x00")
	revs, ignoreFile, err := h.ignoredRevs(ctx, repo.Name, mirror, ref)
	if err != nil {
		return nil, err
	}
	ignored := make([]string, len(revs))
	for i, r := range revs {
		ignored[i] = r.SHA
	}
	flags := h.cfg.Ownership.flags()
	flagKey, ignoreKey := strings.Join(flags, " "), digest(ignored)

	var old blameCache
	if _, err := readJSON(h.ownershipCachePath(repo.Name), &old); err != nil {
		logf("ownership: %s: ignoring an unreadable cache: %v", repo.Name, err)
		old = blameCache{}
	}
	cache := newBlameCache()
	var missing []string
	for _, f := range files {
		if info, ok := old.Blobs[f.blob]; ok {
			cache.Blobs[f.blob] = info
		} else if f.size <= maxBlobBytes {
			missing = append(missing, f.blob)
		}
	}
	slices.Sort(missing)
	if err := readBlobs(ctx, mirror, slices.Compact(missing), cache.Blobs); err != nil {
		return nil, err
	}

	entityDirs, err := h.entityDirs(repo.Name)
	if err != nil {
		return nil, err
	}
	own := &Ownership{Repo: repo.Name, DefaultBranch: branch, Head: head, HeadAt: deref(isoUTC(headAt)), GeneratedAt: nowISO(),
		Flags: flags, IgnoredRevs: revs, Dirs: []OwnedDir{}, Files: []OwnedFile{}}
	dirs := map[string]*OwnedDir{}
	var toBlame []treeFile
	for _, f := range files {
		info, known := cache.Blobs[f.blob]
		switch {
		case filekind.Generated(f.path):
			own.Skipped.Generated.Files++
			own.Skipped.Generated.Lines += info.Lines
		case !known || info.Lines > maxBlameLines:
			own.Skipped.Large.Files++
			own.Skipped.Large.Lines += info.Lines
		case info.Binary:
			own.Skipped.Binary.Files++
		default:
			own.Lines += info.Lines
			for _, d := range dirsOf(f.path, entityDirs) {
				if dirs[d] == nil {
					dirs[d] = &OwnedDir{Path: d}
				}
				dirs[d].Files++
				dirs[d].Lines += info.Lines
			}
			if touched[f.path] && info.Lines > 0 {
				toBlame = append(toBlame, f)
			}
		}
	}
	for _, d := range sortedKeys(dirs) {
		own.Dirs = append(own.Dirs, *dirs[d])
	}

	results := make([]*blameResult, len(toBlame))
	var blamed atomic.Int64
	defer func() {
		if len(toBlame) >= progressEvery {
			logf("ownership: %s: %d files blamed", repo.Name, blamed.Load())
		}
	}()
	err = par.ForEach(ctx, h.cfg.Ownership.parallelism(), toBlame, func(ctx context.Context, i int, f treeFile) error {
		if hit, ok := old.Files[blameKey(f.path, f.blob, flagKey, ignoreKey)]; ok {
			res := blameResult{Lines: hit.Lines, Authors: map[string]blameAuthor{}}
			for sha := range hit.Lines {
				a, ok := old.Authors[sha]
				if !ok {
					res.Authors = nil // the cache is incomplete: blame again
					break
				}
				res.Authors[sha] = a
			}
			if res.Authors != nil {
				results[i] = &res
				return nil
			}
		}
		if n := blamed.Add(1); n%progressEvery == 0 {
			logf("ownership: %s: %d of %d files blamed", repo.Name, n, len(toBlame))
		}
		out, err := git(ctx, mirror, nil, blameArgs(flags, ignoreFile, ref, f.path)...)
		if err != nil {
			logf("ownership: %s: %s: %v", repo.Name, f.path, err)
			return nil // left as failed
		}
		res, err := parseBlame(out)
		if err != nil {
			return fmt.Errorf("%s: %w", f.path, err)
		}
		results[i] = &res
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i, f := range toBlame {
		res := results[i]
		if res == nil {
			own.Skipped.Failed.Files++
			own.Skipped.Failed.Lines += cache.Blobs[f.blob].Lines
			continue
		}
		cache.Files[blameKey(f.path, f.blob, flagKey, ignoreKey)] = cachedBlame{Lines: res.Lines}
		for sha, a := range res.Authors {
			cache.Authors[sha] = a
		}
		own.Files = append(own.Files, h.ownedFile(f.path, *res, shas))
	}
	sort.Slice(own.Files, func(i, j int) bool { return own.Files[i].Path < own.Files[j].Path })
	if err := writeJSON(h.ownershipCachePath(repo.Name), cache); err != nil {
		return nil, err
	}
	return own, nil
}

// entityDirs are the directories, besides the root, that hold a catalog descriptor in the repo that
// describes software in it: the areas of a monorepo's apps.
func (h *harness) entityDirs(repo string) ([]string, error) {
	entities, err := readJSONL[catalog.Entity](filepath.Join(h.raw, "catalog", "entities.jsonl"))
	if err != nil {
		return nil, err
	}
	dirs := map[string]bool{}
	for _, e := range entities {
		if dir := parentDir(e.Path); dir != "" && e.FoundInRepo == repo && deref(e.SourceRepo) == repo &&
			!e.IsTemplateSkeleton && catalog.DescribesSoftware(e.Category) {
			dirs[dir] = true
		}
	}
	return sortedKeys(dirs), nil
}

// dirsOf are the directories a file counts in: its first two levels, or "(root)", and any of extra
// (directories without a trailing slash) that hold it.
func dirsOf(p string, extra []string) []string {
	parts := strings.Split(p, "/")
	var dirs []string
	switch len(parts) {
	case 1:
		return []string{"(root)"}
	case 2:
		dirs = []string{parts[0] + "/"}
	default:
		dirs = []string{parts[0] + "/", parts[0] + "/" + parts[1] + "/"}
	}
	for _, d := range extra {
		if d = d + "/"; strings.HasPrefix(p, d) && !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// ownedFile splits a file's blame into the user's commits and everyone else's.
func (h *harness) ownedFile(p string, res blameResult, shas map[string]bool) OwnedFile {
	f := OwnedFile{Path: p, Commits: []OwnedCommit{}, Others: []OwnedAuthor{}}
	type who struct{ name, email string }
	others := map[who]*OwnedAuthor{}
	for sha, n := range res.Lines {
		f.Lines += n
		if shas[sha] {
			f.Commits = append(f.Commits, OwnedCommit{SHA: sha, Lines: n})
			continue
		}
		f.OtherLines += n
		if !h.cfg.Ownership.others() {
			continue
		}
		a := res.Authors[sha]
		k := who{a.Name, strings.ToLower(a.Email)}
		o := others[k]
		if o == nil {
			o = &OwnedAuthor{Name: a.Name, Email: a.Email}
			others[k] = o
		}
		o.Lines += n
		if at := deref(isoUTC(time.Unix(a.At, 0).UTC().Format(isoLayout))); at > o.LastAt {
			o.LastAt = at
		}
	}
	slices.SortFunc(f.Commits, func(a, b OwnedCommit) int {
		if a.Lines != b.Lines {
			return b.Lines - a.Lines
		}
		return strings.Compare(a.SHA, b.SHA)
	})
	for _, o := range others {
		f.Others = append(f.Others, *o)
	}
	slices.SortFunc(f.Others, func(a, b OwnedAuthor) int {
		if a.Lines != b.Lines {
			return b.Lines - a.Lines
		}
		return strings.Compare(a.Name+a.Email, b.Name+b.Email)
	})
	return f
}
