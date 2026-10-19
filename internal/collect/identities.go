package collect

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marleypowell/footprint/internal/par"
)

type identityKey struct{ role, name, email string }

// identityCounts counts identities in first-seen order, so merging repos is deterministic.
type identityCounts struct {
	order  []identityKey
	counts map[identityKey]int
}

func (c *identityCounts) add(k identityKey) {
	if c.counts[k] == 0 {
		c.order = append(c.order, k)
	}
	c.counts[k]++
}

// IdentityEntry is one (name, email) variant in <data>/org/identities.json.
type IdentityEntry struct {
	Name            string   `json:"name"`
	Email           string   `json:"email"`
	MatchedByConfig *string  `json:"matched_by_config"`
	Author          int      `json:"author"`
	Committer       int      `json:"committer"`
	CoAuthor        int      `json:"co-author"`
	Repos           []string `json:"repos"`
}

func (h *harness) scanIdentities(ctx context.Context, repo Repo) (*identityCounts, error) {
	path := h.mirrorPath(repo.Name)
	found := &identityCounts{counts: map[identityKey]int{}}
	out, err := git(ctx, path, nil, "log", "--all", "--format=%an%x1f%ae%x1f%cn%x1f%ce")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, "\x1f")
		if len(f) != 4 {
			continue
		}
		if h.id.resembles(f[0], f[1]) {
			found.add(identityKey{"author", f[0], f[1]})
		}
		if h.id.resembles(f[2], f[3]) {
			found.add(identityKey{"committer", f[2], f[3]})
		}
	}
	out, err = git(ctx, path, nil, "log", "--all", "-i", "-E", "--grep="+h.id.pattern, "--format=%B%x1e")
	if err != nil {
		return nil, err
	}
	for _, message := range strings.Split(string(out), "\x1e") {
		for _, p := range parseCoauthors(message) {
			if h.id.resembles(p.Name, p.Email) {
				found.add(identityKey{"co-author", p.Name, p.Email})
			}
		}
	}
	return found, nil
}

// identities surfaces every identity variant, so gaps in the configured identity show up.
func (h *harness) identities(ctx context.Context) error {
	repos, err := h.mirroredRepos()
	if err != nil {
		return err
	}
	results := make([]*identityCounts, len(repos))
	err = par.ForEach(ctx, 8, repos, func(ctx context.Context, i int, r Repo) error {
		found, err := h.scanIdentities(ctx, r)
		results[i] = found
		return err
	})
	if err != nil {
		return err
	}
	type personKey struct{ name, email string }
	people := map[personKey]*IdentityEntry{}
	repoSets := map[personKey]map[string]bool{}
	var order []personKey
	for i, found := range results {
		for _, k := range found.order {
			pk := personKey{k.name, strings.ToLower(k.email)}
			p := people[pk]
			if p == nil {
				p = &IdentityEntry{Name: k.name, Email: k.email}
				if basis := h.id.match(k.name, k.email); basis != "" {
					p.MatchedByConfig = &basis
				}
				people[pk], repoSets[pk] = p, map[string]bool{}
				order = append(order, pk)
			}
			switch k.role {
			case "author":
				p.Author += found.counts[k]
			case "committer":
				p.Committer += found.counts[k]
			default:
				p.CoAuthor += found.counts[k]
			}
			repoSets[pk][repos[i].Name] = true
		}
	}
	rows := make([]IdentityEntry, 0, len(order))
	for _, pk := range order {
		p := people[pk]
		p.Repos = sortedKeys(repoSets[pk])
		rows = append(rows, *p)
	}
	total := func(p IdentityEntry) int { return p.Author + p.Committer + p.CoAuthor }
	sort.SliceStable(rows, func(i, j int) bool { return total(rows[i]) > total(rows[j]) })
	err = writeJSON(filepath.Join(h.raw, "org", "identities.json"), struct {
		GeneratedAt string          `json:"generated_at"`
		Configured  IdentityConfig  `json:"configured"`
		Note        string          `json:"note"`
		Identities  []IdentityEntry `json:"identities"`
	}{
		GeneratedAt: nowISO(),
		Configured:  h.cfg.Identity,
		Note: "commit counts are per (name, email) pair across all refs of all mirrors; " +
			"matched_by_config=null means the variant is NOT treated as the user",
		Identities: rows,
	})
	if err != nil {
		return err
	}
	for _, p := range rows {
		matched := deref(p.MatchedByConfig)
		if matched == "" {
			matched = "UNMATCHED"
		}
		logf("identity %-9s a=%-6d c=%-6d co=%-4d repos=%-3d %s <%s>", matched, p.Author, p.Committer, p.CoAuthor,
			len(p.Repos), p.Name, p.Email)
	}
	return nil
}
