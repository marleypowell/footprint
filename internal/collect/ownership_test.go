package collect

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/codehost"
)

// logOf writes commits, newest first, as git log --name-status -z does: each header is \x01<sha>\x01,
// then NUL and a newline, then a status and its path (two for a rename or copy), NUL-separated.
func logOf(commits ...[]string) []byte {
	var b strings.Builder
	for _, c := range commits {
		b.WriteString("\x01" + c[0] + "\x01\x00\n" + strings.Join(c[1:], "\x00") + "\x00")
	}
	return []byte(b.String())
}

func set(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// touchedFiles finds the files at head that the user's commits changed, under the name they had
// then: it reads history from the newest commit back, following renames.
func TestTouchedFiles(t *testing.T) {
	tests := []struct {
		name string
		log  []byte
		mine map[string]bool
		head map[string]bool
		want map[string]bool
	}{
		{"a modified file", logOf([]string{"c2", "M", "a.go"}, []string{"c1", "A", "a.go"}), set("c2"), set("a.go"), set("a.go")},
		{"only the user's commits count", logOf([]string{"c2", "M", "a.go"}, []string{"c1", "A", "b.go"}), set("c1"), set("a.go", "b.go"), set("b.go")},
		{"a file not at head is not blamed", logOf([]string{"c1", "M", "gone.go"}), set("c1"), set("a.go"), set()},
		{"a change before a rename is the renamed file's",
			logOf([]string{"c2", "R100", "old.go", "new.go"}, []string{"c1", "M", "old.go"}), set("c1"), set("new.go"), set("new.go")},
		{"a rename by the user touches the file",
			logOf([]string{"c2", "R090", "old.go", "new.go"}, []string{"c1", "M", "old.go"}), set("c2"), set("new.go"), set("new.go")},
		{"a rename chain is followed back through every name",
			logOf([]string{"c3", "R100", "b.go", "c.go"}, []string{"c2", "R100", "a.go", "b.go"}, []string{"c1", "M", "a.go"}), set("c1"), set("c.go"), set("c.go")},
		{"a name used before the file was added is another file",
			logOf([]string{"c2", "A", "a.go"}, []string{"c1", "M", "a.go"}), set("c1"), set("a.go"), set()},
		{"a copy touches the copy, not its source",
			logOf([]string{"c2", "C100", "src.go", "copy.go"}), set("c2"), set("src.go", "copy.go"), set("copy.go")},
		{"a deletion touches nothing", logOf([]string{"c1", "D", "a.go"}), set("c1"), set("a.go"), set()},
		{"several files in a commit", logOf([]string{"c1", "M", "a.go", "A", "b.go", "M", "c.go"}), set("c1"), set("a.go", "b.go"), set("a.go", "b.go")},
		{"an empty log", nil, set("c1"), set("a.go"), set()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := touchedFiles(tt.log, tt.mine, tt.head); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("touchedFiles() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A file counts toward its first two directories, or "(root)" at the top.
func TestDirsOf(t *testing.T) {
	tests := map[string][]string{
		"README.md":        {"(root)"},
		"src/a.go":         {"src/"},
		"src/app/a.go":     {"src/", "src/app/"},
		"src/app/x/y/a.go": {"src/", "src/app/"},
	}
	for p, want := range tests {
		if got := dirsOf(p); !reflect.DeepEqual(got, want) {
			t.Errorf("dirsOf(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestOwnershipConfigDefaults(t *testing.T) {
	no := false
	tests := []struct {
		name        string
		cfg         OwnershipConfig
		wantFlags   []string
		wantOthers  bool
		wantParMin1 bool
	}{
		{"nothing set follows copied lines and keeps others", OwnershipConfig{}, []string{"-w", "-M", "-C"}, true, true},
		{"copies off", OwnershipConfig{Copies: &no}, []string{"-w", "-M"}, true, true},
		{"others off", OwnershipConfig{Others: &no}, []string{"-w", "-M", "-C"}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.flags(); !reflect.DeepEqual(got, tt.wantFlags) {
				t.Errorf("flags() = %v, want %v", got, tt.wantFlags)
			}
			if tt.cfg.others() != tt.wantOthers {
				t.Errorf("others() = %v", tt.cfg.others())
			}
			if p := tt.cfg.parallelism(); p < 1 || p > 8 {
				t.Errorf("parallelism() = %d, want 1 to 8", p)
			}
		})
	}
	if got := (OwnershipConfig{Parallelism: 3}).parallelism(); got != 3 {
		t.Errorf("parallelism() = %d, want the configured 3", got)
	}
	if runtime.NumCPU() < 1 {
		t.Fatal("no CPUs")
	}
}

// A file's blame splits into the user's commits and everyone else's, the latter by identity (an
// email's case does not make two) with the newest of their lines' dates. With others off, the lines
// are counted and the names are not kept.
func TestOwnedFile(t *testing.T) {
	res := blameResult{
		Lines: map[string]int{shaA: 3, shaB: 5, "c": 2, "d": 1},
		Authors: map[string]blameAuthor{
			shaA: {Name: "Jane Doe", Email: "jane@example.com", At: 1},
			shaB: {Name: "Sam", Email: "Sam@Example.com", At: 1700000000},
			"c":  {Name: "Sam", Email: "sam@example.com", At: 1710000000},
			"d":  {Name: "Pat", Email: "pat@example.com", At: 1},
		},
	}
	mine := map[string]bool{shaA: true}

	h := &harness{cfg: &Config{}}
	got := h.ownedFile("a.go", res, mine)
	want := OwnedFile{Path: "a.go", Lines: 11, OtherLines: 8,
		Commits: []OwnedCommit{{SHA: shaA, Lines: 3}},
		Others: []OwnedAuthor{
			{Name: "Sam", Email: "Sam@Example.com", Lines: 7, LastAt: "2024-03-09T16:00:00+00:00"},
			{Name: "Pat", Email: "pat@example.com", Lines: 1, LastAt: "1970-01-01T00:00:01+00:00"},
		}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ownedFile() = %+v\nwant %+v", got, want)
	}

	no := false
	h = &harness{cfg: &Config{Ownership: OwnershipConfig{Others: &no}}}
	got = h.ownedFile("a.go", res, mine)
	if got.OtherLines != 8 || got.Lines != 11 || len(got.Others) != 0 {
		t.Errorf("with others off: other_lines %d, lines %d, others %v; want 8, 11, none", got.OtherLines, got.Lines, got.Others)
	}
}

// ownership blames, per repo, the files the user changed (under any name they had), counts the
// text lines of the rest toward their directories, leaves out generated, binary and large files,
// passes over the commits the repo's .git-blame-ignore-revs lists, and reuses its cache.
func TestOwnershipStage(t *testing.T) {
	root := t.TempDir()
	f := &gitFixture{t: t, dir: filepath.Join(root, "work")}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := func(prefix string, n int) string {
		var b strings.Builder
		for i := 1; i <= n; i++ {
			b.WriteString(prefix + " " + string(rune('0'+i)) + "\n")
		}
		return b.String()
	}
	f.git(me, "init", "-q", "-b", "main")
	f.write("a.go", lines("mine", 6))
	f.write("b.go", lines("bee", 3))
	f.write("package-lock.json", "{}\n")
	f.commit(me, "feat: add a and b")
	f.write("docs/guide.md", lines("guide", 4))
	f.commit(other, "docs: a guide the user never touches")
	f.write("a.go", strings.Replace(lines("mine", 6), "mine 2\n", "theirs 2\n", 1))
	f.commit(other, "fix: a line of a")
	f.write("c.go", lines("cee", 2))
	f.commit(other, "add c, which the user never touches")
	f.git(me, "mv", "b.go", "lib_b.go")
	f.commit(me, "refactor: rename b")
	f.write("a.go", strings.ReplaceAll(strings.Replace(lines("mine", 6), "mine 2\n", "theirs 2\n", 1), "mine", "reformatted"))
	format := f.commit(other, "style: reformat a")
	f.write(".git-blame-ignore-revs", "# formatting\n"+format+"\n")
	f.commit(other, "chore: ignore the reformat")
	f.write("logo.bin", "\x00\x01binary")
	f.commit(me, "add a logo")
	f.write("d.go", lines("dee", 2))
	squash := f.commit(other, "feat: d (#7)") // lands the user's PR 7

	mainBranch := "main"
	host := &fakeHost{
		remotes: map[string]string{"repo": f.dir},
		repos: []codehost.Repo{{Name: "repo", FullName: "org/repo", URL: "https://code.example/org/repo",
			DefaultBranch: &mainBranch, CreatedAt: "2024-01-01T00:00:00Z", Languages: []codehost.Language{}, Topics: []string{}}},
		prs: []codehost.PullRequest{{Repo: "repo", Number: 7, Title: "d", MergeCommitSHA: &squash}},
	}
	cfg := &Config{Org: "org", Login: "jdoe",
		Identity: IdentityConfig{Emails: []string{"jane.doe@example.com"}, Names: []string{"Jane Doe"}, DiscoveryPattern: "jane|doe"},
		CodeHost: Tool{Kind: "fakehost"}, Catalog: Tool{Kind: "fakecatalog"},
		Mirror: MirrorConfig{Parallelism: 2, FetchPullRefs: true}}
	opts := Options{DataDir: filepath.Join(root, "raw"), CacheDir: filepath.Join(root, "cache")}
	run := func() Ownership {
		t.Helper()
		if err := Run(context.Background(), opts, cfg, Tools{Host: host, Catalog: fakeCatalog{}}, []string{"inventory", "mirror", "prs", "commits", "ownership"}); err != nil {
			t.Fatal(err)
		}
		var own Ownership
		if ok, err := readJSON(filepath.Join(opts.DataDir, "repos", "repo", "ownership.json"), &own); !ok || err != nil {
			t.Fatalf("reading ownership.json: %v %v", ok, err)
		}
		return own
	}
	own := run()

	paths := map[string]OwnedFile{}
	for _, f := range own.Files {
		paths[f.Path] = f
	}
	var names []string
	for _, f := range own.Files {
		names = append(names, f.Path)
	}
	if want := []string{"a.go", "d.go", "lib_b.go"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("files blamed = %v, want %v: the renamed file, the squash commit's, and not c.go (never touched), the lockfile or the binary", names, want)
	}
	if len(own.IgnoredRevs) != 1 || own.IgnoredRevs[0] != (IgnoredRev{SHA: format, Source: "repo_file"}) {
		t.Errorf("ignored revs = %+v, want the reformat from the repo's file", own.IgnoredRevs)
	}
	a := paths["a.go"]
	if a.Lines != 6 || a.OtherLines != 1 || len(a.Commits) != 1 || a.Commits[0].Lines != 5 || len(a.Others) != 1 || a.Others[0].Name != "Other Person" || a.Others[0].Lines != 1 {
		t.Errorf("a.go = %+v, want 5 lines the user's and 1 theirs, the reformat passed over", a)
	}
	if b := paths["lib_b.go"]; b.Lines != 3 || len(b.Commits) == 0 || b.OtherLines != 0 {
		t.Errorf("lib_b.go = %+v, want 3 lines of the user's, followed through the rename", b)
	}
	if d := paths["d.go"]; d.Lines != 2 || d.OtherLines != 0 || len(d.Commits) != 1 || d.Commits[0].SHA != squash {
		t.Errorf("d.go = %+v, want 2 lines in the commit that landed the user's PR", d)
	}
	if own.Skipped.Generated.Files != 1 || own.Skipped.Binary.Files != 1 || own.Skipped.Large.Files != 0 || own.Skipped.Failed.Files != 0 {
		t.Errorf("skipped = %+v, want the lockfile and the binary", own.Skipped)
	}
	// text at head: a 6, lib_b 3, c 2, d 2, the guide 4, and the ignore-revs file's 2
	if own.Lines != 19 {
		t.Errorf("lines = %d, want 19, without the lockfile and the binary", own.Lines)
	}
	dirs := map[string]OwnedDir{}
	for _, d := range own.Dirs {
		dirs[d.Path] = d
	}
	if g := dirs["docs/"]; g.Files != 1 || g.Lines != 4 {
		t.Errorf("docs/ = %+v, want the guide's 4 lines, though the user never blamed it", g)
	}
	if r := dirs["(root)"]; r.Files < 5 {
		t.Errorf("(root) = %+v, want the top-level text files", r)
	}
	if own.Head == "" || own.HeadAt == "" || !reflect.DeepEqual(own.Flags, []string{"-w", "-M", "-C"}) {
		t.Errorf("head %q at %q, flags %v", own.Head, own.HeadAt, own.Flags)
	}

	// A second run, from the cache, writes the same bytes.
	again := run()
	own.GeneratedAt, again.GeneratedAt = "", ""
	if !reflect.DeepEqual(own, again) {
		t.Errorf("a run from the cache differs:\n%+v\n%+v", own, again)
	}
	if !fileExists(filepath.Join(opts.CacheDir, "ownership", "repo.json")) {
		t.Error("no blame cache written")
	}

	// A repo the user no longer has commits in loses its file.
	if err := os.Remove(filepath.Join(opts.DataDir, "repos", "repo", "commits.jsonl")); err != nil {
		t.Fatal(err)
	}
	h, err := newHarness(cfg, opts, Tools{Host: host, Catalog: fakeCatalog{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.ownership(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(opts.DataDir, "repos", "repo", "ownership.json")) {
		t.Error("ownership.json outlived the user's commits")
	}
}
