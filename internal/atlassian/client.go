// Package atlassian is the HTTP client that the Jira and Confluence adapters share: Atlassian Cloud
// REST calls authenticated with an account's API token, made on the site or, for scoped tokens,
// through the API gateway. Package adf renders the rich text those APIs return.
package atlassian

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/marleypowell/footprint/internal/logx"
)

// Product is one Atlassian Cloud product's REST API.
type Product struct {
	Name      string // for messages
	gateway   string // the product's segment of https://api.atlassian.com/ex/<product>/<cloudId>
	tokenEnv  string // a product-only token, checked before ATLASSIAN_API_TOKEN
	probePath string // a cheap authenticated GET, used to tell which base accepts the token
}

var (
	Jira       = Product{Name: "Jira", gateway: "jira", tokenEnv: "JIRA_API_TOKEN", probePath: "/rest/api/3/myself"}
	Confluence = Product{Name: "Confluence", gateway: "confluence", tokenEnv: "CONFLUENCE_API_TOKEN", probePath: "/wiki/rest/api/user/current"}
)

const (
	maxAttempts = 6
	gatewayURL  = "https://api.atlassian.com/ex/"
	tokenHelp   = "create one at https://id.atlassian.com/manage-profile/security/api-tokens"
)

// Env lists the environment variables Connect authenticates with: one of each group must be set.
func (p Product) Env() [][]string {
	return [][]string{{"ATLASSIAN_EMAIL"}, {p.tokenEnv, "ATLASSIAN_API_TOKEN"}}
}

// Client calls one product's REST API on one site. It is safe for concurrent use.
type Client struct {
	base       string // where API paths go: the site, or the gateway for the site's cloud ID
	site       string // https://<site>, for web links
	auth       string
	trimPrefix string // cut from paths: a gateway serving Confluence without the site's /wiki
	http       *http.Client
}

// Connect returns a client for product on site ("example.atlassian.net"), authenticated as
// ATLASSIAN_EMAIL with the product's own token (JIRA_API_TOKEN, CONFLUENCE_API_TOKEN) or else
// ATLASSIAN_API_TOKEN. Classic tokens work on the site and scoped tokens only through the API
// gateway, so when the site rejects the token Connect tries the gateway.
func Connect(ctx context.Context, site string, p Product) (*Client, error) {
	email, token := os.Getenv("ATLASSIAN_EMAIL"), os.Getenv(p.tokenEnv)
	if token == "" {
		token = os.Getenv("ATLASSIAN_API_TOKEN")
	}
	if email == "" || token == "" {
		return nil, fmt.Errorf("no Atlassian API token for %s: set ATLASSIAN_EMAIL and ATLASSIAN_API_TOKEN (or %s); %s",
			p.Name, p.tokenEnv, tokenHelp)
	}
	return connect(ctx, SiteURL(site), gatewayURL+p.gateway+"/", email, token, p)
}

func connect(ctx context.Context, site, gateway, email, token string, p Product) (*Client, error) {
	c := NewClient(site, site, email, token)
	siteErr := probe(ctx, c, p.probePath)
	if siteErr == nil {
		return c, nil
	}
	if !isAuthError(siteErr) {
		return nil, siteErr
	}
	var tenant struct {
		CloudID string `json:"cloudId"`
	}
	if err := NewClient(site, site, "", "").Get(ctx, "/_edge/tenant_info", nil, &tenant); err != nil || tenant.CloudID == "" {
		return nil, fmt.Errorf("%s rejected the API token for %s: %w", p.Name, email, siteErr)
	}
	g := NewClient(gateway+tenant.CloudID, site, email, token)
	err := probe(ctx, g, p.probePath)
	if StatusCode(err) == http.StatusNotFound && strings.HasPrefix(p.probePath, "/wiki/") {
		if g.trimPrefix = "/wiki"; probe(ctx, g, p.probePath) == nil {
			err = nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%s rejected the API token for %s on the site (%v) and through the API gateway (%w): "+
			"check ATLASSIAN_EMAIL, and that a scoped token has %s read scopes", p.Name, email, siteErr, err, p.Name)
	}
	return g, nil
}

// probe checks that the credentials identify an account: an API may answer a token it ignores as
// an anonymous user rather than with 401.
func probe(ctx context.Context, c *Client, path string) error {
	var me struct {
		AccountID string `json:"accountId"`
	}
	if err := c.Get(ctx, path, nil, &me); err != nil {
		return err
	}
	if me.AccountID == "" {
		return &StatusError{Method: http.MethodGet, Path: path, StatusCode: http.StatusUnauthorized,
			Body: "answered for an anonymous user: the token was not accepted"}
	}
	return nil
}

// NewClient returns a client that sends API paths to base and builds web links on site, without
// checking the credentials. With no email and token it sends no credentials.
func NewClient(base, site, email, token string) *Client {
	c := &Client{base: strings.TrimSuffix(base, "/"), site: strings.TrimSuffix(site, "/"), http: &http.Client{Timeout: 2 * time.Minute}}
	if email != "" || token != "" {
		c.auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(email+":"+token))
	}
	return c
}

