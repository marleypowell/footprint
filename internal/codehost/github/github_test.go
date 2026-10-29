package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/codehost"
)

func testHost(t *testing.T) *Host {
	t.Helper()
	h, err := New("acme", []byte(`{"kind": "github"}`))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// The API tests talk to in-memory fakes of GitHub: the client's transport hands each request
// straight to an http.Handler through an httptest.ResponseRecorder, so no test touches the network
// or needs a token, and requests still go to https://api.github.com as in production. (The tests
// that wait run in a testing/synctest bubble, where a real httptest.Server would not do: a goroutine
// reading a socket keeps the bubble's fake clock from moving.)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// serve is a transport that answers every request with h.
func serve(h http.Handler) http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Body == nil { // as a server sees a request without one
			r = r.Clone(r.Context())
			r.Body = http.NoBody
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Result(), nil
	})
}

// fakeClient is an API client with the token "test-token", sending its requests to h.
func fakeClient(h http.Handler) *client {
	return &client{http: &http.Client{Transport: serve(h)}, token: "test-token"}
}

// fakeHost is the adapter for org acme with its API client already made, talking to h, so it never
// looks for a token or runs gh.
func fakeHost(t *testing.T, h http.Handler) *Host {
	t.Helper()
	host := testHost(t)
	host.once.Do(func() { host.api = fakeClient(h) })
	host.ownerOnce.Do(func() { host.qualifier = "org" }) // an organization, unless a test asks scope itself
	return host
}

// gqlRequest is a GraphQL request as the fake API receives it.
type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// fakeGraphQL answers POST /graphql with the JSON that respond returns for each request, and
// records the requests in order.
func fakeGraphQL(t *testing.T, respond func(req gqlRequest) string) (http.Handler, *[]gqlRequest) {
	t.Helper()
	var requests []gqlRequest
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/graphql" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req gqlRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("request body is not a GraphQL request: %v", err)
		}
		requests = append(requests, req)
		io.WriteString(w, respond(req))
	}), &requests
}

func TestPullRequestNumbers(t *testing.T) {
	h := testHost(t)
	tests := []struct {
		message string
		want    []int
	}{
		{"feat: add timeout (#50)", []int{50}},
		{"Merge pull request #123 from acme/jdoe/x\n\nbody", []int{123}},
		{"squash (#5)\n\n* first (#6)\n* second", []int{5, 6}},
		{"Merged PR 44946: an Azure DevOps merge", []int{}},
		{"mentions #7 in passing", []int{}},
	}
	for _, tt := range tests {
		if got := h.PullRequestNumbers(tt.message); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("PullRequestNumbers(%q) = %v, want %v", tt.message, got, tt.want)
		}
	}
}

func TestMigratedPullRequestNumbers(t *testing.T) {
	h := testHost(t)
	tests := []struct {
		message string
		want    []int
	}{
		{"Merged PR 44946: fix\n\nMerged PR 12: older", []int{12, 44946}},
		{"Merged PR 44946: fix\n\nRelated work items: #1234", []int{44946}},
		{"see Merged PR 7: in passing", []int{}},
		{"feat: add timeout (#50)", []int{}},
		{"Merge pull request #123 from acme/jdoe/x", []int{}},
	}
	for _, tt := range tests {
		if got := h.MigratedPullRequestNumbers(tt.message); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("MigratedPullRequestNumbers(%q) = %v, want %v", tt.message, got, tt.want)
		}
	}
}

