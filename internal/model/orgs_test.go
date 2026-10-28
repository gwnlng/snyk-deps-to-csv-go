package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gwnlng/snyk-deps-to-csv-go/internal/client"
)

type orgPage struct {
	ids  []string
	next string
}

func orgsBody(p orgPage) map[string]any {
	data := make([]map[string]any, 0, len(p.ids))
	for _, id := range p.ids {
		data = append(data, map[string]any{
			"type": "org", "id": id,
			"attributes": map[string]any{"name": "name-" + id, "slug": "slug-" + id},
		})
	}
	links := map[string]any{}
	if p.next != "" {
		links["next"] = p.next
	}
	return map[string]any{"data": data, "links": links}
}

func newClientFor(url string) *client.Client {
	return client.NewClient(url, "", "tok", 10, client.Filters{}, 600000, 100000)
}

func TestListGroupOrgsFollowsNextLinks(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.RawQuery)
		mu.Unlock()
		if r.Header.Get("Authorization") != "token tok" || r.URL.Path != "/rest/groups/g1/orgs" {
			t.Errorf("bad auth/path: %q %q", r.Header.Get("Authorization"), r.URL.Path)
		}
		if r.URL.Query().Get("version") != restVersion || r.URL.Query().Get("limit") != "100" {
			t.Errorf("bad query: %s", r.URL.RawQuery)
		}
		var p orgPage
		switch r.URL.Query().Get("starting_after") {
		case "":
			p = orgPage{ids: []string{"o1", "o2"}, next: "/rest/groups/g1/orgs?version=" + restVersion + "&limit=100&starting_after=o2"}
		case "o2":
			p = orgPage{ids: []string{"o3"}, next: "/rest/groups/g1/orgs?version=" + restVersion + "&limit=100&starting_after=o3"}
		default:
			p = orgPage{ids: []string{"o4"}}
		}
		_ = json.NewEncoder(w).Encode(orgsBody(p))
	}))
	defer srv.Close()

	orgs, err := ListGroupOrgs(context.Background(), newClientFor(srv.URL), "g1")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range orgs {
		got = append(got, o.ID)
		if o.Name != "name-"+o.ID || o.Slug != "slug-"+o.ID {
			t.Errorf("attributes not mapped: %+v", o)
		}
	}
	if strings.Join(got, ",") != "o1,o2,o3,o4" || len(queries) != 3 {
		t.Fatalf("ids=%v requests=%d", got, len(queries))
	}
}

func TestListGroupOrgsRejectsForeignHostNextLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(orgsBody(orgPage{ids: []string{"o1"}, next: "http://evil.example/rest/groups/g1/orgs?x=1"}))
	}))
	defer srv.Close()

	if _, err := ListGroupOrgs(context.Background(), newClientFor(srv.URL), "g1"); err == nil ||
		!strings.Contains(err.Error(), "different host") {
		t.Fatalf("err=%v, want different-host rejection", err)
	}
}

func TestListGroupOrgsErrors(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"http 403": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) },
		"empty group": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(orgsBody(orgPage{}))
		},
		"pagination loop": func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(orgsBody(orgPage{ids: []string{"o1"}, next: r.URL.RequestURI()}))
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			if _, err := ListGroupOrgs(context.Background(), newClientFor(srv.URL), "g1"); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
