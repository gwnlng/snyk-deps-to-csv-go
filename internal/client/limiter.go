package client

import (
	"net/http"
	"time"

	"go.uber.org/ratelimit"
	"golang.org/x/time/rate"
)

const (
	// Snyk V1 limits: 150 req/min (primary), 1500 req/hour (secondary) per token.
	// Pace below the primary limit to leave headroom for other callers.
	PaceRequestsPerMinute = 120
	HourlyBudget          = 1500
)

// limitedTransport applies both limiters to every HTTP attempt. Because
// retryablehttp calls the transport once per attempt, retries after a 429
// also consume budget.
type limitedTransport struct {
	base   http.RoundTripper
	pacer  ratelimit.Limiter // uber-go/ratelimit: smooth, burst-free pacing
	hourly *rate.Limiter     // x/time/rate: secondary hourly budget
}

func newLimitedTransport(base http.RoundTripper, perMinute, perHour int) *limitedTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &limitedTransport{
		base:   base,
		pacer:  ratelimit.New(perMinute, ratelimit.Per(time.Minute), ratelimit.WithoutSlack),
		hourly: rate.NewLimiter(rate.Limit(float64(perHour)/3600.0), perHour),
	}
}

func (t *limitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Hourly budget first: it honours context cancellation.
	if err := t.hourly.Wait(req.Context()); err != nil {
		return nil, err
	}
	t.pacer.Take()
	return t.base.RoundTrip(req)
}
