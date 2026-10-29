package atlassian

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConnectNeedsAToken(t *testing.T) {
	t.Setenv("ATLASSIAN_EMAIL", "jane@example.com")
	t.Setenv("ATLASSIAN_API_TOKEN", "")
	t.Setenv("JIRA_API_TOKEN", "")
	_, err := Connect(context.Background(), "example.atlassian.net", Jira)
	if err == nil || !strings.Contains(err.Error(), "ATLASSIAN_API_TOKEN") || !strings.Contains(err.Error(), "JIRA_API_TOKEN") {
		t.Fatalf("want a hint naming the token variables, got %v", err)
	}
}

// A scoped token is refused by the site, so the client moves to the gateway for the site's cloud ID.
func TestConnectFallsBackToTheGateway(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_edge/tenant_info" {
			if r.Header.Get("Authorization") != "" {
				t.Error("tenant_info is public: send it without the token the site just refused")
			}
			w.Write([]byte(`{"cloudId":"cloud-1"}`))
			return
		}
		http.Error(w, `{"message":"scoped tokens use the gateway"}`, http.StatusUnauthorized)
	}))
	defer site.Close()
	var gotAuth string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ex/jira/cloud-1/rest/api/3/myself" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"accountId":"u-1"}`))
	}))
	defer gateway.Close()

	c, err := connect(context.Background(), site.URL, gateway.URL+"/ex/jira/", "jane@example.com", "tok", Jira)
	if err != nil {
		t.Fatal(err)
	}
	if c.base != gateway.URL+"/ex/jira/cloud-1" || c.Site() != site.URL {
		t.Errorf("base %q, site %q", c.base, c.Site())
	}
	if gotAuth != "Basic amFuZUBleGFtcGxlLmNvbTp0b2s=" { // jane@example.com:tok
		t.Errorf("Authorization = %q", gotAuth)
	}

	// A site that answers an ignored token as an anonymous user is a refusal too, and a gateway
	// that serves Confluence without /wiki gets paths without it.
	anonymous := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_edge/tenant_info" {
			w.Write([]byte(`{"cloudId":"cloud-2"}`))
			return
		}
		w.Write([]byte(`{"type":"anonymous","displayName":"Anonymous"}`))
	}))
	defer anonymous.Close()
	noWiki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ex/confluence/cloud-2/rest/api/user/current":
			w.Write([]byte(`{"accountId":"u-1"}`))
		case "/ex/confluence/cloud-2/rest/api/search":
			w.Write([]byte(`{"results":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer noWiki.Close()
	c, err = connect(context.Background(), anonymous.URL, noWiki.URL+"/ex/confluence/", "jane@example.com", "tok", Confluence)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Get(context.Background(), "/wiki/rest/api/search", nil, nil); err != nil {
		t.Errorf("paths should lose /wiki on this gateway: %v", err)
	}

	// With the gateway refusing too, the error says both were tried.
	_, err = connect(context.Background(), site.URL, site.URL+"/ex/jira/", "jane@example.com", "tok", Jira)
	if err == nil || !strings.Contains(err.Error(), "API gateway") {
		t.Errorf("want an error naming both attempts, got %v", err)
	}
}

func TestRetries(t *testing.T) {
	backoff = func(int) time.Duration { return time.Millisecond }
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.WriteHeader(http.StatusBadGateway)
		default:
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL, srv.URL, "e", "t")
	var out struct{ OK bool }
	if err := c.Get(context.Background(), "/x", nil, &out); err != nil || !out.OK || calls.Load() != 3 {
		t.Fatalf("err %v, out %+v, calls %d", err, out, calls.Load())
	}

	missing := httptest.NewServer(http.NotFoundHandler())
	defer missing.Close()
	err := NewClient(missing.URL, missing.URL, "e", "t").Get(context.Background(), "/issue/X-1", nil, nil)
	if StatusCode(err) != http.StatusNotFound {
		t.Errorf("want a not-found error without retries, got %v", err)
	}
}

func TestNextPath(t *testing.T) {
	for next, want := range map[string]string{
		"/rest/api/search?cursor=abc":                                "/wiki/rest/api/search?cursor=abc",
		"/wiki/api/v2/pages/1/versions?cursor=abc":                   "/wiki/api/v2/pages/1/versions?cursor=abc",
		"https://api.atlassian.com/ex/confluence/c1/wiki/rest/api/x": "/wiki/rest/api/x",
		"/ex/confluence/c1/wiki/api/v2/pages?cursor=x":               "/wiki/api/v2/pages?cursor=x",
		"/rest/api/search?cql=space%3D%22wiki%22&next=/wiki/":        "/wiki/rest/api/search?cql=space%3D%22wiki%22&next=/wiki/",
		"": "",
	} {
		if got := NextPath(next); got != want {
			t.Errorf("NextPath(%q) = %q, want %q", next, got, want)
		}
	}
}

func TestTime(t *testing.T) {
	for in, want := range map[string]string{
		"2026-10-05T06:51:51.526-0500": "2026-10-05T11:51:51+00:00",
		"2024-01-02T10:11:12.345Z":     "2024-01-02T10:11:12+00:00",
		"2024-01-02T10:11:12+01:00":    "2024-01-02T09:11:12+00:00",
		"2024-01-02":                   "2024-01-02",
	} {
		if got := Time(in); got != want {
			t.Errorf("Time(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTimestamp(t *testing.T) {
	var v struct {
		Iso   Timestamp `json:"iso"`
		Epoch Timestamp `json:"epoch"`
		Null  Timestamp `json:"null"`
		Gone  Timestamp `json:"gone"`
	}
	in := `{"iso":"2025-07-04T18:01:57.271Z","epoch":1724062969853,"null":null}`
	if err := json.Unmarshal([]byte(in), &v); err != nil {
		t.Fatal(err)
	}
	if v.Iso != "2025-07-04T18:01:57+00:00" || v.Epoch != "2024-08-19T10:22:49+00:00" || v.Null != "" || v.Gone != "" {
		t.Errorf("%+v", v)
	}
	if err := json.Unmarshal([]byte(`{"iso":true}`), &v); err == nil {
		t.Error("a boolean is not a timestamp")
	}
}

// Env names the variables Connect authenticates with: the email, and one of the product's own token
// or the shared one, so that doctor can say which are missing without reading their values.
func TestProductEnv(t *testing.T) {
	tests := []struct {
		product Product
		want    [][]string
	}{
		{Jira, [][]string{{"ATLASSIAN_EMAIL"}, {"JIRA_API_TOKEN", "ATLASSIAN_API_TOKEN"}}},
		{Confluence, [][]string{{"ATLASSIAN_EMAIL"}, {"CONFLUENCE_API_TOKEN", "ATLASSIAN_API_TOKEN"}}},
	}
	for _, tt := range tests {
		t.Run(tt.product.Name, func(t *testing.T) {
			if got := tt.product.Env(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Env() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A site is written with or without its scheme, and with or without a trailing slash or spaces; a
// scheme it has is kept, so that a test site on http works.
func TestSiteURL(t *testing.T) {
	tests := map[string]string{
		"example.atlassian.net":          "https://example.atlassian.net",
		" example.atlassian.net/ ":       "https://example.atlassian.net",
		"https://example.atlassian.net/": "https://example.atlassian.net",
		"http://localhost:8080":          "http://localhost:8080",
	}
	for in, want := range tests {
		if got := SiteURL(in); got != want {
			t.Errorf("SiteURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// Get adds its parameters to a path, after any query the path already has (pagination links carry
// one); Post sends its body as JSON and decodes the answer.
func TestGetAndPost(t *testing.T) {
	var gotURL, gotBody, gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.RequestURI()
		b, _ := io.ReadAll(r.Body)
		gotBody, gotType = string(b), r.Header.Get("Content-Type")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, srv.URL, "e", "t")
	ctx := context.Background()

	for _, tt := range []struct {
		name, path, want string
		params           url.Values
	}{
		{"a bare path", "/rest/x", "/rest/x?limit=5", url.Values{"limit": {"5"}}},
		{"a path with a query", "/rest/x?start=10", "/rest/x?start=10&limit=5", url.Values{"limit": {"5"}}},
		{"no parameters", "/rest/x?start=10", "/rest/x?start=10", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out struct{ OK bool }
			if err := c.Get(ctx, tt.path, tt.params, &out); err != nil || !out.OK || gotURL != tt.want {
				t.Errorf("Get(%q) requested %q (err %v, ok %v), want %q", tt.path, gotURL, err, out.OK, tt.want)
			}
		})
	}

	var out struct{ OK bool }
	if err := c.Post(ctx, "/rest/search", map[string]any{"jql": "project = ENG"}, &out); err != nil || !out.OK {
		t.Fatalf("Post = %v, ok %v", err, out.OK)
	}
	if gotBody != `{"jql":"project = ENG"}` || gotType != "application/json" {
		t.Errorf("Post sent %q as %q, want the JSON body as application/json", gotBody, gotType)
	}
	if err := c.Post(ctx, "/rest/search", func() {}, nil); err == nil {
		t.Error("Post accepted a body that is not JSON")
	}
}

// A site refusing a token answers 401, 403 or 404 on probe paths that exist for every account;
// anything else is not a refusal.
func TestIsAuthError(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want bool
	}{
		{&StatusError{StatusCode: http.StatusUnauthorized}, true},
		{&StatusError{StatusCode: http.StatusForbidden}, true},
		{&StatusError{StatusCode: http.StatusNotFound}, true},
		{&StatusError{StatusCode: http.StatusInternalServerError}, false},
		{errors.New("connection reset"), false},
		{nil, false},
	} {
		if got := isAuthError(tt.err); got != tt.want {
			t.Errorf("isAuthError(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

// TimePtr normalises an optional timestamp, and an empty one is none.
func TestTimePtr(t *testing.T) {
	in, empty := "2024-01-02T10:11:12+01:00", ""
	if got := TimePtr(&in); got == nil || *got != "2024-01-02T09:11:12+00:00" {
		t.Errorf("TimePtr(%q) = %v", in, got)
	}
	if TimePtr(nil) != nil || TimePtr(&empty) != nil {
		t.Error("TimePtr of nil or empty should be nil")
	}
	if got := Timestamp("2024-01-02T09:11:12+00:00").String(); got != "2024-01-02T09:11:12+00:00" {
		t.Errorf("Timestamp.String() = %q", got)
	}
}

// truncate cuts at a byte count, backing up to the start of a character.
func TestTruncate(t *testing.T) {
	tests := []struct {
		s    string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exact", 5, "exact"},
		{"abcdef", 3, "abc"},
		{"éé", 1, ""},
		{"éé", 3, "é"},
	}
	for _, tt := range tests {
		if got := truncate(tt.s, tt.n); got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
		}
	}
}
