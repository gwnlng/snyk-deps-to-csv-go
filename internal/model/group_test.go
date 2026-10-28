package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gwnlng/snyk-deps-to-csv-go/internal/client"
)

// groupServer serves a two-page org listing (ids) and per-org dependencies:
// each org has depsPerOrg dependencies, except "bad" which returns 403.
func groupServer(t *testing.T, ids []string, depsPerOrg int, hits *atomic.Int32) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch {
		case strings.HasPrefix(r.URL.Path, "/rest/groups/g1/orgs"):
			if r.URL.Query().Get("starting_after") == "" && len(ids) > 1 {
				_ = json.NewEncoder(w).Encode(orgsBody(orgPage{
					ids:  ids[:1],
					next: "/rest/groups/g1/orgs?version=" + restVersion + "&limit=100&starting_after=x",
				}))
				return
			}
			rest := ids
			if len(ids) > 1 {
				rest = ids[1:]
			}
			_ = json.NewEncoder(w).Encode(orgsBody(orgPage{ids: rest}))
		case strings.HasSuffix(r.URL.Path, "/dependencies"):
			parts := strings.Split(r.URL.Path, "/") // "", v1, org, <id>, dependencies
			if parts[3] == "bad" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			res := make([]client.Dependency, depsPerOrg)
			for i := range res {
				res[i] = client.Dependency{ID: parts[3] + "-dep", Name: "n", Version: "1"}
			}
			_ = json.NewEncoder(w).Encode(client.Page{Total: depsPerOrg, Results: res})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "\n")
}

func TestExportGroupWritesOneCSVPerOrg(t *testing.T) {
	var hits atomic.Int32
	srv := groupServer(t, []string{"org-a", "org-b", "org-c"}, 4, &hits)
	dir := filepath.Join(t.TempDir(), "out")

	results, err := ExportGroup(context.Background(), newClientFor(srv.URL), "g1", dir, t.Logf)
	if err != nil || len(results) != 3 {
		t.Fatalf("results=%d err=%v", len(results), err)
	}
	for _, id := range []string{"org-a", "org-b", "org-c"} {
		if n := countLines(t, filepath.Join(dir, "dependencies_"+id+".csv")); n != 5 { // header + 4
			t.Errorf("%s: %d lines, want 5", id, n)
		}
	}
}

func TestExportGroupContinuesAfterOrgFailure(t *testing.T) {
	var hits atomic.Int32
	srv := groupServer(t, []string{"org-a", "bad", "org-c"}, 2, &hits)
	dir := t.TempDir()

	results, err := ExportGroup(context.Background(), newClientFor(srv.URL), "g1", dir, t.Logf)
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("err=%v, want error naming org bad", err)
	}
	if len(results) != 3 || results[1].Err == nil || results[0].Err != nil || results[2].Err != nil {
		t.Fatalf("unexpected results: %+v", results)
	}
	for _, id := range []string{"org-a", "org-c"} {
		if _, statErr := os.Stat(filepath.Join(dir, "dependencies_"+id+".csv")); statErr != nil {
			t.Errorf("%s: %v", id, statErr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(dir, "dependencies_bad.csv")); !os.IsNotExist(statErr) {
		t.Errorf("failed org must not leave a CSV (stat err=%v)", statErr)
	}
}

func TestExportGroupRejectsUnsafeOrgID(t *testing.T) {
	var hits atomic.Int32
	srv := groupServer(t, []string{"../escape"}, 1, &hits)
	dir := t.TempDir()

	results, err := ExportGroup(context.Background(), newClientFor(srv.URL), "g1", dir, t.Logf)
	if err == nil || len(results) != 1 || results[0].Err == nil {
		t.Fatalf("err=%v results=%+v", err, results)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("no files expected, got %v", entries)
	}
}

// The limits are per token: the org listing and every org's export must draw
// on one shared budget, not a fresh one per org.
func TestExportGroupSharesRateLimiterAcrossOrgs(t *testing.T) {
	var hits atomic.Int32
	// 2 listing pages + 3 org exports (1 page each) = 5 requests needed.
	srv := groupServer(t, []string{"org-a", "org-b", "org-c"}, 3, &hits)
	c := client.NewClient(srv.URL, "", "tok", 10, client.Filters{}, 600000, 4) // budget of 4

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	results, err := ExportGroup(ctx, c, "g1", t.TempDir(), t.Logf)
	if err == nil {
		t.Fatal("expected failure once the shared budget is spent")
	}
	if got := hits.Load(); got != 4 {
		t.Fatalf("server saw %d requests, want exactly the shared budget of 4", got)
	}
	if len(results) == 0 || results[len(results)-1].Err == nil {
		t.Fatalf("last org should have failed: %+v", results)
	}
}
