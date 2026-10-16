package scrape

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
)

// Repo is one org repository's GitHub metadata (<data>/org/repos.json).
type Repo struct {
	Name                  string     `json:"name"`
	FullName              string     `json:"full_name"`
	URL                   string     `json:"url"`
	Description           *string    `json:"description"`
	Homepage              *string    `json:"homepage"`
	DefaultBranch         *string    `json:"default_branch"`
	Visibility            string     `json:"visibility"`
	IsArchived            bool       `json:"is_archived"`
	IsEmpty               bool       `json:"is_empty"`
	IsFork                bool       `json:"is_fork"`
	IsTemplate            bool       `json:"is_template"`
	IsMirror              bool       `json:"is_mirror"`
	ForkOf                *string    `json:"fork_of"`
	GeneratedFromTemplate *string    `json:"generated_from_template"`
	CreatedAt             string     `json:"created_at"`
	UpdatedAt             string     `json:"updated_at"`
	PushedAt              *string    `json:"pushed_at"`
	ArchivedAt            *string    `json:"archived_at"`
	DiskUsageKB           *int64     `json:"disk_usage_kb"`
	PrimaryLanguage       *string    `json:"primary_language"`
	Languages             []Language `json:"languages"`
	Topics                []string   `json:"topics"`
}

type Language struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

func (r Repo) defaultBranch() string { return deref(r.DefaultBranch) }

const reposQuery = `
query($org: String!, $cursor: String) {
  organization(login: $org) {
    repositories(first: 50, after: $cursor, orderBy: {field: NAME, direction: ASC}) {
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

func (n repoNode) repo() Repo {
	r := Repo{
		Name: n.Name, FullName: n.NameWithOwner, URL: n.URL, Description: n.Description,
		Visibility: n.Visibility, IsArchived: n.IsArchived, IsEmpty: n.IsEmpty, IsFork: n.IsFork,
		IsTemplate: n.IsTemplate, IsMirror: n.IsMirror, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
		PushedAt: n.PushedAt, ArchivedAt: n.ArchivedAt, DiskUsageKB: n.DiskUsage,
		Languages: []Language{}, Topics: []string{},
	}
	if deref(n.HomepageURL) != "" {
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
		r.Languages = append(r.Languages, Language{Name: e.Node.Name, Bytes: e.Size})
	}
	for _, t := range n.RepositoryTopics.Nodes {
		r.Topics = append(r.Topics, t.Topic.Name)
	}
	return r
}

func (h *harness) inventory(ctx context.Context) error {
	gh, err := h.github(ctx)
	if err != nil {
		return err
	}
	var repos []Repo
	var cursor *string
	for {
		var data struct {
			Organization *struct {
				Repositories struct {
					TotalCount int        `json:"totalCount"`
					PageInfo   pageInfo   `json:"pageInfo"`
					Nodes      []repoNode `json:"nodes"`
				} `json:"repositories"`
			} `json:"organization"`
		}
		if err := gh.graphql(ctx, reposQuery, map[string]any{"org": h.cfg.Org, "cursor": cursor}, &data); err != nil {
			return err
		}
		if data.Organization == nil {
			return errors.New("organization " + h.cfg.Org + " not found or not visible to this token")
		}
		page := data.Organization.Repositories
		for _, n := range page.Nodes {
			repos = append(repos, n.repo())
		}
		logf("inventory: %d/%d repos", len(repos), page.TotalCount)
		if !page.PageInfo.HasNextPage {
			break
		}
		cursor = page.PageInfo.EndCursor
	}
	sort.SliceStable(repos, func(i, j int) bool { return strings.ToLower(repos[i].Name) < strings.ToLower(repos[j].Name) })
	if err := writeJSON(filepath.Join(h.raw, "org", "repos.json"), repos); err != nil {
		return err
	}
	logf("inventory: wrote %d repos", len(repos))
	return nil
}
