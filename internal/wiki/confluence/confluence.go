// Package confluence is the wiki adapter for Confluence Cloud: a CQL search for the account's
// content (REST API v1, the only one with CQL), then each page's and blog post's body (ADF,
// rendered as Markdown) and versions (REST API v2).
package confluence

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/marleypowell/footprint/internal/atlassian"
	"github.com/marleypowell/footprint/internal/atlassian/adf"
	"github.com/marleypowell/footprint/internal/logx"
	"github.com/marleypowell/footprint/internal/par"
	"github.com/marleypowell/footprint/internal/wiki"
)

// contentTypes are the CQL content types searched, with their REST API v2 collections. Pages (live
// docs included) and blog posts have bodies and version histories; the others only their owner and
// current version.
var contentTypes = map[string]string{
	"page": "pages", "blogpost": "blogposts", "whiteboard": "whiteboards", "database": "databases",
	"folder": "folders", "embed": "embeds",
}

const (
	parallelism = 6
	// maxPages bounds a cursor loop, in case a next link never runs out.
	maxPages = 10_000
)

// searchParams are sent with every search page. CQL leaves out archived pages and spaces unless
// asked, and the user wrote those too.
var searchParams = url.Values{
	"limit":                 {"100"},
	"expand":                {"content.space,content.history,content.version,content.ancestors,content.metadata.labels"},
	"cqlcontext":            {`{"contentStatuses":["current","archived"]}`},
	"includeArchivedSpaces": {"true"},
}

// Wiki is one Confluence Cloud site (config: {"kind": "confluence", "site": "example.atlassian.net"}).
type Wiki struct {
	site string

	once   sync.Once
	api    *atlassian.Client
	apiErr error
}

var _ wiki.Wiki = (*Wiki)(nil)

// New returns the adapter. settings is the wiki config section.
func New(settings json.RawMessage) (*Wiki, error) {
	var s struct {
		Site string `json:"site"`
	}
	if len(settings) > 0 {
		if err := json.Unmarshal(settings, &s); err != nil {
			return nil, fmt.Errorf("confluence settings: %w", err)
		}
	}
	return &Wiki{site: s.Site}, nil
}

func (w *Wiki) client(ctx context.Context) (*atlassian.Client, error) {
	w.once.Do(func() {
		if w.site == "" {
			w.apiErr = errors.New(`confluence: wiki.site is not set (e.g. "example.atlassian.net")`)
			return
		}
		w.api, w.apiErr = atlassian.Connect(ctx, w.site, atlassian.Confluence)
	})
	return w.api, w.apiErr
}

type rawUser struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	PublicName  string `json:"publicName"`
	Email       string `json:"email"`
}

func (u rawUser) name() string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.PublicName
}

func (w *Wiki) Account(ctx context.Context) (wiki.Account, error) {
	api, err := w.client(ctx)
	if err != nil {
		return wiki.Account{}, err
	}
	var me rawUser
	if err := api.Get(ctx, "/wiki/rest/api/user/current", nil, &me); err != nil {
		return wiki.Account{}, err
	}
	return wiki.Account{ID: me.AccountID, Name: me.name(), Email: me.Email}, nil
}