func TestRepoPath(t *testing.T) {
	h := testHost(t)
	tests := []struct {
		ref, want string
		ok        bool
	}{
		{"acme/pay", "acme/pay", true},
		{"url:https://github.com/acme/pay/tree/main/", "acme/pay", true},
		{"https://github.com/acme/web-ui.git", "acme/web-ui", true},
		{"git@github.com:acme/pay.git", "acme/pay", true},
		{"https://dev.azure.com/acme/Platform/_build", "", false},
		{"github/acme/repo", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := h.RepoPath(tt.ref)
		if got != tt.want || ok != tt.ok {
			t.Errorf("RepoPath(%q) = (%q, %v), want (%q, %v)", tt.ref, got, ok, tt.want, tt.ok)
		}
	}
}

func TestConventions(t *testing.T) {
	h := testHost(t)
	repo := codehost.Repo{Name: "pay", FullName: "acme/pay", URL: "https://github.com/acme/pay"}
	if got := h.CommitURL(repo, "abc123"); got != "https://github.com/acme/pay/commit/abc123" {
		t.Errorf("CommitURL = %q", got)
	}
	if got := h.FileURL(repo, "release/1", "docs/adr/0001 why.md"); got != "https://github.com/acme/pay/blob/release/1/docs/adr/0001%20why.md" {
		t.Errorf("FileURL = %q", got)
	}
	r := h.Remote(repo)
	if r.URL != "https://github.com/acme/pay.git" || r.PullRequestRefspec != "+refs/pull/*/head:refs/pull/*/head" {
		t.Errorf("Remote = %+v", r)
	}
	if len(r.GitConfig) != 2 || r.GitConfig[0].Value != "" || r.GitConfig[1].Value != "!gh auth git-credential" {
		t.Errorf("credential helper should be reset, then set to gh: %+v", r.GitConfig)
	}
}

// Account reads GET /user, and derives the two noreply addresses GitHub commits web edits as from
// the id and the lowercased login. A profile without a name leaves Name empty.
func TestAccount(t *testing.T) {
	tests := []struct {
		name, user string
		want       codehost.Account
	}{
		{"named", `{"login": "JDoe", "id": 1234, "name": "Jane Doe", "created_at": "2015-06-01T00:00:00Z"}`,
			codehost.Account{Login: "JDoe", ID: 1234, Name: "Jane Doe", CreatedAt: "2015-06-01T00:00:00Z",
				NoReplyEmails: []string{"1234+jdoe@users.noreply.github.com", "jdoe@users.noreply.github.com"}}},
		{"no name", `{"login": "jdoe", "id": 7, "name": null, "created_at": "2015-06-01T00:00:00Z"}`,
			codehost.Account{Login: "jdoe", ID: 7, CreatedAt: "2015-06-01T00:00:00Z",
				NoReplyEmails: []string{"7+jdoe@users.noreply.github.com", "jdoe@users.noreply.github.com"}}},
	}
	for _, tt := range tests {
		h := fakeHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/user" {
				t.Errorf("%s: unexpected request %s %s", tt.name, r.Method, r.URL)
			}
			io.WriteString(w, tt.user)
		}))
		got, err := h.Account(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: Account() = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

// scope names the owner in a search as GitHub's qualifiers do, org: for an organization and user:
// for a user account, asking the owner's type once; a login no one has is an error.
func TestScope(t *testing.T) {
	tests := []struct {
		name, answer, want, wantErr string
	}{
		{"an organization", `{"data": {"repositoryOwner": {"__typename": "Organization"}}}`, "org:acme", ""},
		{"a user", `{"data": {"repositoryOwner": {"__typename": "User"}}}`, "user:acme", ""},
		{"nobody", `{"data": {"repositoryOwner": null}}`, "", "acme is no organization or user"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, requests := fakeGraphQL(t, func(req gqlRequest) string {
				if req.Query != ownerQuery || req.Variables["login"] != "acme" {
					t.Errorf("asked %q with %v", req.Query, req.Variables)
				}
				return tt.answer
			})
			host := testHost(t)
			for range 2 {
				got, err := host.scope(context.Background(), fakeClient(h))
				if got != tt.want || (tt.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
					t.Errorf("scope() = %q, %v; want %q, error %q", got, err, tt.want, tt.wantErr)
				}
			}
			if len(*requests) != 1 {
				t.Errorf("asked %d times, want once", len(*requests))
			}
		})
	}
}
