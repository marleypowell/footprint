package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/marleypowell/footprint/internal/atlassian"
	"github.com/marleypowell/footprint/internal/tracker"
)

const adfDoc = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Make it ","marks":[]},{"type":"text","text":"fast","marks":[{"type":"strong"}]}]}]}`

func userJSON(id, name string) string {
	return `{"self":"https://x/user?accountId=` + id + `","accountId":"` + id + `","displayName":"` + name +
		`","emailAddress":"` + id + `@example.com","avatarUrls":{"48x48":"https://x/a.png"},"active":true}`
}

// fakeJira serves the REST API v3 endpoints the adapter uses, with Jira's response shapes.
func fakeJira(t *testing.T) *httptest.Server {
	me, other := userJSON("u-me", "Jane Doe"), userJSON("u-2", "Other Person")
	issue1 := `{"id":"1001","key":"ENG-1","fields":{
	  "summary":"Speed up search","description":` + adfDoc + `,
	  "issuetype":{"name":"Story","iconUrl":"https://x/i.png"},
	  "project":{"key":"ENG","name":"Engineering","self":"https://x/p"},
	  "status":{"name":"In Progress","statusCategory":{"key":"indeterminate","name":"In Progress"}},
	  "resolution":null,"priority":{"name":"High"},"labels":["backend"],"components":[{"name":"api"}],
	  "fixVersions":[{"name":"1.0"}],"assignee":` + me + `,"reporter":` + other + `,"creator":` + other + `,
	  "created":"2024-01-02T10:00:00.000+0000","updated":"2024-01-05T09:30:00.000+0100","resolutiondate":null,"duedate":"2024-02-01",
	  "parent":{"key":"ENG-0","fields":{"summary":"Search epic","status":{"name":"Done"},"issuetype":{"name":"Epic"}}},
	  "subtasks":[],
	  "issuelinks":[{"type":{"name":"Blocks","inward":"is blocked by","outward":"blocks"},
	    "outwardIssue":{"key":"ENG-2","fields":{"summary":"Ship it","status":{"name":"To Do"},"issuetype":{"name":"Task"}}}}],
	  "attachment":[{"id":"a1","filename":"trace.txt","size":12,"mimeType":"text/plain","author":` + me + `,
	    "created":"2024-01-03T00:00:00.000+0000","content":"https://x/att","self":"https://x/a"}],
	  "comment":{"comments":[{"id":"c2","author":` + me + `,"body":` + adfDoc + `,"created":"2024-01-04T00:00:00.000+0000","updated":"2024-01-04T00:00:00.000+0000"}],"total":2},
	  "worklog":{"worklogs":[{"id":"w1","author":` + me + `,"started":"2024-01-03T09:00:00.000+0000","timeSpentSeconds":3600}],"total":1},
	  "customfield_10020":[{"id":1,"name":"Sprint 1","state":"closed","boardId":3,"startDate":"2024-01-01T09:00:00.000Z","endDate":"2024-01-14T17:00:00.000Z","completeDate":"2024-01-15T10:00:00.000Z"}],
	  "customfield_10016":3,
	  "customfield_10001":{"id":"t1","name":"Otters","title":"Otters","isVisible":true},
	  "customfield_10011":"0|i0001:",
	  "customfield_12347":{"self":"https://x/o","value":"None","id":"1"},
	  "customfield_10200":` + adfDoc + `,
	  "customfield_10400":"plain notes",
	  "customfield_10500":null,
	  "progress":{"progress":0,"total":0},"watches":{"watchCount":1,"isWatching":true},"lastViewed":"2024-01-06T00:00:00.000+0000"
	},"changelog":{"startAt":0,"maxResults":1,"total":2,"histories":[]}}`
	moved := `{"id":"1009","key":"ENG-9","fields":{"summary":"Moved here","project":{"key":"ENG","name":"Engineering"},
	  "status":{"name":"Done","statusCategory":{"key":"done"}},"issuetype":{"name":"Bug"},
	  "created":"2023-05-01T00:00:00.000+0000","updated":"2023-06-01T00:00:00.000+0000","resolutiondate":"2023-06-01T00:00:00.000+0000",
	  "worklog":{"worklogs":[{"id":"w9","author":` + other + `,"started":"2023-05-03T09:00:00.000+0000","timeSpentSeconds":60}],"total":3}},
	  "changelog":{"total":1,"histories":[{"id":"h9","author":` + other + `,"created":"2023-05-02T00:00:00.000+0000",
	    "items":[{"field":"Key","fieldtype":"jira","from":null,"fromString":"OLD-5","to":null,"toString":"ENG-9"},
	             {"field":"project","fieldtype":"jira","from":"1","fromString":"Old","to":"2","toString":"Engineering"}]}]}}`
	pages := map[string]string{
		"/rest/api/3/myself": `{"accountId":"u-me","displayName":"Jane Doe","emailAddress":"jane.doe@example.com"}`,
		"/rest/api/3/field": `[{"id":"summary","name":"Summary","schema":{"type":"string"}},
		  {"id":"customfield_10020","name":"Sprint","schema":{"custom":"com.pyxis.greenhopper.jira:gh-sprint"}},
		  {"id":"customfield_10016","name":"Story point estimate","schema":{"custom":"com.pyxis.greenhopper.jira:jsw-story-points"}},
		  {"id":"customfield_10001","name":"Team","schema":{"custom":"com.atlassian.teams:rm-teams-custom-field-team"}},
		  {"id":"customfield_10011","name":"Rank","schema":{"custom":"com.pyxis.greenhopper.jira:gh-lexo-rank"}},
		  {"id":"customfield_12347","name":"Incident Impact Type","schema":{"custom":"com.atlassian.jira.plugin.system.customfieldtypes:select"}},
		  {"id":"customfield_10200","name":"Notes","schema":{"custom":"com.atlassian.jira.plugin.system.customfieldtypes:textarea"}},
		  {"id":"customfield_10400","name":"Notes","schema":{"custom":"com.atlassian.jira.plugin.system.customfieldtypes:textfield"}},
		  {"id":"progress","name":"Progress","schema":{"type":"progress"}}]`,
		"/rest/api/3/issue/ENG-1":                                     issue1,
		"/rest/api/3/issue/OLD-5":                                     moved,
		"/rest/api/3/issue/ENG-9":                                     moved,
		"/rest/api/3/issue/ENG-100":                                   busy(100, 100, other),
		"/rest/api/3/issue/1100/changelog?maxResults=100&startAt=0":   changes(0, 100, 101, false, other),
		"/rest/api/3/issue/1100/changelog?maxResults=100&startAt=100": changes(100, 101, 101, true, other),
		"/rest/api/3/issue/1001/comment?maxResults=100&orderBy=created&startAt=0": `{"startAt":0,"total":2,"comments":[
		  {"id":"c2","author":` + me + `,"body":` + adfDoc + `,"created":"2024-01-04T00:00:00.000+0000","updated":"2024-01-04T00:00:00.000+0000"},
		  {"id":"c1","author":` + other + `,"body":{"type":"doc","version":1,"content":[]},"created":"2024-01-03T00:00:00.000+0000","updated":"2024-01-03T12:00:00.000+0000"}]}`,
		"/rest/api/3/issue/1001/changelog?maxResults=100&startAt=0": `{"startAt":0,"maxResults":1,"total":2,"isLast":false,"values":[
		  {"id":"h1","author":` + other + `,"created":"2024-01-02T11:00:00.000+0000","items":[
		    {"field":"assignee","fieldtype":"jira","fieldId":"assignee","from":null,"fromString":null,"to":"u-me","toString":"Jane Doe"}]}]}`,
		"/rest/api/3/issue/1001/changelog?maxResults=100&startAt=1": `{"startAt":1,"maxResults":1,"total":2,"isLast":true,"values":[
		  {"id":"h2","author":` + me + `,"created":"2024-01-03T08:00:00.000+0000","items":[
		    {"field":"status","fieldtype":"jira","fieldId":"status","from":"1","fromString":"To Do","to":"3","toString":"In Progress"}]}]}`,
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/search/jql":
			body, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			if req["jql"] != `assignee WAS "u-me" ORDER BY created ASC` || req["includeArchivedProjects"] != true ||
				!reflect.DeepEqual(req["fields"], []any{"key"}) {
				t.Errorf("search request %v", req)
			}
			if req["nextPageToken"] == nil {
				w.Write([]byte(`{"issues":[{"id":"1001","key":"ENG-1"}],"nextPageToken":"p2"}`))
			} else { // the last page carries no token (and need not say isLast)
				w.Write([]byte(`{"issues":[{"id":"1009","key":"ENG-9"}]}`))
			}
			return
		case "/rest/api/3/issue/ENG-403":
			http.Error(w, `{"errorMessages":["forbidden"]}`, http.StatusForbidden)
			return
		case "/rest/api/3/issue/ENG-409":
			http.Error(w, `{"errorMessages":["conflict"]}`, http.StatusConflict)
			return
		}
		key := r.URL.Path
		if r.URL.RawQuery != "" && !strings.HasPrefix(key, "/rest/api/3/issue/ENG") && !strings.HasPrefix(key, "/rest/api/3/issue/OLD") {
			key += "?" + r.URL.RawQuery
		}
		if body, ok := pages[key]; ok {
			w.Write([]byte(body))
			return
		}
		http.Error(w, `{"errorMessages":["Issue does not exist or you do not have permission to see it."]}`, http.StatusNotFound)
	}))
}

