package scrape

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParseDiff(t *testing.T) {
	// The layout `git log --raw --numstat -z` emits after the format terminator.
	section := strings.Join([]string{
		"\n:100644 100644 1111111 2222222 M", "src/app.go",
		":000000 100644 0000000 3333333 A", "img/logo.png",
		":100644 100644 4444444 4444444 R100", "old/name.txt", "new/name.txt",
		"3\t1\tsrc/app.go",
		"-\t-\timg/logo.png",
		"0\t0\t", "old/name.txt", "new/name.txt",
		"",
	}, "\x00")
	got := parseDiff(section)
	hundred := 100
	want := []FileChange{
		{Path: "src/app.go", Status: "M", Additions: ptr(3), Deletions: ptr(1)},
		{Path: "img/logo.png", Status: "A", Binary: true},
		{Path: "new/name.txt", OldPath: "old/name.txt", Status: "R", Similarity: &hundred, Additions: ptr(0), Deletions: ptr(0)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseDiff:\n got %s\nwant %s", mustJSON(t, got), mustJSON(t, want))
	}
	if got := parseDiff("\x00"); got == nil || len(got) != 0 {
		t.Errorf("a merge (no diff) should give an empty, non-nil list, got %#v", got)
	}
}

func TestPropagateRefs(t *testing.T) {
	// main: m1 <- m2.  feature: m1 <- f1 <- f2.  refs/pull/7/head -> f1.  annotated tag v1 -> f2.
	refs := strings.Join([]string{
		"refs/heads/main\x00m2\x00",
		"refs/heads/feature\x00f2\x00",
		"refs/pull/7/head\x00f1\x00",
		"refs/tags/v1\x00tagobject\x00f2",
	}, "\n")
	revs := "f2 f1\nf1 m1\n" // rev-list --topo-order --parents --all --not refs/heads/main
	got := propagateRefs(refs, revs, "refs/heads/main", map[string]bool{"f1": true, "f2": true})
	want := map[string][]string{
		"f1": {"refs/heads/feature", "refs/pull/7/head", "refs/tags/v1"},
		"f2": {"refs/heads/feature", "refs/tags/v1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("propagateRefs = %v, want %v", got, want)
	}
}

func TestPropagateRefsManyRefs(t *testing.T) {
	// More than 64 refs on one commit exercises multi-word bitsets.
	var refs, want []string
	for i := range 130 {
		name := fmt.Sprintf("refs/heads/b%03d", i)
		refs = append(refs, name+"\x00c2\x00")
		want = append(want, name)
	}
	got := propagateRefs(strings.Join(refs, "\n"), "c2 c1\nc1\n", "refs/heads/main", map[string]bool{"c1": true})
	if !reflect.DeepEqual(got["c1"], want) {
		t.Errorf("got %d refs, want %d", len(got["c1"]), len(want))
	}
}

func TestClassifyRefs(t *testing.T) {
	got := classifyRefs([]string{"refs/pull/10/head", "refs/heads/zeta", "refs/pull/9/head", "refs/tags/v2", "refs/heads/alpha"})
	want := refSet{
		branches: []string{"alpha", "zeta"},
		prs:      []int{9, 10},
		tags:     []string{"v2"},
		counts:   RefCounts{Branches: 2, PullRequestRefs: 2, Tags: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("classifyRefs = %+v, want %+v", got, want)
	}
	var many []string
	for i := maxRefsPerKind + 5; i > 0; i-- {
		many = append(many, fmt.Sprintf("refs/pull/%d/head", i))
	}
	c := classifyRefs(many)
	if len(c.prs) != maxRefsPerKind || c.prs[0] != 1 || c.counts.PullRequestRefs != maxRefsPerKind+5 {
		t.Errorf("cap: kept %d of %d, starting at %d", len(c.prs), c.counts.PullRequestRefs, c.prs[0])
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
