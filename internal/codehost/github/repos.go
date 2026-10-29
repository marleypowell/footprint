package github

import (
	"context"
	"fmt"

	"github.com/marleypowell/footprint/internal/codehost"
	"github.com/marleypowell/footprint/internal/logx"
)

// reposQuery lists an owner's own repositories: an organization's, or a user's (not those others
// own that the user collaborates on).
const reposQuery = `
query($org: String!, $cursor: String) {
  repositoryOwner(login: $org) {
    __typename
    repositories(first: 50, after: $cursor, ownerAffiliations: [OWNER], orderBy: {field: NAME, direction: ASC}) {
      totalCount
      pageInfo { hasNextPage endCursor }
      nodes {
        name nameWithOwner url description homepageUrl
        isArchived isEmpty isFork isPrivate isTemplate isMirror visibility
        createdAt updatedAt pushedAt archivedAt diskUsage
        defaultBranchRef { name }
        primaryLanguage { name }
        languages(first: 25, orderBy: {field: SIZE, direction: DESC}) { totalSize edges { size node { name } } }
        repositoryTopics(first: 50) { nodes { topic { name } } }
        parent { nameWithOwner }
        templateRepository { nameWithOwner }
      }
    }
  }
}`

type pageInfo struct {
	HasNextPage bool    `json:"hasNextPage"`
	EndCursor   *string `json:"endCursor"`
}

type nameNode struct {
	Name string `json:"name"`
}

type ownerNode struct {
	NameWithOwner string `json:"nameWithOwner"`
}

type repoNode struct {
	Name             string     `json:"name"`
	NameWithOwner    string     `json:"nameWithOwner"`
	URL              string     `json:"url"`
	Description      *string    `json:"description"`
	HomepageURL      *string    `json:"homepageUrl"`
	IsArchived       bool       `json:"isArchived"`
	IsEmpty          bool       `json:"isEmpty"`
	IsFork           bool       `json:"isFork"`
	IsTemplate       bool       `json:"isTemplate"`
	IsMirror         bool       `json:"isMirror"`
	Visibility       string     `json:"visibility"`
	CreatedAt        string     `json:"createdAt"`
	UpdatedAt        string     `json:"updatedAt"`
	PushedAt         *string    `json:"pushedAt"`
	ArchivedAt       *string    `json:"archivedAt"`
	DiskUsage        *int64     `json:"diskUsage"`
	DefaultBranchRef *nameNode  `json:"defaultBranchRef"`
	PrimaryLanguage  *nameNode  `json:"primaryLanguage"`
	Parent           *ownerNode `json:"parent"`
	Template         *ownerNode `json:"templateRepository"`
	Languages        struct {
		Edges []struct {
			Size int64    `json:"size"`
			Node nameNode `json:"node"`
		} `json:"edges"`
	} `json:"languages"`
	RepositoryTopics struct {
		Nodes []struct {
			Topic nameNode `json:"topic"`
		} `json:"nodes"`
	} `json:"repositoryTopics"`
}

func (n repoNode) repo() codehost.Repo {
	r := codehost.Repo{
		Name: n.Name, FullName: n.NameWithOwner, URL: n.URL, Description: n.Description,
		Visibility: n.Visibility, IsArchived: n.IsArchived, IsEmpty: n.IsEmpty, IsFork: n.IsFork,
		IsTemplate: n.IsTemplate, IsMirror: n.IsMirror, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
		PushedAt: n.PushedAt, ArchivedAt: n.ArchivedAt, DiskUsageKB: n.DiskUsage,
		Languages: []codehost.Language{}, Topics: []string{},
	}
	if n.HomepageURL != nil && *n.HomepageURL != "" {
		r.Homepage = n.HomepageURL
	}
	if n.DefaultBranchRef != nil {
		r.DefaultBranch = &n.DefaultBranchRef.Name
	}
	if n.PrimaryLanguage != nil {
		r.PrimaryLanguage = &n.PrimaryLanguage.Name
	}
	if n.Parent != nil {
		r.ForkOf = &n.Parent.NameWithOwner
	}
	if n.Template != nil {
		r.GeneratedFromTemplate = &n.Template.NameWithOwner
	}
	for _, e := range n.Languages.Edges {
		r.Languages = append(r.Languages, codehost.Language{Name: e.Node.Name, Bytes: e.Size})
	}
	for _, t := range n.RepositoryTopics.Nodes {
		r.Topics = append(r.Topics, t.Topic.Name)
	}
	return r
}

func (h *Host) Repos(ctx context.Context) ([]codehost.Repo, error) {
	api, err := h.client(ctx)
	if err != nil {
		return nil, err
	}
	var repos []codehost.Repo
	var cursor *string
	for {
		var data struct {
			Owner *struct {
				Typename     string `json:"__typename"`
				Repositories struct {
					TotalCount int        `json:"totalCount"`
					PageInfo   pageInfo   `json:"pageInfo"`
					Nodes      []repoNode `json:"nodes"`
				} `json:"repositories"`
			} `json:"repositoryOwner"`
		}
		if err := api.graphql(ctx, reposQuery, map[string]any{"org": h.org, "cursor": cursor}, &data); err != nil {
			return nil, err
		}
		if data.Owner == nil {
			return nil, fmt.Errorf("github: %s is no organization or user this token can see", h.org)
		}
		page := data.Owner.Repositories
		for _, n := range page.Nodes {
			repos = append(repos, n.repo())
		}
		logx.Printf("github: %d/%d repos", len(repos), page.TotalCount)
		if !page.PageInfo.HasNextPage {
			return repos, nil
		}
		cursor = page.PageInfo.EndCursor
	}
}
