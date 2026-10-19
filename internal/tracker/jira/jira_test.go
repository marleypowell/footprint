package jira

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/marleypowell/footprint/internal/atlassian"
	"github.com/marleypowell/footprint/internal/tracker"
)

func TestKeys(t *testing.T) {
	j := &Tracker{keys: map[string]bool{}}
	for _, k := range []string{"SHOP", "APP", "TK", "UI", "CORE", "ACME", "CAFE", "GRO"} {
		j.keys[k] = true
	}
	tests := []struct {
		name, text string
		want       []string
	}{
		{"conventional commit scope", "feat(SHOP-4435): move the shell", []string{"SHOP-4435"}},
		{"lowercase branch name", "jdoe/tk-473", []string{"TK-473"}},
		{"suffix of a feature-flag name", "flag `storefront-enable-order-search-ui-17`", []string{"UI-17"}},
		{"inside a word", "upgrade aspnetcore-8 packages", []string{}},
		{"unknown project", "UTF-8 and ISO-8601", []string{}},
		{"missing dash typo", "SHOP3481 tidy up", []string{"SHOP-3481"}},
		{"leading zero is an analyzer id", "suppress ACME0901 and ACME-0001", []string{}},
		{"seven digits", "SHOP-1234567", []string{}},
		{"adjacent keys", "SHOP-1,SHOP-2 SHOP-3", []string{"SHOP-1", "SHOP-2", "SHOP-3"}},
		{"guid fragments are not keys", "id deadbeef-cafe-1234-abcd-0123456789ab", []string{}},
		{"duplicates collapse", "GRO-12 then GRO-12 again", []string{"GRO-12"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := j.findKeys(tt.text); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("findKeys(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
	if got := j.findKeys("APP-2", "app-1"); !reflect.DeepEqual(got, []string{"APP-1", "APP-2"}) {
		t.Errorf("several texts: got %v", got)
	}
}

// The projects come from project search, live and archived, with the keys each project had before.
func TestProjects(t *testing.T) {
	pages := map[string]string{
		"0": `{"startAt":0,"maxResults":1,"total":2,"isLast":false,"values":[
		  {"key":"PAY","name":"Payments","projectTypeKey":"software","archived":false,"projectKeys":["PAY"]}]}`,
		"1": `{"startAt":1,"maxResults":1,"total":2,"isLast":true,"values":[
		  {"key":"LEG","name":"Legacy","projectTypeKey":"software","archived":true,"projectKeys":["TK","LEG","FW"]}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/rest/api/3/project/search" || !reflect.DeepEqual(q["status"], []string{"live", "archived"}) ||
			q.Get("expand") != "projectKeys" {
			t.Errorf("unexpected request %s", r.URL)
		}
		w.Write([]byte(pages[q.Get("startAt")]))
	}))
	defer srv.Close()
	j, err := New([]byte(`{"kind": "jira", "site": "example.atlassian.net", "extra_project_keys": ["dev"]}`))
	if err != nil {
		t.Fatal(err)
	}
	j.api = atlassian.NewClient(srv.URL, "https://example.atlassian.net", "e", "t")
	j.once.Do(func() {})

	ctx := context.Background()
	got, err := j.Projects(ctx)
	want := []tracker.Project{
		{Key: "LEG", Name: "Legacy", Type: "software", Archived: true, PreviousKeys: []string{"FW", "TK"}},
		{Key: "PAY", Name: "Payments", Type: "software", PreviousKeys: []string{}},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Projects() = %+v, %v; want %+v", got, err, want)
	}
	keys, err := j.Keys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := keys("fix(PAY-111): timeouts, tk-4 and LEG-5; DEV-6 moved, UTF-8"); !reflect.DeepEqual(got, []string{"DEV-6", "LEG-5", "PAY-111", "TK-4"}) {
		t.Errorf("keys of current, previous and extra project keys: %v", got)
	}
}

func TestNoProjectsIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"startAt":0,"maxResults":100,"total":0,"isLast":true,"values":[]}`))
	}))
	defer srv.Close()
	j, _ := New([]byte(`{"site": "example.atlassian.net"}`))
	j.api = atlassian.NewClient(srv.URL, "https://example.atlassian.net", "e", "t")
	j.once.Do(func() {})
	if _, err := j.Keys(context.Background()); err == nil {
		t.Error("an account that sees no projects would find no keys, silently")
	}
}
