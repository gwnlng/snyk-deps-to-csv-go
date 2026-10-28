package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const testPerPage = 10

// tiered simulates Snyk's two rate-limit tiers (scaled down for tests):
// at most perWindow requests in any sliding window, and hourlyCap in total.
// Exceeding either returns HTTP 429.
type tiered struct {
	*httptest.Server
	mu        sync.Mutex
	stamps    []time.Time
	served    int
	throttled int
}

func (s *tiered) counts() (served, throttled int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.served, s.throttled
}

func newTiered(t *testing.T, totalItems, perWindow int, window time.Duration, hourlyCap int, retryAfter string) *tiered {
	s := &tiered{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		now := time.Now()
		kept := s.stamps[:0]
		for _, ts := range s.stamps {
			if now.Sub(ts) < window {
				kept = append(kept, ts)
			}
		}
		s.stamps = kept
		if len(s.stamps) >= perWindow || s.served >= hourlyCap {
			s.throttled++
			s.mu.Unlock()
			w.Header().Set("Retry-After", retryAfter)
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		s.stamps = append(s.stamps, now)
		s.served++
		s.mu.Unlock()

		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		per, _ := strconv.Atoi(r.URL.Query().Get("perPage"))
		var res []Dependency
		for i := (page - 1) * per; i < page*per && i < totalItems; i++ {
			res = append(res, Dependency{ID: fmt.Sprintf("d%d@1", i), Name: fmt.Sprintf("d%d", i), Version: "1"})
		}
		_ = json.NewEncoder(w).Encode(Page{Total: totalItems, Results: res})
	}))
	t.Cleanup(s.Close)
	return s
}

// scripted serves a fixed status sequence (last entry repeats) and counts hits.
type scripted struct {
	*httptest.Server
	mu   sync.Mutex
	hits int
}

func (s *scripted) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits
}

func newScripted(t *testing.T, retryAfter string, statuses ...int) *scripted {
	s := &scripted{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		i := s.hits
		s.hits++
		s.mu.Unlock()
		if i >= len(statuses) {
			i = len(statuses) - 1
		}
		if statuses[i] == http.StatusTooManyRequests {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(Page{Total: 1, Results: []Dependency{{ID: "a@1", Name: "a", Version: "1"}}})
	}))
	t.Cleanup(s.Close)
	return s
}

func newTestClient(url string, perMinute, perHour int) *Client {
	return NewClient(url, "o1", "tok", testPerPage, Filters{}, perMinute, perHour)
}

// Retries after a 429 must pass through the limiter and spend hourly budget.
func TestRetriesConsumeHourlyBudget(t *testing.T) {
	srv := newScripted(t, "0", http.StatusTooManyRequests)
	c := newTestClient(srv.URL, 600000, 3)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if _, err := c.FetchPage(ctx, 1); err == nil {
		t.Fatal("expected error once the hourly budget is spent")
	}
	if n := srv.count(); n != 3 {
		t.Fatalf("server hits=%d, want exactly 3 (budget)", n)
	}
}

func TestRecoversFrom429WithRetryAfter(t *testing.T) {
	srv := newScripted(t, "0", http.StatusTooManyRequests, http.StatusTooManyRequests, http.StatusOK)
	c := newTestClient(srv.URL, 600000, 100000)

	p, err := c.FetchPage(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Results) != 1 || srv.count() != 3 {
		t.Fatalf("results=%d hits=%d, want 1 result after 3 hits", len(p.Results), srv.count())
	}
}

// No Retry-After: retryablehttp's DefaultBackoff applies (>= 1s first wait).
func TestDefaultBackoffWithoutRetryAfter(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the default 1s backoff")
	}
	srv := newScripted(t, "", http.StatusTooManyRequests, http.StatusOK)
	c := newTestClient(srv.URL, 600000, 100000)

	start := time.Now()
	if _, err := c.FetchPage(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 900*time.Millisecond {
		t.Fatalf("elapsed %v, want >= ~1s default backoff", d)
	}
	if srv.count() != 2 {
		t.Fatalf("hits=%d, want 2", srv.count())
	}
}

// Persistent 429s: give up after RetryMax+1 attempts and fail the export.
func TestRetryExhaustion(t *testing.T) {
	srv := newScripted(t, "0", http.StatusTooManyRequests)
	c := newTestClient(srv.URL, 600000, 100000)

	if _, err := c.FetchPage(context.Background(), 1); err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if want := c.http.RetryMax + 1; srv.count() != want {
		t.Fatalf("hits=%d, want %d", srv.count(), want)
	}

	before := srv.count()
	var sb strings.Builder
	if _, err := Export(context.Background(), c, &sb, t.Logf); err == nil {
		t.Fatal("Export must fail (non-zero exit) when retries are exhausted")
	}
	if srv.count() != before+c.http.RetryMax+1 {
		t.Fatalf("export hits=%d, want %d", srv.count()-before, c.http.RetryMax+1)
	}
}

// Paced client against a server enforcing a window cap: never throttled,
// and every page is retrieved.
func TestExportStaysUnderPrimaryLimit(t *testing.T) {
	const pages = 15
	srv := newTiered(t, pages*testPerPage, 20, time.Second, 1000, "1")
	c := newTestClient(srv.URL, 600, 1000) // 10 req/s vs server cap 20/s

	var sb strings.Builder
	n, err := Export(context.Background(), c, &sb, t.Logf)
	if err != nil || n != pages*testPerPage {
		t.Fatalf("n=%d err=%v", n, err)
	}
	served, throttled := srv.counts()
	if throttled != 0 || served != pages {
		t.Fatalf("served=%d throttled=%d, want %d/0", served, throttled, pages)
	}
}

// Contrast: without pacing the server throttles, but retry/backoff still
// completes the export with every dependency.
func TestUnpacedClientHitsLimitButStillCompletes(t *testing.T) {
	if testing.Short() {
		t.Skip("waits on Retry-After")
	}
	const pages = 12
	srv := newTiered(t, pages*testPerPage, 5, time.Second, 1000, "1")
	c := newTestClient(srv.URL, 600000, 100000)

	var sb strings.Builder
	n, err := Export(context.Background(), c, &sb, t.Logf)
	if err != nil || n != pages*testPerPage {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, throttled := srv.counts(); throttled == 0 {
		t.Fatal("expected the unpaced client to be throttled at least once")
	}
}

// The secondary (hourly) budget is never overrun; the export fails loudly
// instead of silently truncating.
func TestExportStopsAtHourlyBudget(t *testing.T) {
	const pages = 15
	srv := newTiered(t, pages*testPerPage, 1000, time.Second, 12, "1")
	c := newTestClient(srv.URL, 600000, 12)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var sb strings.Builder
	n, err := Export(ctx, c, &sb, t.Logf)
	if err == nil {
		t.Fatal("expected error: budget allows only 12 of 15 pages")
	}
	if n >= pages*testPerPage {
		t.Fatalf("wrote %d rows, expected a partial export", n)
	}
	if served, throttled := srv.counts(); throttled != 0 || served != 12 {
		t.Fatalf("served=%d throttled=%d, want 12/0 (client must not exceed budget)", served, throttled)
	}
}
