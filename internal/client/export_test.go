package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func server(t *testing.T, total, advertised int, throttleFirst bool) (*httptest.Server, *atomic.Int32) {
	var calls atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.Header.Get("Authorization") != "token tok" || !strings.HasSuffix(r.URL.Path, "/org/o1/dependencies") {
			t.Errorf("bad auth/path: %q %q", r.Header.Get("Authorization"), r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		var body requestBody
		_ = json.Unmarshal(b, &body)
		if len(body.Filters.Languages) != 1 || body.Filters.Languages[0] != "npm" {
			t.Errorf("filters not sent: %s", b)
		}
		if len(body.Filters.Licenses) != 1 || body.Filters.Licenses[0] != "MIT" ||
			len(body.Filters.Severity) != 2 || !strings.Contains(string(b), `"severity":["high","critical"]`) {
			t.Errorf("licenses/severity not sent as arrays: %s", b)
		}
		if throttleFirst && n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		per, _ := strconv.Atoi(r.URL.Query().Get("perPage"))
		var res []Dependency
		for i := (page - 1) * per; i < page*per && i < total; i++ {
			res = append(res, Dependency{ID: fmt.Sprintf("d%d@1", i), Name: fmt.Sprintf("d%d", i), Version: "1"})
		}
		_ = json.NewEncoder(w).Encode(Page{Total: advertised, Results: res})
	})), &calls
}

func run(t *testing.T, srv *httptest.Server) (int, error) {
	c := NewClient(srv.URL, "o1", "tok", 10, Filters{Languages: []string{"npm"}, Licenses: []string{"MIT"}, Severity: []string{"high", "critical"}}, 6000, 100000)
	var sb strings.Builder
	return Export(context.Background(), c, &sb, t.Logf)
}

func TestExportAllPagesWithRetryOn429(t *testing.T) {
	srv, calls := server(t, 25, 25, true)
	defer srv.Close()
	n, err := run(t, srv)
	if err != nil || n != 25 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if calls.Load() != 4 { // 1 throttled + 3 pages
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestExportDetectsIncomplete(t *testing.T) {
	srv, _ := server(t, 20, 25, false)
	defer srv.Close()
	if _, err := run(t, srv); err == nil {
		t.Fatal("expected incomplete export error")
	}
}
