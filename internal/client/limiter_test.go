package client

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

// stubTransport records the time of every call that reaches the "network".
type stubTransport struct {
	mu    sync.Mutex
	times []time.Time
}

func (s *stubTransport) RoundTrip(*http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.times = append(s.times, time.Now())
	s.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

func (s *stubTransport) calls() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.times...)
}

func doReq(t *testing.T, ctx context.Context, rt http.RoundTripper) error {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://snyk.test/v1/org/o/dependencies", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err == nil {
		resp.Body.Close()
	}
	return err
}

// The pacer must space requests out, not let them burst.
func TestPacingSpreadsRequests(t *testing.T) {
	stub := &stubTransport{}
	lt := newLimitedTransport(stub, 600, 100000) // one request per 100ms
	for i := 0; i < 6; i++ {
		if err := doReq(t, context.Background(), lt); err != nil {
			t.Fatal(err)
		}
	}
	times := stub.calls()
	if len(times) != 6 {
		t.Fatalf("calls=%d", len(times))
	}
	for i := 1; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < 70*time.Millisecond {
			t.Errorf("gap %d = %v, want >= ~100ms (burst detected)", i, gap)
		}
	}
	if total := times[5].Sub(times[0]); total < 350*time.Millisecond {
		t.Errorf("6 requests spanned %v, want >= ~500ms", total)
	}
}

// The shipped defaults must stay inside Snyk's published tiers.
func TestDefaultLimitsUnderSnykTiers(t *testing.T) {
	if PaceRequestsPerMinute >= 150 {
		t.Errorf("PaceRequestsPerMinute=%d must be below the 150/min primary limit", PaceRequestsPerMinute)
	}
	if HourlyBudget > 1500 {
		t.Errorf("HourlyBudget=%d must not exceed the 1500/hour secondary limit", HourlyBudget)
	}
	// Steady-state pace must also fit the primary limit over any minute.
	if perMin := int(time.Minute / (time.Minute / PaceRequestsPerMinute)); perMin >= 150 {
		t.Errorf("effective pace %d/min exceeds primary limit", perMin)
	}
}

// Once the hourly budget is spent, no further request reaches the network.
func TestHourlyBudgetExhausted(t *testing.T) {
	stub := &stubTransport{}
	lt := newLimitedTransport(stub, 600000, 3)

	for i := 0; i < 3; i++ {
		if err := doReq(t, context.Background(), lt); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := doReq(t, ctx, lt); err == nil {
		t.Fatal("4th request should have been blocked by the hourly budget")
	}
	if n := len(stub.calls()); n != 3 {
		t.Fatalf("network calls=%d, want exactly 3", n)
	}
}

// A blocked limiter wait must be interruptible (Ctrl-C).
func TestContextCancelUnblocksWait(t *testing.T) {
	stub := &stubTransport{}
	lt := newLimitedTransport(stub, 600000, 1)
	if err := doReq(t, context.Background(), lt); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	err := doReq(t, ctx, lt)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("cancel took %v to unblock", d)
	}
	if n := len(stub.calls()); n != 1 {
		t.Fatalf("network calls=%d, want 1", n)
	}
}