// Pages searches for content the account created or edited (CQL's contributor covers both; creator
// is added in case), then loads the bodies and versions of the pages and blog posts.
func (w *Wiki) Pages(ctx context.Context, account wiki.Account) ([]wiki.Page, error) {
	if account.ID == "" {
		return nil, errors.New("confluence: the account has no ID")
	}
	api, err := w.client(ctx)
	if err != nil {
		return nil, err
	}
	id := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(account.ID)
	types := make([]string, 0, len(contentTypes))
	for t := range contentTypes {
		types = append(types, t)
	}
	sort.Strings(types)
	cql := fmt.Sprintf(`(creator = "%s" OR contributor = "%s") AND type IN (%s) ORDER BY created ASC`,
		id, id, strings.Join(types, ", "))
	found, err := search(ctx, api, cql)
	if err != nil {
		return nil, err
	}
	names := &userNames{byID: map[string]string{}}
	pages := make([]wiki.Page, len(found))
	for i, r := range found {
		pages[i] = fromSearch(api.Site(), r, names)
	}
	logx.Printf("confluence: %d pages and other content created or edited by %s", len(pages), account.Name)
	var done atomic.Int64
	err = par.ForEach(ctx, parallelism, pages, func(ctx context.Context, i int, p wiki.Page) error {
		if contentTypes[p.Type] == "" {
			return nil
		}
		if err := loadPage(ctx, api, &pages[i]); err != nil {
			return fmt.Errorf("%s %s: %w", p.Type, p.ID, err)
		}
		if n := done.Add(1); n%100 == 0 {
			logx.Printf("confluence: loaded %d pages", n)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	names.resolve(ctx, api, pages)
	return pages, nil
}

type rawContent struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Status  string `json:"status"`
	Title   string `json:"title"`
	Space   *struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"space"`
	History *struct {
		CreatedBy   *rawUser `json:"createdBy"`
		CreatedDate string   `json:"createdDate"`
	} `json:"history"`
	Version *struct {
		By        *rawUser `json:"by"`
		When      string   `json:"when"`
		Number    int      `json:"number"`
		Message   string   `json:"message"`
		MinorEdit bool     `json:"minorEdit"`
	} `json:"version"`
	Ancestors []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"ancestors"`
	Metadata *struct {
		Labels struct {
			Results []struct {
				Name string `json:"name"`
			} `json:"results"`
		} `json:"labels"`
	} `json:"metadata"`
	Links struct {
		WebUI string `json:"webui"`
	} `json:"_links"`
}

type searchResult struct {
	Content               *rawContent `json:"content"`
	URL                   string      `json:"url"`
	LastModified          string      `json:"lastModified"`
	ResultGlobalContainer struct {
		Title      string `json:"title"`
		DisplayURL string `json:"displayUrl"`
	} `json:"resultGlobalContainer"`
}

// search runs a CQL query to the end, following the cursor links. A page can come back short, even
// empty, when results the account may not see are filtered out, so only the links say when to stop.
func search(ctx context.Context, api *atlassian.Client, cql string) ([]searchResult, error) {
	var out []searchResult
	seen := map[string]bool{}
	first := url.Values{"cql": {cql}}
	for k, v := range searchParams {
		first[k] = v
	}
	err := follow(ctx, api, "/wiki/rest/api/search?"+first.Encode(),
		func(data []byte) (string, error) {
			var page struct {
				Results []searchResult `json:"results"`
				Links   struct {
					Next string `json:"next"`
				} `json:"_links"`
			}
			if err := json.Unmarshal(data, &page); err != nil {
				return "", err
			}
			for _, r := range page.Results {
				if r.Content != nil && r.Content.ID != "" && !seen[r.Content.ID] {
					seen[r.Content.ID] = true
					out = append(out, r)
				}
			}
			return withSearchParams(atlassian.NextPath(page.Links.Next)), nil
		})
	return out, err
}

// follow GETs path, then each next path read returns, until there is none (or it stops advancing).
func follow(ctx context.Context, api *atlassian.Client, path string, read func([]byte) (next string, err error)) error {
	for n := 0; path != ""; n++ {
		if n == maxPages {
			return fmt.Errorf("%s: more than %d pages", path, maxPages)
		}
		var data json.RawMessage
		if err := api.Get(ctx, path, nil, &data); err != nil {
			return err
		}
		next, err := read(data)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if next == path {
			break
		}
		path = next
	}
	return nil
}

// withSearchParams makes sure a next-page link still sends every search parameter.
func withSearchParams(path string) string {
	if path == "" {
		return path
	}
	base, query, _ := strings.Cut(path, "?")
	q, err := url.ParseQuery(query)
	if err != nil {
		return path
	}
	added := false
	for k, v := range searchParams {
		if !q.Has(k) {
			q[k], added = v, true
		}
	}
	if !added {
		return path
	}
	return base + "?" + q.Encode()
}

func fromSearch(site string, r searchResult, names *userNames) wiki.Page {
	c := r.Content
	p := wiki.Page{ID: c.ID, Type: c.Type, Status: c.Status, Title: c.Title, Ancestors: []wiki.PageRef{},
		Labels: []string{}, Versions: []wiki.Version{}}
	if c.Subtype != "" {
		p.Subtype = &c.Subtype
	}
	if c.Space != nil {
		p.Space = wiki.Space{Key: c.Space.Key, Name: c.Space.Name}
	} else { // the space as the search result shows it: "/spaces/KEY"
		key := strings.TrimPrefix(r.ResultGlobalContainer.DisplayURL, "/spaces/")
		p.Space = wiki.Space{Key: strings.SplitN(key, "/", 2)[0], Name: r.ResultGlobalContainer.Title}
	}
	if web := cmp.Or(c.Links.WebUI, r.URL); web != "" {
		p.URL = site + "/wiki" + web
	}
	for _, a := range c.Ancestors {
		p.Ancestors = append(p.Ancestors, wiki.PageRef{ID: a.ID, Title: a.Title})
	}
	if c.Metadata != nil {
		for _, l := range c.Metadata.Labels.Results {
			p.Labels = append(p.Labels, l.Name)
		}
		sort.Strings(p.Labels)
	}
	if c.History != nil {
		p.CreatedAt, p.CreatedBy = atlassian.Time(c.History.CreatedDate), names.user(c.History.CreatedBy)
	}
	if v := c.Version; v != nil {
		p.Version, p.UpdatedAt, p.UpdatedBy = v.Number, atlassian.Time(v.When), names.user(v.By)
		p.Versions = []wiki.Version{{Number: v.Number, Author: p.UpdatedBy, CreatedAt: p.UpdatedAt, Message: v.Message, MinorEdit: v.MinorEdit}}
	} else if r.LastModified != "" {
		p.UpdatedAt = atlassian.Time(r.LastModified)
	}
	return p
}

// loadPage adds what REST API v2 knows beyond the search: the owner, and for pages and blog posts
// the body and every version.
func loadPage(ctx context.Context, api *atlassian.Client, p *wiki.Page) error {
	base := "/wiki/api/v2/" + contentTypes[p.Type] + "/" + url.PathEscape(p.ID)
	hasBody := p.Type == "page" || p.Type == "blogpost"
	var d struct {
		Subtype   string              `json:"subtype"`
		AuthorID  string              `json:"authorId"`
		OwnerID   string              `json:"ownerId"`
		CreatedAt atlassian.Timestamp `json:"createdAt"`
		Body      struct {
			ADF struct {
				Value json.RawMessage `json:"value"` // the document as a JSON string (or, defensively, inline)
			} `json:"atlas_doc_format"`
		} `json:"body"`
	}
	var params url.Values
	if hasBody {
		params = url.Values{"body-format": {"atlas_doc_format"}}
	}
	err := api.Get(ctx, base, params, &d)
	if atlassian.StatusCode(err) == http.StatusNotFound { // deleted since the search
		logx.Printf("confluence: %s %s (%q) is gone; keeping what the search returned", p.Type, p.ID, p.Title)
		return nil
	}
	if err != nil {
		return err
	}
	if p.Subtype == nil && d.Subtype != "" {
		p.Subtype = &d.Subtype
	}
	if owner := userRef(d.OwnerID); owner != nil {
		p.Owner = owner
	}
	if p.CreatedBy == nil {
		p.CreatedBy = userRef(d.AuthorID)
	}
	if p.CreatedAt == "" {
		p.CreatedAt = d.CreatedAt.String()
	}
	if !hasBody {
		return nil
	}
	if body, err := markdownBody(d.Body.ADF.Value); err != nil {
		logx.Printf("confluence: %s %s: unreadable body: %v", p.Type, p.ID, err)
	} else {
		p.Body = &body
	}
	var versions []wiki.Version
	err = follow(ctx, api, base+"/versions?limit=250", func(data []byte) (string, error) {
		var page struct {
			Results []struct {
				Number    int                 `json:"number"`
				AuthorID  string              `json:"authorId"`
				CreatedAt atlassian.Timestamp `json:"createdAt"`
				Message   string              `json:"message"`
				MinorEdit bool                `json:"minorEdit"`
			} `json:"results"`
			Links struct {
				Next string `json:"next"`
			} `json:"_links"`
		}
		if err := json.Unmarshal(data, &page); err != nil {
			return "", err
		}
		for _, v := range page.Results {
			versions = append(versions, wiki.Version{Number: v.Number, Author: userRef(v.AuthorID),
				CreatedAt: v.CreatedAt.String(), Message: v.Message, MinorEdit: v.MinorEdit})
		}
		return atlassian.NextPath(page.Links.Next), nil
	})
	if err != nil {
		return err
	}
	if len(versions) > 0 {
		sort.Slice(versions, func(i, j int) bool { return versions[i].Number < versions[j].Number })
		p.Versions = versions
		p.Version = max(p.Version, versions[len(versions)-1].Number)
	}
	return nil
}

// markdownBody renders a v2 body value: an ADF document as a JSON string, or empty for an empty page.
func markdownBody(value json.RawMessage) (string, error) {
	doc := []byte(value)
	var s string
	if err := json.Unmarshal(value, &s); err == nil {
		doc = []byte(s)
	}
	if len(doc) == 0 || string(doc) == "null" {
		return "", nil
	}
	return adf.Markdown(doc)
}

func userRef(id string) *wiki.User {
	if id == "" {
		return nil
	}
	return &wiki.User{ID: id}
}

// userNames collects display names: from the search results, then looked up for the rest.
type userNames struct {
	byID map[string]string
}

func (n *userNames) user(u *rawUser) *wiki.User {
	if u == nil || u.AccountID == "" {
		return nil
	}
	if name := u.name(); name != "" {
		n.byID[u.AccountID] = name
	}
	return &wiki.User{ID: u.AccountID, Name: u.name()}
}

// resolve fills in the names of every user on the pages, looking up the ones the search did not
// show. A failed lookup leaves names empty: the IDs are what identify people.
func (n *userNames) resolve(ctx context.Context, api *atlassian.Client, pages []wiki.Page) {
	var users []*wiki.User
	for i := range pages {
		p := &pages[i]
		users = append(users, p.CreatedBy, p.Owner, p.UpdatedBy)
		for j := range p.Versions {
			users = append(users, p.Versions[j].Author)
		}
	}
	var unknown []string
	seen := map[string]bool{}
	for _, u := range users {
		if u != nil && u.ID != "" && n.byID[u.ID] == "" && !seen[u.ID] {
			seen[u.ID] = true
			unknown = append(unknown, u.ID)
		}
	}
	sort.Strings(unknown)
	for start := 0; start < len(unknown); start += 100 {
		var resp struct {
			Results []rawUser `json:"results"`
		}
		chunk := unknown[start:min(start+100, len(unknown))]
		if err := api.Post(ctx, "/wiki/api/v2/users-bulk", map[string]any{"accountIds": chunk}, &resp); err != nil {
			logx.Printf("confluence: looking up %d user names: %v", len(unknown), err)
			break
		}
		for _, u := range resp.Results {
			if u.name() != "" {
				n.byID[u.AccountID] = u.name()
			}
		}
	}
	for _, u := range users {
		if u != nil && u.Name == "" {
			u.Name = n.byID[u.ID]
		}
	}
}
