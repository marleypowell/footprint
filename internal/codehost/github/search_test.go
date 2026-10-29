//go:build go1.25

package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marleypowell/footprint/internal/codehost"
)

// A commit search hit becomes an attributed commit with the message's subject line only (without
// the carriage return of a CRLF message).
func TestCommitSearchCommits(t *testing.T) {
	var page commitSearch
	if err := json.Unmarshal([]byte(`{"total_count": 2, "items": [
  {"sha": "aaa", "repository": {"name": "pay"},
   "commit": {"author": {"name": "Jane Doe", "email": "jdoe@acme.test", "date": "2024-04-01T10:00:00+01:00"},
              "message": "Add retries\r\n\r\nWith a cap."}},
  {"sha": "bbb", "repository": {"name": "web"},
   "commit": {"author": {"name": "jdoe", "email": "1+jdoe@users.noreply.github.com", "date": "2024-04-02T00:00:00Z"},
              "message": "Update README.md"}}
]}`), &page); err != nil {
		t.Fatal(err)
	}
	want := []codehost.AttributedCommit{
		{Repo: "pay", SHA: "aaa", Date: "2024-04-01T10:00:00+01:00", AuthorName: "Jane Doe", AuthorEmail: "jdoe@acme.test", Subject: "Add retries"},
		{Repo: "web", SHA: "bbb", Date: "2024-04-02T00:00:00Z", AuthorName: "jdoe", AuthorEmail: "1+jdoe@users.noreply.github.com", Subject: "Update README.md"},
	}
	if got := page.commits(); !reflect.DeepEqual(got, want) {
		t.Errorf("commits() =\n %+v\nwant\n %+v", got, want)
	}
}

// commitItems is a page of n search hits with SHAs prefix1..prefixN.
func commitItems(prefix string, n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`{"sha": "%s%d", "repository": {"name": "pay"}, "commit": {"message": "change"}}`, prefix, i+1)
	}
	return "[" + strings.Join(items, ",") + "]"
}

// searchRequest is one REST commit search as the fake API receives it.
type searchRequest struct{ q, sort, order, perPage, page string }

// fakeCommitSearch answers GET /search/commits with respond's total_count and items, and records
// the requests.
func fakeCommitSearch(t *testing.T, respond func(r searchRequest) (total int, items string)) (http.Handler, *[]searchRequest) {
	t.Helper()
	var requests []searchRequest
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/search/commits" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		p := r.URL.Query()
		sr := searchRequest{p.Get("q"), p.Get("sort"), p.Get("order"), p.Get("per_page"), p.Get("page")}
		requests = append(requests, sr)
		total, items := respond(sr)
		io.WriteString(w, fmt.Sprintf(`{"total_count": %d, "items": %s}`, total, items))
	}), &requests
}

// A positive limit asks for a sample: about half of it from the newest commits and half from the
// oldest, in whole pages of 100, since an author's emails change over time. Every search first
// waits searchPause, which keeps the client under the search API's 30 requests a minute.
func TestAttributedCommitsSample(t *testing.T) {
	inBubble(t, "", func(t *testing.T) {
		h, requests := fakeCommitSearch(t, func(r searchRequest) (int, string) {
			switch r.order + r.page {
			case "desc1":
				return 5000, commitItems("new-a", 100)
			case "desc2":
				return 5000, commitItems("new-b", 100)
			case "asc1":
				return 5000, commitItems("old", 3) // a short page: no more to ask for
			}
			t.Errorf("unexpected search %+v", r)
			return 0, "[]"
		})
		start := time.Now()
		commits, err := fakeHost(t, h).AttributedCommits(context.Background(), "jdoe", 202) // 101 of each
		if err != nil {
			t.Fatal(err)
		}
		waited := time.Since(start)

		if len(commits) != 203 || commits[0].SHA != "new-a1" || commits[100].SHA != "new-b1" || commits[200].SHA != "old1" {
			t.Errorf("AttributedCommits(limit 202) = %d commits, want the 200 newest then the 3 oldest", len(commits))
		}
		const q = "author:jdoe org:acme"
		want := []searchRequest{
			{q, "author-date", "desc", "100", "1"},
			{q, "author-date", "desc", "100", "2"},
			{q, "author-date", "asc", "100", "1"},
		}
		if !reflect.DeepEqual(*requests, want) {
			t.Errorf("AttributedCommits(limit 202) searched %+v, want %+v", *requests, want)
		}
		if want := 3 * searchPause; waited != want {
			t.Errorf("AttributedCommits(limit 202) took %s, want %s (a pause before each search)", waited, want)
		}
	})
}

// With no limit, AttributedCommits takes every commit: it searches the author-date range from 2000
// to tomorrow, and on finding more than the 1000 results search can return, it halves the range
// (discarding that first page) and searches each half. A single day still over the cap yields its
// first 10 pages of 100.
func TestAttributedCommitsAll(t *testing.T) {
	inBubble(t, "", func(t *testing.T) { // the clock reads 2000-01-01, so tomorrow is 2000-01-02
		const q = "author:jdoe org:acme author-date:"
		h, requests := fakeCommitSearch(t, func(r searchRequest) (int, string) {
			switch r.q {
			case q + "2000-01-01..2000-01-02":
				return 2500, commitItems("discarded", 100)
			case q + "2000-01-01..2000-01-01":
				return 1200, commitItems("day1-p"+r.page+"-", 100)
			case q + "2000-01-02..2000-01-02":
				return 1, commitItems("day2-", 1)
			}
			t.Errorf("unexpected search %+v", r)
			return 0, "[]"
		})
		start := time.Now()
		commits, err := fakeHost(t, h).AttributedCommits(context.Background(), "jdoe", 0)
		if err != nil {
			t.Fatal(err)
		}
		waited := time.Since(start)

		byPrefix := map[string]int{}
		for _, c := range commits {
			prefix, _, _ := strings.Cut(c.SHA, "-")
			byPrefix[prefix]++
		}
		if want := map[string]int{"day1": 1000, "day2": 1}; !reflect.DeepEqual(byPrefix, want) {
			t.Errorf("AttributedCommits() = commits %v, want %v", byPrefix, want)
		}
		want := []searchRequest{{q: q + "2000-01-01..2000-01-02", perPage: "100", page: "1"}}
		for page := 1; page <= 10; page++ {
			want = append(want, searchRequest{q: q + "2000-01-01..2000-01-01", perPage: "100", page: strconv.Itoa(page)})
		}
		want = append(want, searchRequest{q: q + "2000-01-02..2000-01-02", perPage: "100", page: "1"})
		if !reflect.DeepEqual(*requests, want) {
			t.Errorf("AttributedCommits() searched\n %+v\nwant\n %+v", *requests, want)
		}
		if want := 12 * searchPause; waited != want {
			t.Errorf("AttributedCommits() took %s, want %s (a pause before each search)", waited, want)
		}
	})
}

// A context that ends during the pause before a search ends it without a request.
func TestSearchCommitsStopsWhenTheContextEnds(t *testing.T) {
	inBubble(t, "", func(t *testing.T) {
		h, requests := fakeCommitSearch(t, func(searchRequest) (int, string) { return 0, "[]" })
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := fakeHost(t, h).AttributedCommits(ctx, "jdoe", 0)
		if !errors.Is(err, context.DeadlineExceeded) || len(*requests) != 0 {
			t.Errorf("AttributedCommits() = %v after %d searches, want the deadline before any", err, len(*requests))
		}
	})
}
