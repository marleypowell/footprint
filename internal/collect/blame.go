package collect

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// blameAuthor is who git blame credits a commit to: the author, with the repo's .mailmap applied.
type blameAuthor struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	At    int64  `json:"at"` // author time, Unix seconds
}

// blameResult is one file's blame, as lines per commit.
type blameResult struct {
	Lines   map[string]int         // commit -> lines of the file it last changed
	Authors map[string]blameAuthor // commit -> its author, for every commit in Lines
}

var blameHeaderRE = regexp.MustCompile(`^([0-9a-f]{40}) \d+ \d+ (\d+)$`)

// parseBlame reads `git blame --incremental`: a header line per run of lines ("<sha> <original
// line> <final line> <count>"), and, the first time a commit appears, its author's details.
func parseBlame(out []byte) (blameResult, error) {
	res := blameResult{Lines: map[string]int{}, Authors: map[string]blameAuthor{}}
	var cur string
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := blameHeaderRE.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[2])
			cur = m[1]
			res.Lines[cur] += n
			continue
		}
		if cur == "" {
			continue
		}
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		a := res.Authors[cur]
		switch key {
		case "author":
			a.Name = value
		case "author-mail":
			a.Email = strings.TrimSuffix(strings.TrimPrefix(value, "<"), ">")
		case "author-time":
			a.At, _ = strconv.ParseInt(value, 10, 64)
		default:
			continue
		}
		res.Authors[cur] = a
	}
	if err := sc.Err(); err != nil {
		return res, fmt.Errorf("reading blame: %w", err)
	}
	for sha := range res.Lines {
		if _, ok := res.Authors[sha]; !ok {
			return res, fmt.Errorf("blame names commit %s without its author", sha)
		}
	}
	return res, nil
}

// blameCache is <cache>/ownership/<repo>.json: what the last run learned, so that a run blames only
// the files that changed. Entries are keyed by the file's blob, so another person's later commit to a
// file never reuses a stale blame, and by the flags and ignored revisions the blame was made with.
// What is the user's and what is another's is decided after, so a changed identity reuses it.
type blameCache struct {
	Blobs   map[string]blobInfo    `json:"blobs"`   // blob -> its size in lines
	Files   map[string]cachedBlame `json:"files"`   // blameKey -> lines per commit
	Authors map[string]blameAuthor `json:"authors"` // commit -> author
}

type blobInfo struct {
	Lines  int  `json:"lines"`
	Binary bool `json:"binary"`
}

type cachedBlame struct {
	Lines map[string]int `json:"lines"`
}

func newBlameCache() *blameCache {
	return &blameCache{Blobs: map[string]blobInfo{}, Files: map[string]cachedBlame{}, Authors: map[string]blameAuthor{}}
}

// blameKey identifies a blame: the file's name and blob, and how it was made.
func blameKey(path, blob, flags, ignored string) string {
	return path + "\x00" + blob + "\x00" + flags + "\x00" + ignored
}

// digest is a short hash of strings, for the cache key of the ignored revisions.
func digest(parts []string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// countLines is a text's lines: those ending in a newline, and a last one without. binary says it
// holds a NUL byte in its first 8000 bytes, as git decides.
func countLines(b []byte) (lines int, binary bool) {
	head := b[:min(len(b), 8000)]
	if bytes.IndexByte(head, 0) >= 0 {
		return 0, true
	}
	lines = bytes.Count(b, []byte{'\n'})
	if len(b) > 0 && b[len(b)-1] != '\n' {
		lines++
	}
	return lines, false
}

// blameArgs are git blame's arguments for one file.
func blameArgs(flags []string, ignoreFile, ref, path string) []string {
	args := append([]string{"blame", "--incremental"}, flags...)
	if ignoreFile != "" {
		args = append(args, "--ignore-revs-file", filepath.ToSlash(ignoreFile))
	}
	return append(args, ref, "--", path)
}

// parseIgnoreRevs reads an ignore-revs file: the first word of each line that is a full commit
// name, comments and anything else skipped.
func parseIgnoreRevs(text string) []string {
	var shas []string
	for _, line := range strings.Split(text, "\n") {
		line, _, _ = strings.Cut(line, "#")
		if f := strings.Fields(line); len(f) > 0 && blameHeaderRE.MatchString(f[0]+" 0 0 0") {
			shas = append(shas, f[0])
		}
	}
	slices.Sort(shas)
	return slices.Compact(shas)
}
