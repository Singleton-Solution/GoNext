package scenarios

import (
	"context"
	"net/http"
	"time"
)

// Homepage mirrors tools/load/k6/scenarios/homepage.js: a GET against
// "/" expected to hit the page cache. The SLO bucket is "cachedAnon"
// from lib/baseline.js — the tightest budget in the system.
type Homepage struct{}

// Name implements [Scenario].
func (Homepage) Name() string { return "homepage" }

// Bucket implements [Scenario]. Values from lib/baseline.js cachedAnon.
func (Homepage) Bucket() SLO {
	return SLO{
		P95:          250 * time.Millisecond,
		P99:          500 * time.Millisecond,
		MaxErrorRate: 0.01,
	}
}

// Setup is a no-op — homepage does not need shared state.
func (Homepage) Setup(_ context.Context, _ string) error { return nil }

// Iter issues a single GET to baseURL with browser-ish headers.
func (Homepage) Iter(ctx context.Context, client *http.Client, baseURL string) Result {
	headers := http.Header{
		"Accept":          []string{"text/html,application/xhtml+xml"},
		"Accept-Encoding": []string{"gzip"},
	}
	return timeRequest(ctx, client, http.MethodGet, baseURL+"/", headers)
}
