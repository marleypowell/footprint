package github

import (
	"net/http"
	"reflect"
	"strconv"
	"testing"
	"time"

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
	r := h.Remote(repo)
	if r.URL != "https://github.com/acme/pay.git" || r.PullRequestRefspec != "+refs/pull/*/head:refs/pull/*/head" {
		t.Errorf("Remote = %+v", r)
	}
	if len(r.GitConfig) != 2 || r.GitConfig[0].Value != "" || r.GitConfig[1].Value != "!gh auth git-credential" {
		t.Errorf("credential helper should be reset, then set to gh: %+v", r.GitConfig)
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
