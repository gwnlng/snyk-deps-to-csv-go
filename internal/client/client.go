package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/go-retryablehttp"
)

type Filters struct {
	Languages    []string `json:"languages,omitempty"`
	Projects     []string `json:"projects,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	Licenses     []string `json:"licenses,omitempty"`
	Severity     []string `json:"severity,omitempty"`
}

// apiBase normalises a user-supplied host to its V1 API root: a trailing "/"
// or an already-present "/v1" is dropped before "/v1" is appended.
func apiBase(raw string) string {
	return apiHost(raw) + "/v1"
}

// apiHost strips any trailing "/" and "/v1" so both the V1 and REST roots can
// be derived from the same host.
func apiHost(raw string) string {
	s := strings.TrimRight(raw, "/")
	s = strings.TrimSuffix(s, "/v1")
	return strings.TrimRight(s, "/")
}

type requestBody struct {
	Filters Filters `json:"filters"`
}

type ProjectRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Dependency struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Version        string       `json:"version"`
	LatestVersion  string       `json:"latestVersion"`
	Type           string       `json:"type"`
	IsDeprecated   bool         `json:"isDeprecated"`
	IssuesCritical int          `json:"issuesCritical"`
	IssuesHigh     int          `json:"issuesHigh"`
	IssuesMedium   int          `json:"issuesMedium"`
	IssuesLow      int          `json:"issuesLow"`
	Licenses       []License    `json:"licenses"`
	Projects       []ProjectRef `json:"projects"`
}

type License struct {
	ID string `json:"id"`
}

type Page struct {
	Total   int          `json:"total"`
	Results []Dependency `json:"results"`
}

type Client struct {
	http    *retryablehttp.Client
	host    string
	baseURL string
	orgID   string
	token   string
	perPage int
	filters Filters
}

func NewClient(baseURL, orgID, token string, perPage int, f Filters, perMinute, perHour int) *Client {
	rc := retryablehttp.NewClient()
	rc.RetryMax = 10
	// Default backoff (exponential, honours Retry-After on 429) and default
	// retry policy (429, 5xx, connection errors).
	rc.Backoff = retryablehttp.DefaultBackoff
	rc.CheckRetry = retryablehttp.DefaultRetryPolicy
	rc.Logger = nil
	rc.HTTPClient = &http.Client{
		Timeout:   60 * time.Second,
		Transport: newLimitedTransport(nil, perMinute, perHour),
	}
	return &Client{http: rc, host: apiHost(baseURL), baseURL: apiBase(baseURL), orgID: orgID, token: token, perPage: perPage, filters: f}
}

// ForOrg returns a client for another organization that shares this client's
// HTTP client, and therefore its rate limiters: Snyk's limits are per token,
// not per organization.
func (c *Client) ForOrg(orgID string) *Client {
	cp := *c
	cp.orgID = orgID
	return &cp
}

// FetchPage retrieves one 1-based page of dependencies.
func (c *Client) FetchPage(ctx context.Context, page int) (*Page, error) {
	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("perPage", strconv.Itoa(c.perPage))
	q.Set("sortBy", "dependency")
	q.Set("order", "asc")
	u := fmt.Sprintf("%s/org/%s/dependencies?%s", c.baseURL, url.PathEscape(c.orgID), q.Encode())

	body, err := json.Marshal(requestBody{Filters: c.filters})
	if err != nil {
		return nil, err
	}
	req, err := retryablehttp.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("page %d: %w", page, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("page %d: HTTP %d: %s", page, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var p Page
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return nil, fmt.Errorf("page %d: decode: %w", page, err)
	}
	return &p, nil
}

// Host returns the API host (scheme and authority, without any /v1 suffix).
func (c *Client) Host() string { return c.host }

// GetJSON performs an authenticated JSON:API GET of the absolute URL u and
// decodes the response body into out. It shares the client's rate limiters
// and retry policy.
func (c *Client) GetJSON(ctx context.Context, u string, out any) error {
	req, err := retryablehttp.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/vnd.api+json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}
