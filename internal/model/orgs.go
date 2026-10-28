package model

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/gwnlng/snyk-deps-to-csv-go/internal/client"
)

// restVersion is the Snyk REST API version sent as the "version" query
// parameter on REST calls.
const restVersion = "2026-03-25"

const orgsPageLimit = 100

// Org is a Snyk organization within a group.
type Org struct {
	ID   string
	Name string
	Slug string
}

type orgsResponse struct {
	Data []struct {
		ID         string `json:"id"`
		Attributes struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"attributes"`
	} `json:"data"`
	Links struct {
		Next string `json:"next"`
	} `json:"links"`
}

// ListGroupOrgs returns every organization in the group, following the
// JSON:API "links.next" pagination to the last page.
func ListGroupOrgs(ctx context.Context, c *client.Client, groupID string) ([]Org, error) {
	q := url.Values{}
	q.Set("version", restVersion)
	q.Set("limit", fmt.Sprint(orgsPageLimit))
	next := fmt.Sprintf("%s/rest/groups/%s/orgs?%s", c.Host(), url.PathEscape(groupID), q.Encode())

	var orgs []Org
	seen := map[string]bool{}
	for page := 1; next != ""; page++ {
		if seen[next] {
			return nil, fmt.Errorf("orgs page %d: pagination loop at %s", page, next)
		}
		seen[next] = true

		var r orgsResponse
		if err := c.GetJSON(ctx, next, &r); err != nil {
			return nil, fmt.Errorf("list orgs page %d: %w", page, err)
		}
		for _, d := range r.Data {
			orgs = append(orgs, Org{ID: d.ID, Name: d.Attributes.Name, Slug: d.Attributes.Slug})
		}
		var err error
		if next, err = resolveNext(c.Host(), r.Links.Next); err != nil {
			return nil, err
		}
	}
	if len(orgs) == 0 {
		return nil, fmt.Errorf("no organizations found in group %q", groupID)
	}
	return orgs, nil
}

// resolveNext turns a "links.next" value into an absolute URL on the
// configured host. Absolute links to any other host are rejected so the API
// token is never sent elsewhere.
func resolveNext(host, link string) (string, error) {
	if link == "" {
		return "", nil
	}
	base, err := url.Parse(host + "/")
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(link)
	if err != nil {
		return "", fmt.Errorf("invalid next link %q: %w", link, err)
	}
	if ref.IsAbs() && !strings.EqualFold(ref.Host, base.Host) {
		return "", fmt.Errorf("refusing next link to a different host: %q", link)
	}
	return base.ResolveReference(ref).String(), nil
}
