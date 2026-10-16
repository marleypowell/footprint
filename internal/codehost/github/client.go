package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/marleypowell/footprint/internal/logx"
)

const (
	apiURL      = "https://api.github.com"
	maxAttempts = 6
	// searchPause keeps the REST search API under its 30 requests/minute limit.
	searchPause = 2100 * time.Millisecond
)

// client calls the GitHub API with GH_TOKEN, GITHUB_TOKEN, or the gh CLI's token.
type client struct {
	http  *http.Client
	token string
}

func newClient(ctx context.Context) (*client, error) {
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		out, err := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", "github.com").Output()
		if err != nil {
			return nil, fmt.Errorf("no GitHub token: run `gh auth login` or set GH_TOKEN (gh auth token: %w)", err)
		}
		token = strings.TrimSpace(string(out))
	}
	return &client{http: &http.Client{Timeout: 5 * time.Minute}, token: token}, nil
}

// request sends an API request, retrying network errors, server errors and rate limits.
func (c *client) request(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	for attempt := 1; ; attempt++ {
		data, wait, err := c.try(ctx, method, path, body, attempt)
		if err == nil || wait == 0 || attempt == maxAttempts {
			return data, err
		}
		logx.Printf("github: %v; retrying in %s", err, wait)
		if err := sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

// try makes one attempt. A non-zero wait means the failure is transient and worth retrying.
func (c *client) try(ctx context.Context, method, path string, body []byte, attempt int) ([]byte, time.Duration, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiURL+path, reader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "footprint")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, backoff(attempt), err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, backoff(attempt), err
	}
	if resp.StatusCode < 300 {
		return data, 0, nil
	}
	err = fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, truncate(oneLine(string(data)), 300))
	return nil, retryWait(resp, data, attempt), err
}

// retryWait is how long to wait before retrying a failed response; zero means do not retry.
func retryWait(resp *http.Response, body []byte, attempt int) time.Duration {
	if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
		return time.Duration(secs+1) * time.Second
	}
	rateLimited := resp.StatusCode == http.StatusTooManyRequests ||
		(resp.StatusCode == http.StatusForbidden && bytes.Contains(bytes.ToLower(body), []byte("rate limit")))
	switch {
	case rateLimited && resp.Header.Get("X-RateLimit-Remaining") == "0":
		if reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			if d := time.Until(time.Unix(reset, 0)) + time.Second; d > 0 {
				return d
			}
		}
		return backoff(attempt)
	case rateLimited, resp.StatusCode >= 500:
		return backoff(attempt)
	}
	return 0
}

func backoff(attempt int) time.Duration {
	return min(time.Duration(attempt)*time.Minute, 5*time.Minute)
}

func (c *client) get(ctx context.Context, path string, params url.Values, out any) error {
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	data, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

type graphQLError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// graphql runs a query and decodes its data into out. A response carrying data alongside errors
// (one inaccessible field, say) is logged and used, as the gh CLI does.
func (c *client) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	for attempt := 1; ; attempt++ {
		data, err := c.request(ctx, http.MethodPost, "/graphql", body)
		if err != nil {
			return err
		}
		var resp struct {
			Data   json.RawMessage `json:"data"`
			Errors []graphQLError  `json:"errors"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return fmt.Errorf("graphql: decoding response: %w", err)
		}
		if len(resp.Errors) > 0 {
			if transientGraphQL(resp.Errors) && attempt < maxAttempts {
				logx.Printf("github: graphql: %s; retrying in %s", resp.Errors[0].Message, backoff(attempt))
				if err := sleep(ctx, backoff(attempt)); err != nil {
					return err
				}
				continue
			}
			if len(resp.Data) == 0 || string(resp.Data) == "null" {
				return fmt.Errorf("graphql: %s", resp.Errors[0].Message)
			}
			msg, _ := json.Marshal(resp.Errors)
			logx.Printf("github: graphql errors: %s", truncate(string(msg), 500))
		}
		return json.Unmarshal(resp.Data, out)
	}
}

func transientGraphQL(errs []graphQLError) bool {
	for _, e := range errs {
		m := strings.ToLower(e.Message)
		if e.Type == "RATE_LIMITED" || strings.Contains(m, "rate limit") || strings.Contains(m, "timeout") ||
			strings.Contains(m, "something went wrong") {
			return true
		}
	}
	return false
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// oneLine collapses whitespace runs (e.g. in an HTML error page) so a message fits one log line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

const dateLayout = "2006-01-02"

// tomorrow is the upper bound for search date ranges.
func tomorrow() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
}

// splitDays halves a date range for search slicing.
func splitDays(lo, hi time.Time) (time.Time, time.Time) {
	days := int(hi.Sub(lo).Hours() / 24)
	mid := lo.AddDate(0, 0, days/2)
	return mid, mid.AddDate(0, 0, 1)
}
