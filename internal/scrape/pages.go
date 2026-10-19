package scrape

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"

	"github.com/marleypowell/footprint/internal/wiki"
)

// PageRecord is one line of <data>/wiki/<space>.jsonl: wiki content the user created or edited,
// with how it involves them.
type PageRecord struct {
	wiki.Page
	Roles        []string `json:"roles"`
	UserVersions int      `json:"user_versions"`
}

// pageRoles are the ways wiki content can involve the user, in the order records list them.
// contributor stands in when the history kept does not show the user (a whiteboard keeps only its
// current version), since the wiki says they created or edited it.
var pageRoles = []string{"creator", "owner", "editor", "contributor"}

// pages loads everything the user created or edited in the wiki, then refreshes its part of
// index.json. With no wiki in the config it does nothing.
func (h *harness) pages(ctx context.Context) error {
	w := h.tools.Wiki
	if w == nil {
		logf("pages: no wiki in the config; skipping")
		return nil
	}
	account, err := w.Account(ctx)
	if err != nil {
		return err
	}
	h.checkAccount("pages", account.Name, account.Email)
	pages, err := w.Pages(ctx, account)
	if err != nil {
		return err
	}
	bySpace := map[string][]PageRecord{}
	for _, p := range pages {
		rec := PageRecord{Page: p}
		rec.Roles, rec.UserVersions = pageInvolvement(&rec.Page, account.ID)
		bySpace[p.Space.Key] = append(bySpace[p.Space.Key], rec)
	}
	dir := filepath.Join(h.raw, "wiki")
	files := map[string]bool{}
	for space, rows := range bySpace {
		slices.SortFunc(rows, func(a, b PageRecord) int {
			return cmp.Or(cmp.Compare(a.CreatedAt, b.CreatedAt), cmp.Compare(a.ID, b.ID))
		})
		name := fileName(space) + ".jsonl"
		if err := writeJSONL(filepath.Join(dir, name), rows); err != nil {
			return err
		}
		files[name] = true
	}
	if err := removeOtherJSONL(dir, files); err != nil {
		return err
	}
	logf("pages: %d pages and other content in %d spaces", len(pages), len(bySpace))
	groups, err := indexGroups(dir, "wiki/", pageRoles, spaceGroup)
	if err != nil {
		return err
	}
	return h.refreshIndex(func(index *Index) { index.WikiBySpace, index.Totals.WikiPages = groups, records(groups) })
}

// pageInvolvement marks the user's versions, and returns the user's roles and version count.
// Editing means saving any version after the first.
func pageInvolvement(p *wiki.Page, userID string) ([]string, int) {
	isUser := func(u *wiki.User) bool { return u != nil && u.ID == userID }
	has := map[string]bool{"creator": isUser(p.CreatedBy), "owner": isUser(p.Owner)}
	mine := 0
	for i := range p.Versions {
		v := &p.Versions[i]
		if v.ByUser = isUser(v.Author); v.ByUser {
			mine++
			has["editor"] = has["editor"] || v.Number > 1
		}
	}
	roles := []string{}
	for _, r := range pageRoles {
		if has[r] {
			roles = append(roles, r)
		}
	}
	if len(roles) == 0 {
		roles = []string{"contributor"}
	}
	return roles, mine
}