// changes renders changelog entries number from..to-1 as an API page.
func changes(from, to, total int, last bool, author string) string {
	var items []string
	for n := from; n < to; n++ {
		items = append(items, fmt.Sprintf(`{"id":"%d","author":%s,"created":"2024-01-01T00:%02d:%02d.000+0000",`+
			`"items":[{"field":"labels","fieldtype":"jira","from":null,"fromString":"","to":null,"toString":"x%d"}]}`, n, author, n/60, n%60, n))
	}
	return fmt.Sprintf(`{"startAt":%d,"maxResults":100,"total":%d,"isLast":%v,"values":[%s]}`, from, total, last, strings.Join(items, ","))
}

// busy is an issue with more changes than GET issue embeds: the newest embedded ones, whose total
// counts only themselves.
func busy(embedded, total int, author string) string {
	page := changes(1, embedded+1, total, false, author)
	values := page[strings.Index(page, `"values":`)+len(`"values":`) : len(page)-1]
	return `{"id":"1100","key":"ENG-100","fields":{"summary":"Busy","project":{"key":"ENG","name":"Engineering"},
	  "status":{"name":"Done","statusCategory":{"key":"done"}},"created":"2024-01-01T00:00:00.000+0000"},
	  "changelog":{"startAt":0,"maxResults":` + fmt.Sprint(embedded) + `,"total":` + fmt.Sprint(total) + `,"histories":` + values + `}}`
}

