package atlassian

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
