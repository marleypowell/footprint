package confluence

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/atlassian"
	"github.com/marleypowell/footprint/internal/wiki"
)

func userJSON(id, name string) string {
	return `{"type":"known","accountId":"` + id + `","displayName":"` + name + `","publicName":"` + name + `"}`
}

// fakeConfluence serves the REST API v1 search and v2 page endpoints with Confluence's shapes.
func fakeConfluence(t *testing.T) *httptest.Server {
	me, other := userJSON("u-me", "Jane Doe"), userJSON("u-2", "Other Person")
	content := func(id, typ, title, created, createdBy, by string, number int, extra string) string {
		return `{"content":{"id":"` + id + `","type":"` + typ + `","status":"current","title":"` + title + `",
		  "space":{"key":"EN","name":"Engineering"},
		  "history":{"latest":true,"createdBy":` + createdBy + `,"createdDate":"` + created + `"},
		  "version":{"by":` + by + `,"when":"2024-03-01T10:00:00.000Z","number":` + string(rune('0'+number)) + `,"message":"","minorEdit":false},
		  ` + extra + `"_links":{"webui":"/spaces/EN/pages/` + id + `/` + title + `"}},
		  "url":"/spaces/EN/pages/` + id + `","lastModified":"2024-03-01T10:00:00.000Z"}`
	}
	page1 := content("101", "page", "Design", "2024-01-01T09:00:00.000Z", me, other, 3,
		`"ancestors":[{"id":"1","title":"Engineering Home"},{"id":"50","title":"Designs"}],
		 "metadata":{"labels":{"results":[{"prefix":"global","name":"rfc"},{"prefix":"global","name":"audit"}]}},`)
	blog := content("202", "blogpost", "Show and tell", "2024-02-01T09:00:00.000Z", other, me, 2, "")
	folder := content("303", "folder", "Notes", "2024-02-02T09:00:00.000Z", me, me, 1, "")
	board := content("404", "whiteboard", "Sketch", "2024-02-03T09:00:00.000Z", other, other, 5, "")
	adf := `{\"type\":\"doc\",\"version\":1,\"content\":[{\"type\":\"heading\",\"attrs\":{\"level\":1},\"content\":[{\"type\":\"text\",\"text\":\"Design\"}]}]}`
	routes := map[string]string{
		"/wiki/rest/api/user/current": `{"accountId":"u-me","displayName":"Jane Doe","email":"jane.doe@example.com"}`,
		"/wiki/api/v2/pages/101?body-format=atlas_doc_format": `{"id":"101","status":"current","title":"Design","subtype":"live",
		  "authorId":"u-me","ownerId":"u-me","createdAt":"2024-01-01T09:00:00.000Z","version":{"number":3},
		  "body":{"atlas_doc_format":{"value":"` + adf + `","representation":"atlas_doc_format"}}}`,
		"/wiki/api/v2/pages/101/versions?limit=250": `{"results":[
		  {"number":2,"authorId":"u-me","createdAt":"2024-01-02T09:00:00.000Z","message":"tidy","minorEdit":true},
		  {"number":1,"authorId":"u-me","createdAt":"2024-01-01T09:00:00.000Z","message":"","minorEdit":false}],
		  "_links":{"next":"/wiki/api/v2/pages/101/versions?limit=250&cursor=v2"}}`,
		"/wiki/api/v2/pages/101/versions?limit=250&cursor=v2": `{"results":[
		  {"number":3,"authorId":"u-3","createdAt":"2024-03-01T10:00:00.000Z","message":"","minorEdit":false}],"_links":{}}`,
		"/wiki/api/v2/blogposts/202?body-format=atlas_doc_format": `{"id":"202","authorId":"u-2",
		  "body":{"atlas_doc_format":{"value":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"inline"}]}]}}}}`,
		"/wiki/api/v2/blogposts/202/versions?limit=250": `{"results":[
		  {"number":1,"authorId":"u-2","createdAt":"2024-02-01T09:00:00.000Z"},{"number":2,"authorId":"u-me","createdAt":"2024-03-01T10:00:00.000Z"},
		  {"number":3,"authorId":null,"createdAt":"2024-03-02T10:00:00.000Z"}]}`,
		"/wiki/api/v2/folders/303": `{"id":"303","authorId":"u-me","ownerId":"u-me","createdAt":1706864400000}`, // epoch ms, as databases do
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/wiki/rest/api/search":
			for k := range searchParams { // archived content included, on every page
				if q.Get(k) != searchParams.Get(k) {
					t.Errorf("search %s = %q", k, q.Get(k))
				}
			}
			if q.Get("cursor") == "" && !strings.Contains(q.Get("cql"), `contributor = "u-me"`) {
				t.Errorf("cql = %q", q.Get("cql"))
			}
			switch q.Get("cursor") {
			case "": // the next link leaves out the parameters, which the adapter puts back
				w.Write([]byte(`{"results":[` + page1 + `,` + blog + `],"_links":{"next":"/rest/api/search?cql=x&cursor=c2&limit=100"}}`))
			case "c2":
				w.Write([]byte(`{"results":[` + folder + `,` + page1 + `],"_links":{"next":"/rest/api/search?cql=x&cursor=c3"}}`))
			case "c3": // everything on this page was filtered out, but the search goes on
				w.Write([]byte(`{"results":[],"_links":{"next":"/rest/api/search?cql=x&cursor=c4"}}`))
			case "c4":
				w.Write([]byte(`{"results":[` + board + `],"_links":{"next":"/rest/api/search?cql=x&cursor=c4"}}`))
			default:
				t.Errorf("cursor %q", q.Get("cursor"))
			}
			return
		case r.URL.Path == "/wiki/api/v2/whiteboards/404": // deleted between the search and the fetch
			http.NotFound(w, r)
			return
		case r.URL.Path == "/wiki/api/v2/users-bulk":
			body, _ := io.ReadAll(r.Body)
			if string(body) != `{"accountIds":["u-3"]}` {
				t.Errorf("users-bulk asked for %s", body)
			}
			w.Write([]byte(`{"results":[` + userJSON("u-3", "Third Person") + `]}`))
			return
		}
		key := r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		if body, ok := routes[key]; ok {
			w.Write([]byte(body))
			return
		}
		t.Errorf("unexpected request %s", key)
		http.NotFound(w, r)
	}))
}

