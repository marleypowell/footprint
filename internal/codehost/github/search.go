package github

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/marleypowell/footprint/internal/codehost"
	"github.com/marleypowell/footprint/internal/logx"
)

// commitSearch is a page of REST commit search results.
type commitSearch struct {
	TotalCount int `json:"total_count"`
	Items      []struct {
		SHA        string `json:"sha"`
		Repository struct {
			Name string `json:"name"`
		} `json:"repository"`
		Commit struct {
			Author struct {
				Name  string `json:"name"`
				Email string `json:"email"`
				Date  string `json:"date"`
			} `json:"author"`
			Message string `json:"message"`
		} `json:"commit"`
	} `json:"items"`
}

func (c *client) searchCommits(ctx context.Context, params url.Values) (*commitSearch, error) {
	if err := sleep(ctx, searchPause); err != nil {
		return nil, err
	}
	var page commitSearch
	if err := c.get(ctx, "/search/commits", params, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

func (s *commitSearch) commits() []codehost.AttributedCommit {
	out := make([]codehost.AttributedCommit, 0, len(s.Items))
	for _, it := range s.Items {
		line, _, _ := strings.Cut(it.Commit.Message, "\n")
		out = append(out, codehost.AttributedCommit{
			Repo: it.Repository.Name, SHA: it.SHA, Date: it.Commit.Author.Date,
			AuthorName: it.Commit.Author.Name, AuthorEmail: it.Commit.Author.Email,
			Subject: strings.TrimRight(line, "\r"),
		})
	}
	return out
}

// AttributedCommits uses GitHub commit search, which covers default branches and links commits
// to the account through any verified email.
func (h *Host) AttributedCommits(ctx context.Context, author string, limit int) ([]codehost.AttributedCommit, error) {
	api, err := h.client(ctx)
	if err != nil {
		return nil, err
	}
	scope, err := h.scope(ctx, api)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("author:%s %s", author, scope)
	if limit > 0 {
		return sampleCommits(ctx, api, query, limit)
	}
	return allCommits(ctx, api, query)
}

// sampleCommits takes the newest and the oldest commits, where email changes show up.
func sampleCommits(ctx context.Context, api *client, query string, limit int) ([]codehost.AttributedCommit, error) {
	var out []codehost.AttributedCommit
	for _, order := range []string{"desc", "asc"} {
		for page, got := 1, 0; got < (limit+1)/2; page++ {
			res, err := api.searchCommits(ctx, url.Values{
				"q": {query}, "sort": {"author-date"}, "order": {order}, "per_page": {"100"}, "page": {strconv.Itoa(page)},
			})
			if err != nil {
				return nil, err
			}
			out = append(out, res.commits()...)
			got += len(res.Items)
			if len(res.Items) < 100 {
				break
			}
		}
	}
	return out, nil
}

// allCommits slices author-date ranges until each fits the 1000-result search cap.
func allCommits(ctx context.Context, api *client, query string) ([]codehost.AttributedCommit, error) {
	type span struct{ lo, hi time.Time }
	pending := []span{{time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), tomorrow()}}
	var out []codehost.AttributedCommit
	for len(pending) > 0 {
		s := pending[0]
		pending = pending[1:]
		q := fmt.Sprintf("%s author-date:%s..%s", query, s.lo.Format(dateLayout), s.hi.Format(dateLayout))
		var found []codehost.AttributedCommit
		for page := 1; ; page++ {
			res, err := api.searchCommits(ctx, url.Values{"q": {q}, "per_page": {"100"}, "page": {strconv.Itoa(page)}})
			if err != nil {
				return nil, err
			}
			if page == 1 && res.TotalCount > 1000 && s.lo.Before(s.hi) { // over the cap: halve the range
				mid, next := splitDays(s.lo, s.hi)
				pending = append([]span{{s.lo, mid}, {next, s.hi}}, pending...)
				break
			}
			found = append(found, res.commits()...)
			if len(res.Items) < 100 || page == 10 {
				out = append(out, found...)
				logx.Printf("github: commits authored %s..%s -> %d/%d", s.lo.Format(dateLayout), s.hi.Format(dateLayout),
					len(found), res.TotalCount)
				break
			}
		}
	}
	return out, nil
}
