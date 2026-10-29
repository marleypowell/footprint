//go:build go1.25

// testing/synctest needs Go 1.25, newer than the go directive in go.mod, hence the build
// constraint on this file and on the others that run in a bubble.

package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// inBubble runs f in a testing/synctest bubble with local time set to UTC. Waits there take no
// real time: the fake clock jumps ahead whenever every goroutine in the bubble is waiting. A
// non-empty now (RFC 3339) moves the clock there first, so the dates the code derives from
// time.Now are the same on every run.
func inBubble(t *testing.T, now string, f func(t *testing.T)) {
	t.Helper()
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })
	synctest.Test(t, func(t *testing.T) {
		if now != "" {
			at, err := time.Parse(time.RFC3339, now)
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Until(at))
		}
		f(t)
	})
}

// reply is one canned HTTP response.
type reply struct {
	status  int
	headers map[string]string
	body    string
}

// replies answers the n-th request with the n-th reply (the last one once they run out), and
// counts the requests.
func replies(rs ...reply) (http.Handler, *int) {
	calls := 0
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rp := rs[min(calls, len(rs)-1)]
		calls++
		for k, v := range rp.headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(rp.status)
		io.WriteString(w, rp.body)
	}), &calls
}

// The token comes from GH_TOKEN, then GITHUB_TOKEN; only without either does the client ask the
// gh CLI, and without gh it says how to log in.
func TestNewClientToken(t *testing.T) {
	tests := []struct{ ghToken, githubToken, want string }{
		{"from-gh-token", "from-github-token", "from-gh-token"},
		{"", "from-github-token", "from-github-token"},
	}
	for _, tt := range tests {
		t.Setenv("GH_TOKEN", tt.ghToken)
		t.Setenv("GITHUB_TOKEN", tt.githubToken)
		c, err := newClient(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if c.token != tt.want {
			t.Errorf("newClient() with GH_TOKEN=%q GITHUB_TOKEN=%q: token %q, want %q", tt.ghToken, tt.githubToken, c.token, tt.want)
		}
	}

	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("PATH", t.TempDir()) // no gh to ask
	if _, err := newClient(context.Background()); err == nil || !strings.Contains(err.Error(), "or set GH_TOKEN") {
		t.Errorf("newClient() with no token and no gh: error %v, want one saying how to log in", err)
	}
}

// Every request carries the token and pins the API version; REST parameters go in the query
// string, and GraphQL is a JSON POST of the query and its variables.
func TestRequestHeaders(t *testing.T) {
	var got []*http.Request
	var bodies []string
	c := fakeClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got, bodies = append(got, r), append(bodies, string(body))
		io.WriteString(w, `{"data": {}}`)
	}))
	ctx := context.Background()
	var out struct{}
	if err := c.get(ctx, "/search/commits", url.Values{"q": {"author:jdoe org:acme"}, "page": {"2"}}, &out); err != nil {
		t.Fatal(err)
	}
	if err := c.graphql(ctx, "query { viewer { login } }", map[string]any{"org": "acme"}, &out); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		method, url, contentType, body string
	}{
		{"GET", "https://api.github.com/search/commits?page=2&q=author%3Ajdoe+org%3Aacme", "", ""},
		{"POST", "https://api.github.com/graphql", "application/json", `{"query":"query { viewer { login } }","variables":{"org":"acme"}}`},
	}
	for i, tt := range tests {
		r := got[i]
		if r.Method != tt.method || r.URL.String() != tt.url {
			t.Errorf("request %d = %s %s, want %s %s", i, r.Method, r.URL, tt.method, tt.url)
		}
		for header, want := range map[string]string{
			"Authorization": "Bearer test-token", "Accept": "application/vnd.github+json",
			"X-GitHub-Api-Version": "2022-11-28", "User-Agent": "footprint", "Content-Type": tt.contentType,
		} {
			if v := r.Header.Get(header); v != want {
				t.Errorf("request %d header %s = %q, want %q", i, header, v, want)
			}
		}
		if bodies[i] != tt.body {
			t.Errorf("request %d body = %s, want %s", i, bodies[i], tt.body)
		}
	}
}

