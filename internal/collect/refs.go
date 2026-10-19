package collect

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

// refContainment maps each target commit (all off the default branch) to the other refs whose
// history contains it.
func refContainment(ctx context.Context, path, defaultRef string, targets map[string]bool) (map[string][]string, error) {
	if len(targets) == 0 {
		return map[string][]string{}, nil
	}
	refs, err := git(ctx, path, nil, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(*objectname)")
	if err != nil {
		return nil, err
	}
	args := []string{"rev-list", "--topo-order", "--parents", "--all"}
	if hasRef(ctx, path, defaultRef) {
		args = append(args, "--not", defaultRef)
	}
	revs, err := git(ctx, path, nil, args...)
	if err != nil {
		return nil, err
	}
	return propagateRefs(string(refs), string(revs), defaultRef, targets), nil
}

// propagateRefs walks `rev-list --topo-order --parents` output (children before parents) once,
// pushing a bitset of the refs whose tip is at or above each commit down to its parents. refLines
// are `for-each-ref` lines of refname\0object\0peeled-object; the default branch is skipped.
func propagateRefs(refLines, revLines, defaultRef string, targets map[string]bool) map[string][]string {
	var names []string
	tips := map[string]bitset{}
	for _, line := range strings.Split(refLines, "\n") {
		f := strings.Split(line, "\x00")
		if len(f) != 3 || f[0] == defaultRef {
			continue
		}
		tip := f[1]
		if f[2] != "" { // annotated tag: the commit it points at
			tip = f[2]
		}
		tips[tip] = tips[tip].with(len(names))
		names = append(names, f[0])
	}
	masks := map[string]bitset{} // pending sets for commits not reached yet; each owned by its entry
	result := map[string][]string{}
	for _, line := range strings.Split(revLines, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		sha := f[0]
		mask := orInto(masks[sha], tips[sha])
		delete(masks, sha)
		if targets[sha] {
			result[sha] = mask.names(names)
		}
		for _, parent := range f[1:] {
			masks[parent] = orInto(masks[parent], mask)
		}
	}
	return result
}

type bitset []uint64

// with returns b with bit i set (b may be reallocated).
func (b bitset) with(i int) bitset {
	for len(b) <= i/64 {
		b = append(b, 0)
	}
	b[i/64] |= 1 << (i % 64)
	return b
}

// orInto returns dst|src, reusing dst's storage; dst must be exclusively owned by the caller.
func orInto(dst, src bitset) bitset {
	if len(dst) < len(src) {
		dst = append(dst, make(bitset, len(src)-len(dst))...)
	}
	for i, w := range src {
		dst[i] |= w
	}
	return dst
}

func (b bitset) names(all []string) []string {
	out := []string{}
	for i, w := range b {
		for bit := 0; w != 0 && bit < 64; bit++ {
			if w&(1<<bit) != 0 {
				out = append(out, all[i*64+bit])
			}
		}
	}
	return out
}

type refSet struct {
	branches []string
	prs      []int
	tags     []string
	counts   RefCounts
}

// classifyRefs sorts containing refs into branches, PR numbers and tags, capping each list.
// The lowest PR number is usually the PR that introduced the commit.
func classifyRefs(refs []string) refSet {
	branches, tags, prs := []string{}, []string{}, []int{}
	for _, r := range refs {
		switch {
		case strings.HasPrefix(r, "refs/heads/"):
			branches = append(branches, strings.TrimPrefix(r, "refs/heads/"))
		case strings.HasPrefix(r, "refs/tags/"):
			tags = append(tags, strings.TrimPrefix(r, "refs/tags/"))
		case strings.HasPrefix(r, "refs/pull/"):
			if parts := strings.Split(r, "/"); len(parts) > 2 {
				if n, err := strconv.Atoi(parts[2]); err == nil {
					prs = append(prs, n)
				}
			}
		}
	}
	sort.Strings(branches)
	sort.Strings(tags)
	sort.Ints(prs)
	return refSet{
		branches: branches[:min(len(branches), maxRefsPerKind)],
		prs:      prs[:min(len(prs), maxRefsPerKind)],
		tags:     tags[:min(len(tags), maxRefsPerKind)],
		counts:   RefCounts{Branches: len(branches), PullRequestRefs: len(prs), Tags: len(tags)},
	}
}