// SiteURL turns a configured site ("example.atlassian.net", with or without a scheme) into its URL.
func SiteURL(site string) string {
	site = strings.TrimSuffix(strings.TrimSpace(site), "/")
	if strings.HasPrefix(site, "https://") || strings.HasPrefix(site, "http://") {
		return site
	}
	return "https://" + site
}

// Site is the site's URL, for web links.
func (c *Client) Site() string { return c.site }

// Get decodes the JSON response to a GET of path into out (nil: discard it). path may carry a
// query, as pagination links do; params are added to it.
func (c *Client) Get(ctx context.Context, path string, params url.Values, out any) error {
	if len(params) > 0 {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		path += sep + params.Encode()
	}
	return c.do(ctx, http.MethodGet, path, nil, out)
}

// Post sends body as JSON and decodes the response into out.
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, path, data, out)
}

// StatusError is an API response with a failure status.
type StatusError struct {
	Method, Path string
	StatusCode   int
	Body         string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// StatusCode is the HTTP status of an API failure, or 0 for any other error.
func StatusCode(err error) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.StatusCode
	}
	return 0
}

// isAuthError reports a site refusing a token. Atlassian answers a scoped token on the site with
// 401, 403, or even 404, on probe paths that exist for every account.
func isAuthError(err error) bool {
	switch StatusCode(err) {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return true
	}
	return false
}

// do sends a request, retrying network errors, server errors and rate limits.
func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	for attempt := 1; ; attempt++ {
		data, wait, retry, err := c.try(ctx, method, path, body, attempt)
		if err == nil {
			if out == nil || len(data) == 0 {
				return nil
			}
			if err := json.Unmarshal(data, out); err != nil {
				return fmt.Errorf("%s %s: decoding response: %w", method, path, err)
			}
			return nil
		}
		if !retry || attempt == maxAttempts {
			return err
		}
		logx.Printf("atlassian: %v; retrying in %s", err, wait)
		if err := sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// try makes one attempt. retry reports whether a failure is transient, and wait how long to wait
// before the next attempt.
func (c *Client) try(ctx context.Context, method, path string, body []byte, attempt int) ([]byte, time.Duration, bool, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+strings.TrimPrefix(path, c.trimPrefix), reader)
	if err != nil {
		return nil, 0, false, err
	}
	if c.auth != "" {
		req.Header.Set("Authorization", c.auth)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "footprint")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, false, ctx.Err()
		}
		return nil, backoff(attempt), true, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, backoff(attempt), true, err
	}
	if resp.StatusCode < 300 {
		return data, 0, false, nil
	}
	err = &StatusError{Method: method, Path: path, StatusCode: resp.StatusCode, Body: truncate(oneLine(string(data)), 300)}
	if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
		return nil, 0, false, err
	}
	if secs, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil {
		return nil, time.Duration(secs) * time.Second, true, err
	}
	return nil, backoff(attempt), true, err
}

// backoff is the wait before retry number attempt, when the API does not say.
var backoff = func(attempt int) time.Duration {
	return min(time.Duration(1<<attempt)*time.Second, time.Minute)
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// NextPath turns a pagination link from a response's _links.next into a path for Get. Confluence
// writes these relative to the /wiki context, relative to the site with /wiki included, or (through
// the gateway) after /ex/confluence/<cloud id>.
func NextPath(next string) string {
	if next == "" {
		return ""
	}
	if u, err := url.Parse(next); err == nil && u.IsAbs() {
		next = u.RequestURI()
	}
	path, _, _ := strings.Cut(next, "?")
	switch i := strings.Index(path, "/wiki/"); {
	case i == 0:
		return next
	case i > 0:
		return next[i:]
	}
	return "/wiki" + next
}

const timeLayout = "2006-01-02T15:04:05-07:00" // the dataset's UTC timestamps: "+00:00", not "Z"

// Time normalises an Atlassian timestamp (Jira's "2024-01-02T10:11:12.345+0100", or RFC 3339) to
// UTC. Anything else is returned unchanged.
func Time(s string) string {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(timeLayout)
		}
	}
	return s
}

// Timestamp is a JSON timestamp that Atlassian writes as an ISO 8601 string, or (some Confluence
// v2 content types) as epoch milliseconds. String() gives the UTC form Time does.
type Timestamp string

func (t *Timestamp) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch x := v.(type) {
	case string:
		*t = Timestamp(Time(x))
	case float64:
		*t = Timestamp(time.UnixMilli(int64(x)).UTC().Format(timeLayout))
	case nil:
		*t = ""
	default:
		return fmt.Errorf("timestamp: unexpected %T", v)
	}
	return nil
}

func (t Timestamp) String() string { return string(t) }

// TimePtr is Time for an optional timestamp; empty becomes nil.
func TimePtr(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	t := Time(*s)
	return &t
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
