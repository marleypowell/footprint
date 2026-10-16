package scrape

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
)

const (
	githubAPI   = "https://api.github.com"
	maxAttempts = 6
	// searchPause keeps the REST search API under its 30 requests/minute limit.
	searchPause = 2100 * time.Millisecond
)

// githubClient calls the GitHub API with GH_TOKEN, GITHUB_TOKEN, or the gh CLI's token.
type githubClient struct {
	http  *http.Client
	token string
}

func newGitHubClient(ctx context.Context) (*githubClient, error) {
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
	return &githubClient{http: &http.Client{Timeout: 5 * time.Minute}, token: token}, nil
}

// request sends an API request, retrying network errors, server errors and rate limits.
func (c *githubClient) request(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	for attempt := 1; ; attempt++ {
		data, wait, err := c.try(ctx, method, path, body, attempt)
		if err == nil || wait == 0 || attempt == maxAttempts {
			return data, err
		}
		logf("github: %v; retrying in %s", err, wait)
		if err := sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

// try makes one attempt. A non-zero wait means the failure is transient and worth retrying.
func (c *githubClient) try(ctx context.Context, method, path string, body []byte, attempt int) ([]byte, time.Duration, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, githubAPI+path, reader)
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

func (c *githubClient) get(ctx context.Context, path string, params url.Values, out any) error {
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
func (c *githubClient) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
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
				logf("github: graphql: %s; retrying in %s", resp.Errors[0].Message, backoff(attempt))
				if err := sleep(ctx, backoff(attempt)); err != nil {
					return err
				}
				continue
			}
			if len(resp.Data) == 0 || string(resp.Data) == "null" {
				return fmt.Errorf("graphql: %s", resp.Errors[0].Message)
			}
			msg, _ := json.Marshal(resp.Errors)
			logf("graphql errors: %s", truncate(string(msg), 500))
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

// commitSearch is a page of REST commit search results.
type commitSearch struct {
	TotalCount int `json:"total_count"`
	Items      []struct {
		SHA        string `json:"sha"`
		Repository struct {
			Name string `json:"name"`
		} `json:"repository"`
		Commit struct {
			Author struct {
				Name  string `json:"name"`
				Email string `json:"email"`
				Date  string `json:"date"`
			} `json:"author"`
			Message string `json:"message"`
		} `json:"commit"`
	} `json:"items"`
}

func (c *githubClient) searchCommits(ctx context.Context, params url.Values) (*commitSearch, error) {
	if err := sleep(ctx, searchPause); err != nil {
		return nil, err
	}
	var page commitSearch
	if err := c.get(ctx, "/search/commits", params, &page); err != nil {
		return nil, err
	}
	return &page, nil
}
