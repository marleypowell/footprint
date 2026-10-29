package github

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/marleypowell/footprint/internal/codehost"
)

// A repository node from GraphQL becomes the neutral record: nested names flattened, languages
// with their sizes, topics as names. What GitHub leaves null stays nil (an empty repository has no
// default branch, a fork names its parent), an empty homepage is no homepage, and lists are never
// null.
func TestRepoNode(t *testing.T) {
	i64 := func(n int64) *int64 { return &n }
	tests := []struct {
		name, node string
		want       codehost.Repo
	}{
		{"everything set", `{
  "name": "pay", "nameWithOwner": "acme/pay", "url": "https://github.com/acme/pay",
  "description": "Payments service", "homepageUrl": "https://pay.acme.test",
  "isArchived": true, "isEmpty": false, "isFork": true, "isPrivate": true, "isTemplate": false, "isMirror": false,
  "visibility": "PRIVATE", "createdAt": "2019-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
  "pushedAt": "2023-12-01T00:00:00Z", "archivedAt": "2024-01-01T00:00:00Z", "diskUsage": 2048,
  "defaultBranchRef": {"name": "main"}, "primaryLanguage": {"name": "Go"},
  "languages": {"totalSize": 1100, "edges": [{"size": 1000, "node": {"name": "Go"}}, {"size": 100, "node": {"name": "Shell"}}]},
  "repositoryTopics": {"nodes": [{"topic": {"name": "payments"}}, {"topic": {"name": "api"}}]},
  "parent": {"nameWithOwner": "upstream/pay"}, "templateRepository": {"nameWithOwner": "acme/service-template"}
}`, codehost.Repo{
			Name: "pay", FullName: "acme/pay", URL: "https://github.com/acme/pay",
			Description: ptr("Payments service"), Homepage: ptr("https://pay.acme.test"), DefaultBranch: ptr("main"),
			Visibility: "PRIVATE", IsArchived: true, IsFork: true, ForkOf: ptr("upstream/pay"),
			GeneratedFromTemplate: ptr("acme/service-template"),
			CreatedAt:             "2019-01-01T00:00:00Z", UpdatedAt: "2024-01-01T00:00:00Z",
			PushedAt: ptr("2023-12-01T00:00:00Z"), ArchivedAt: ptr("2024-01-01T00:00:00Z"), DiskUsageKB: i64(2048),
			PrimaryLanguage: ptr("Go"),
			Languages:       []codehost.Language{{Name: "Go", Bytes: 1000}, {Name: "Shell", Bytes: 100}},
			Topics:          []string{"payments", "api"},
		}},
		{"an empty repository", `{
  "name": "scratch", "nameWithOwner": "acme/scratch", "url": "https://github.com/acme/scratch",
  "description": null, "homepageUrl": "", "isEmpty": true, "visibility": "INTERNAL",
  "createdAt": "2024-02-01T00:00:00Z", "updatedAt": "2024-02-01T00:00:00Z",
  "pushedAt": null, "archivedAt": null, "diskUsage": null,
  "defaultBranchRef": null, "primaryLanguage": null,
  "languages": {"totalSize": 0, "edges": []}, "repositoryTopics": {"nodes": []},
  "parent": null, "templateRepository": null
}`, codehost.Repo{
			Name: "scratch", FullName: "acme/scratch", URL: "https://github.com/acme/scratch",
			Visibility: "INTERNAL", IsEmpty: true, CreatedAt: "2024-02-01T00:00:00Z", UpdatedAt: "2024-02-01T00:00:00Z",
			Languages: []codehost.Language{}, Topics: []string{},
		}},
	}
	for _, tt := range tests {
		var n repoNode
		if err := json.Unmarshal([]byte(tt.node), &n); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got := n.repo(); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: repo() =\n %+v\nwant\n %+v", tt.name, got, tt.want)
		}
	}
}

// Repos pages through the org's repositories 50 at a time, passing each page's end cursor to the
// next request, until GitHub says there is no next page.
func TestReposPaginates(t *testing.T) {
	pages := map[any]string{
		nil: `{"data": {"organization": {"repositories": {"totalCount": 3,
  "pageInfo": {"hasNextPage": true, "endCursor": "c1"},
  "nodes": [{"name": "api", "nameWithOwner": "acme/api"}, {"name": "pay", "nameWithOwner": "acme/pay"}]}}}}`,
		"c1": `{"data": {"organization": {"repositories": {"totalCount": 3,
  "pageInfo": {"hasNextPage": false, "endCursor": "c2"},
  "nodes": [{"name": "web", "nameWithOwner": "acme/web"}]}}}}`,
	}
	h, requests := fakeGraphQL(t, func(req gqlRequest) string { return pages[req.Variables["cursor"]] })
	repos, err := fakeHost(t, h).Repos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range repos {
		names = append(names, r.FullName)
	}
	if want := []string{"acme/api", "acme/pay", "acme/web"}; !reflect.DeepEqual(names, want) {
		t.Errorf("Repos() = %v, want %v", names, want)
	}
	var vars []map[string]any
	for _, r := range *requests {
		if r.Query != reposQuery {
			t.Errorf("Repos() sent query %q, want reposQuery", r.Query)
		}
		vars = append(vars, r.Variables)
	}
	if want := []map[string]any{{"org": "acme", "cursor": nil}, {"org": "acme", "cursor": "c1"}}; !reflect.DeepEqual(vars, want) {
		t.Errorf("Repos() sent variables %v, want %v", vars, want)
	}
}

// An org the token cannot see comes back as a null organization (with a NOT_FOUND error beside
// it), which Repos reports rather than returning no repositories.
func TestReposUnknownOrg(t *testing.T) {
	h, _ := fakeGraphQL(t, func(gqlRequest) string {
		return `{"data": {"organization": null}, "errors": [{"type": "NOT_FOUND", "message": "Could not resolve to an Organization with the login of 'acme'."}]}`
	})
	repos, err := fakeHost(t, h).Repos(context.Background())
	want := "github: organization acme not found or not visible to this token"
	if err == nil || err.Error() != want || repos != nil {
		t.Errorf("Repos() = (%v, %v), want error %q", repos, err, want)
	}
}