// request retries server errors and rate limits, waiting a minute longer each time (or as long
// as GitHub says), and returns any other failure at once.
func TestRequestRetries(t *testing.T) {
	tests := []struct {
		name      string
		replies   []reply
		wantCalls int
		wantWait  time.Duration
		wantErr   string // empty for success
	}{
		{"success is returned at once", []reply{{200, nil, `{"ok": true}`}}, 1, 0, ""},
		{"a server error is retried after a minute",
			[]reply{{502, nil, "<html>Bad Gateway</html>"}, {200, nil, `{"ok": true}`}}, 2, time.Minute, ""},
		{"a rate limit waits for Retry-After, plus a second",
			[]reply{{429, map[string]string{"Retry-After": "5"}, "slow down"}, {200, nil, `{"ok": true}`}}, 2, 6 * time.Second, ""},
		{"each retry waits a minute longer, and the sixth failure is returned",
			[]reply{{503, nil, "unavailable"}}, 6, (1 + 2 + 3 + 4 + 5) * time.Minute, "GET /repos/acme/pay: HTTP 503: unavailable"},
		{"a client error is returned at once",
			[]reply{{404, nil, `{"message": "Not Found"}`}}, 1, 0, `GET /repos/acme/pay: HTTP 404: {"message": "Not Found"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inBubble(t, "", func(t *testing.T) {
				h, calls := replies(tt.replies...)
				start := time.Now()
				data, err := fakeClient(h).request(context.Background(), http.MethodGet, "/repos/acme/pay", nil)
				waited := time.Since(start)
				switch {
				case tt.wantErr == "" && (err != nil || string(data) != `{"ok": true}`):
					t.Errorf("request() = (%s, %v), want the last reply's body", data, err)
				case tt.wantErr != "" && (err == nil || err.Error() != tt.wantErr):
					t.Errorf("request() error = %v, want %q", err, tt.wantErr)
				}
				if *calls != tt.wantCalls || waited != tt.wantWait {
					t.Errorf("request() made %d calls over %s, want %d over %s", *calls, waited, tt.wantCalls, tt.wantWait)
				}
			})
		})
	}
}

// A request that gets no response at all (a dropped connection) is retried like a server error.
func TestRequestRetriesNetworkErrors(t *testing.T) {
	inBubble(t, "", func(t *testing.T) {
		calls := 0
		ok := serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "{}") }))
		c := &client{token: "test-token", http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if calls++; calls == 1 {
				return nil, errors.New("connection reset by peer")
			}
			return ok.RoundTrip(r)
		})}}
		start := time.Now()
		if _, err := c.request(context.Background(), http.MethodGet, "/user", nil); err != nil {
			t.Fatal(err)
		}
		if waited := time.Since(start); calls != 2 || waited != time.Minute {
			t.Errorf("request() made %d calls over %s, want 2 over 1m", calls, waited)
		}
	})
}

// A context that ends while request waits to retry ends the request with the context's error.
func TestRequestStopsWaitingWhenTheContextEnds(t *testing.T) {
	inBubble(t, "", func(t *testing.T) {
		h, calls := replies(reply{status: 502, body: "Bad Gateway"})
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		_, err := fakeClient(h).request(ctx, http.MethodGet, "/user", nil)
		if !errors.Is(err, context.DeadlineExceeded) || *calls != 2 {
			t.Errorf("request() = %v after %d calls, want the deadline after 2 (at 0 and 1m)", err, *calls)
		}
	})
}

// An error page is collapsed to one line and cut to 300 characters, so a failure logs one line.
func TestRequestErrorIsOneShortLine(t *testing.T) {
	page := "<html>\n  <head><title>Bad Request</title></head>\n  <body>" + strings.Repeat("x", 400)
	h, _ := replies(reply{status: 400, body: page})
	_, err := fakeClient(h).request(context.Background(), http.MethodGet, "/x", nil)
	line := "<html> <head><title>Bad Request</title></head> <body>"
	want := "GET /x: HTTP 400: " + line + strings.Repeat("x", 300-len(line))
	if err == nil || err.Error() != want {
		t.Errorf("request() error = %v, want %q", err, want)
	}
}

func TestRetryWait(t *testing.T) {
	response := func(status int, headers map[string]string) *http.Response {
		r := &http.Response{StatusCode: status, Header: http.Header{}}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		return r
	}
	reset := strconv.FormatInt(time.Now().Add(90*time.Second).Unix(), 10)
	tests := []struct {
		name     string
		resp     *http.Response
		body     string
		min, max time.Duration
	}{
		{"Retry-After wins", response(403, map[string]string{"Retry-After": "30"}), "secondary rate limit", 31 * time.Second, 31 * time.Second},
		{"primary limit waits for the reset", response(403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": reset}),
			"API rate limit exceeded", 80 * time.Second, 92 * time.Second},
		{"secondary limit backs off", response(403, nil), "You have exceeded a secondary rate limit", time.Minute, time.Minute},
		{"server errors back off", response(502, nil), "<html>Bad Gateway</html>", time.Minute, time.Minute},
		{"other client errors fail", response(404, nil), "Not Found", 0, 0},
		{"forbidden without a rate limit fails", response(403, nil), "Resource not accessible", 0, 0},
	}
	for _, tt := range tests {
		if got := retryWait(tt.resp, []byte(tt.body), 1); got < tt.min || got > tt.max {
			t.Errorf("%s: wait %s, want %s..%s", tt.name, got, tt.min, tt.max)
		}
	}
	if backoff(9) != 5*time.Minute {
		t.Error("backoff is capped at five minutes")
	}
}

// graphql decodes a response's data, fails on errors that come without data, uses data that comes
// with errors (one inaccessible field, say), and retries the errors that pass (rate limits,
// timeouts, "something went wrong"), which GitHub's GraphQL API reports with HTTP 200.
func TestGraphQL(t *testing.T) {
	const ok = `{"data": {"viewer": {"login": "jdoe"}}}`
	tests := []struct {
		name      string
		responses []string // the n-th request gets the n-th, the last once they run out
		wantLogin string
		wantCalls int
		wantWait  time.Duration
		wantErr   string // a prefix of the error; empty for success
	}{
		{"data is decoded", []string{ok}, "jdoe", 1, 0, ""},
		{"errors without data fail",
			[]string{`{"data": null, "errors": [{"type": "NOT_FOUND", "message": "Could not resolve to a User"}]}`},
			"", 1, 0, "graphql: Could not resolve to a User"},
		{"errors beside data are logged and the data used",
			[]string{`{"data": {"viewer": {"login": "jdoe"}}, "errors": [{"type": "FORBIDDEN", "message": "Resource not accessible by integration"}]}`},
			"jdoe", 1, 0, ""},
		{"a transient error is retried after a minute",
			[]string{`{"errors": [{"type": "RATE_LIMITED", "message": "API rate limit exceeded"}]}`, ok},
			"jdoe", 2, time.Minute, ""},
		{"transient errors fail after six attempts",
			[]string{`{"errors": [{"message": "Something went wrong while executing your query."}]}`},
			"", 6, (1 + 2 + 3 + 4 + 5) * time.Minute, "graphql: Something went wrong while executing your query."},
		{"a response that is not JSON fails", []string{`<html>`}, "", 1, 0, "graphql: decoding response: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inBubble(t, "", func(t *testing.T) {
				n := 0
				h, requests := fakeGraphQL(t, func(gqlRequest) string {
					r := tt.responses[min(n, len(tt.responses)-1)]
					n++
					return r
				})
				var out struct {
					Viewer struct {
						Login string `json:"login"`
					} `json:"viewer"`
				}
				start := time.Now()
				err := fakeClient(h).graphql(context.Background(), "query { viewer { login } }", nil, &out)
				waited := time.Since(start)
				switch {
				case tt.wantErr == "" && err != nil:
					t.Errorf("graphql() error = %v", err)
				case tt.wantErr != "" && (err == nil || !strings.HasPrefix(err.Error(), tt.wantErr)):
					t.Errorf("graphql() error = %v, want %q...", err, tt.wantErr)
				}
				if out.Viewer.Login != tt.wantLogin {
					t.Errorf("graphql() decoded login %q, want %q", out.Viewer.Login, tt.wantLogin)
				}
				if len(*requests) != tt.wantCalls || waited != tt.wantWait {
					t.Errorf("graphql() made %d calls over %s, want %d over %s", len(*requests), waited, tt.wantCalls, tt.wantWait)
				}
			})
		})
	}
}

func TestTransientGraphQL(t *testing.T) {
	tests := []struct {
		errs []graphQLError
		want bool
	}{
		{[]graphQLError{{Type: "RATE_LIMITED", Message: "API rate limit already exceeded for user ID 1."}}, true},
		{[]graphQLError{{Message: "You have exceeded a secondary Rate Limit."}}, true},
		{[]graphQLError{{Message: "Timeout on validation of query"}}, true},
		{[]graphQLError{{Message: "Something went wrong while executing your query."}}, true},
		{[]graphQLError{{Type: "NOT_FOUND", Message: "Could not resolve to a Repository"}, {Type: "RATE_LIMITED"}}, true},
		{[]graphQLError{{Type: "NOT_FOUND", Message: "Could not resolve to a Repository"}}, false},
		{[]graphQLError{{Type: "FORBIDDEN", Message: "Resource not accessible by integration"}}, false},
	}
	for _, tt := range tests {
		if got := transientGraphQL(tt.errs); got != tt.want {
			t.Errorf("transientGraphQL(%+v) = %v, want %v", tt.errs, got, tt.want)
		}
	}
}

// Search date ranges are inclusive (created:lo..hi), so splitDays halves one into two that
// neither overlap nor leave a gap: lo..mid and mid+1..hi.
func TestSplitDays(t *testing.T) {
	day := func(s string) time.Time {
		d, err := time.Parse(dateLayout, s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	tests := []struct{ lo, hi, mid, next string }{
		{"2024-01-01", "2024-01-11", "2024-01-06", "2024-01-07"},
		{"2024-01-01", "2024-01-04", "2024-01-02", "2024-01-03"},
		{"2024-01-01", "2024-01-02", "2024-01-01", "2024-01-02"},
		{"2023-12-31", "2024-03-01", "2024-01-30", "2024-01-31"}, // 61 days, across a leap February
	}
	for _, tt := range tests {
		mid, next := splitDays(day(tt.lo), day(tt.hi))
		if got, gotNext := mid.Format(dateLayout), next.Format(dateLayout); got != tt.mid || gotNext != tt.next {
			t.Errorf("splitDays(%s, %s) = (%s, %s), want (%s, %s)", tt.lo, tt.hi, got, gotNext, tt.mid, tt.next)
		}
	}
}

// tomorrow, the upper bound of every search, is the start of the next day, so today's results are
// included.
func TestTomorrow(t *testing.T) {
	inBubble(t, "2024-03-10T15:04:05Z", func(t *testing.T) {
		if got, want := tomorrow(), time.Date(2024, 3, 11, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
			t.Errorf("tomorrow() = %s, want %s", got, want)
		}
	})
}