func testTracker(t *testing.T) *Tracker {
	t.Helper()
	srv := fakeJira(t)
	t.Cleanup(srv.Close)
	j, err := New([]byte(`{"kind":"jira","site":"example.atlassian.net"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	j.api = atlassian.NewClient(srv.URL, "https://example.atlassian.net", "jane.doe@example.com", "token")
	j.once.Do(func() {})
	return j
}

func TestAccountAndAssigned(t *testing.T) {
	j := testTracker(t)
	account, err := j.Account(context.Background())
	if err != nil || account != (tracker.Account{ID: "u-me", Name: "Jane Doe", Email: "jane.doe@example.com"}) {
		t.Fatalf("account %+v, err %v", account, err)
	}
	keys, err := j.Assigned(context.Background(), account)
	if err != nil || !reflect.DeepEqual(keys, []string{"ENG-1", "ENG-9"}) {
		t.Fatalf("assigned %v, err %v", keys, err)
	}
}

func TestIssues(t *testing.T) {
	j := testTracker(t)
	issues, missing, err := j.Issues(context.Background(), []string{"ENG-1", "OLD-5", "ENG-404", "ENG-9", "ENG-403", "ENG-409", "ENG-100"})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 3 || len(issues) != 3 {
		t.Fatalf("missing %+v, %d issues", missing, len(issues))
	}
	if m := missing[2]; missing[0] != (tracker.Missing{Key: "ENG-404", Reason: tracker.NotFound}) ||
		missing[1] != (tracker.Missing{Key: "ENG-403", Reason: tracker.Hidden}) ||
		m.Key != "ENG-409" || m.Reason != tracker.Failed || !strings.Contains(m.Error, "HTTP 409") {
		t.Errorf("missing, with reasons: %+v", missing)
	}
	check := func(what string, got, want any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", what, got, want)
		}
	}
	i := issues[0]
	check("key", i.Key, "ENG-1")
	check("url", i.URL, "https://example.atlassian.net/browse/ENG-1")
	check("project", i.Project, tracker.ProjectRef{Key: "ENG", Name: "Engineering"})
	check("type, summary", i.Type+": "+i.Summary, "Story: Speed up search")
	check("description", i.Description, "Make it **fast**")
	check("status", i.Status+" / "+i.StatusCategory, "In Progress / in_progress")
	check("priority", *i.Priority, "High")
	check("resolution", i.Resolution, (*string)(nil))
	check("labels, components, versions", [][]string{i.Labels, i.Components, i.FixVersions}, [][]string{{"backend"}, {"api"}, {"1.0"}})
	check("sprints", i.Sprints, []tracker.Sprint{{Name: "Sprint 1", State: "closed", StartAt: ptr("2024-01-01T09:00:00+00:00"),
		EndAt: ptr("2024-01-14T17:00:00+00:00"), CompletedAt: ptr("2024-01-15T10:00:00+00:00")}})
	check("estimate", *i.Estimate, 3.0)
	check("team", *i.Team, "Otters")
	check("parent", *i.Parent, tracker.IssueRef{Key: "ENG-0", Summary: "Search epic", Type: "Epic", Status: "Done"})
	check("links", i.Links, []tracker.IssueLink{{Relation: "blocks", IssueRef: tracker.IssueRef{Key: "ENG-2", Summary: "Ship it", Type: "Task", Status: "To Do"}}})
	check("people", []tracker.User{*i.Assignee, *i.Reporter, *i.Creator}, []tracker.User{{ID: "u-me", Name: "Jane Doe"}, {ID: "u-2", Name: "Other Person"}, {ID: "u-2", Name: "Other Person"}})
	check("times", []string{i.CreatedAt, i.UpdatedAt, *i.DueDate}, []string{"2024-01-02T10:00:00+00:00", "2024-01-05T08:30:00+00:00", "2024-02-01"})
	check("attachments", i.Attachments, []tracker.Attachment{{ID: "a1", Filename: "trace.txt", Size: 12, MimeType: "text/plain",
		Author: &tracker.User{ID: "u-me", Name: "Jane Doe"}, CreatedAt: "2024-01-03T00:00:00+00:00"}})

	// The issue response held one of two comments and none of the history: both were paged in.
	if len(i.Comments) != 2 || i.Comments[0].ID != "c1" || i.Comments[1].Body != "Make it **fast**" {
		t.Errorf("comments, oldest first, as Markdown: %+v", i.Comments)
	}
	if len(i.History) != 2 || i.History[0].Items[0].Field != "assignee" || *i.History[0].Items[0].ToID != "u-me" ||
		*i.History[1].Items[0].To != "In Progress" {
		t.Errorf("history: %+v", i.History)
	}
	check("worklogs", i.Worklogs, []tracker.Worklog{{ID: "w1", Author: &tracker.User{ID: "u-me", Name: "Jane Doe"},
		StartedAt: "2024-01-03T09:00:00+00:00", Seconds: 3600}})

	// Fields: everything else by name, rich text as Markdown, noise and the record's own fields gone.
	check("fields", i.Fields, map[string]any{
		"Incident Impact Type":      map[string]any{"value": "None", "id": "1"},
		"Notes":                     "Make it **fast**",
		"Notes (customfield_10400)": "plain notes",
	})

	m := issues[1]
	check("moved issue", m.Key+" "+strings.Join(m.PreviousKeys, ","), "ENG-9 OLD-5")
	check("moved issue resolved", *m.ResolvedAt, "2023-06-01T00:00:00+00:00")
	check("worklogs kept when their endpoint 404s", len(m.Worklogs), 1)

	// A full embedded page of changes means there are older ones: the whole history is paged in.
	b := issues[2]
	if len(b.History) != 101 || b.History[0].ID != "0" || b.History[100].ID != "100" {
		t.Errorf("busy issue history: %d changes, first %q", len(b.History), b.History[0].ID)
	}
}

func TestAssignedNeedsKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"issues":[{"id":"1001"}]}`))
	}))
	defer srv.Close()
	j, _ := New([]byte(`{"site":"example.atlassian.net"}`), "")
	j.api = atlassian.NewClient(srv.URL, "https://example.atlassian.net", "e", "t")
	j.once.Do(func() {})
	if _, err := j.Assigned(context.Background(), tracker.Account{ID: "u-me"}); err == nil {
		t.Error("issues without keys must fail the search, not vanish")
	}
}

func ptr[T any](v T) *T { return &v }