func TestPages(t *testing.T) {
	srv := fakeConfluence(t)
	defer srv.Close()
	w, err := New([]byte(`{"kind":"confluence","site":"example.atlassian.net"}`))
	if err != nil {
		t.Fatal(err)
	}
	w.api = atlassian.NewClient(srv.URL, "https://example.atlassian.net", "jane.doe@example.com", "token")
	w.once.Do(func() {})

	account, err := w.Account(context.Background())
	if err != nil || account != (wiki.Account{ID: "u-me", Name: "Jane Doe", Email: "jane.doe@example.com"}) {
		t.Fatalf("account %+v, err %v", account, err)
	}
	pages, err := w.Pages(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 4 {
		t.Fatalf("want 4 pages (one listed twice by the search), got %d", len(pages))
	}
	check := func(what string, got, want any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", what, got, want)
		}
	}
	jane, other, third := &wiki.User{ID: "u-me", Name: "Jane Doe"}, &wiki.User{ID: "u-2", Name: "Other Person"}, &wiki.User{ID: "u-3", Name: "Third Person"}
	p := pages[0]
	check("page", []string{p.ID, p.Type, *p.Subtype, p.Title, p.URL}, []string{"101", "page", "live", "Design",
		"https://example.atlassian.net/wiki/spaces/EN/pages/101/Design"})
	check("space", p.Space, wiki.Space{Key: "EN", Name: "Engineering"})
	check("ancestors", p.Ancestors, []wiki.PageRef{{ID: "1", Title: "Engineering Home"}, {ID: "50", Title: "Designs"}})
	check("labels", p.Labels, []string{"audit", "rfc"})
	check("created", []any{p.CreatedAt, p.CreatedBy, p.Owner}, []any{"2024-01-01T09:00:00+00:00", jane, jane})
	check("updated", []any{p.UpdatedAt, p.UpdatedBy, p.Version}, []any{"2024-03-01T10:00:00+00:00", other, 3})
	check("body", *p.Body, "# Design")
	check("versions, oldest first, names looked up", p.Versions, []wiki.Version{
		{Number: 1, Author: jane, CreatedAt: "2024-01-01T09:00:00+00:00"},
		{Number: 2, Author: jane, CreatedAt: "2024-01-02T09:00:00+00:00", Message: "tidy", MinorEdit: true},
		{Number: 3, Author: third, CreatedAt: "2024-03-01T10:00:00+00:00"},
	})
	b := pages[1]
	check("blog post", []any{b.Type, *b.Body, b.CreatedBy, len(b.Versions), b.Versions[1].Author, b.Versions[2].Author},
		[]any{"blogpost", "inline", other, 3, jane, (*wiki.User)(nil)})
	f := pages[2]
	check("folder: owner, no body, current version only", []any{f.Type, f.Owner, f.Body == nil, len(f.Versions), f.Versions[0].Author},
		[]any{"folder", jane, true, 1, jane})
	wb := pages[3]
	check("whiteboard gone from v2: kept from the search", []any{wb.Type, wb.Title, wb.Owner, wb.Version}, []any{"whiteboard", "Sketch", (*wiki.User)(nil), 5})
}

func TestMarkdownBody(t *testing.T) {
	for in, want := range map[string]string{
		`"{\"type\":\"doc\",\"version\":1,\"content\":[]}"`: "",
		`""`:   "",
		`null`: "",
		`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"x"}]}]}`: "x",
	} {
		if got, err := markdownBody([]byte(in)); err != nil || got != want {
			t.Errorf("markdownBody(%s) = %q, %v; want %q", in, got, err, want)
		}
	}
}
