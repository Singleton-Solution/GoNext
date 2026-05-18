package scenarios

import (
	"context"
	"net/http"
	"time"
)

// Posts mirrors tools/load/k6/scenarios/posts-list.js: anonymous GET of
// /wp-json/wp/v2/posts. SLO bucket is "anonRead" from lib/baseline.js.
type Posts struct{}

// Name implements [Scenario].
func (Posts) Name() string { return "posts" }

// Bucket implements [Scenario]. Values from lib/baseline.js anonRead.
func (Posts) Bucket() SLO {
	return SLO{
		P95:          400 * time.Millisecond,
		P99:          800 * time.Millisecond,
		MaxErrorRate: 0.01,
	}
}

// Setup is a no-op.
func (Posts) Setup(_ context.Context, _ string) error { return nil }

// Iter issues a GET to /wp-json/wp/v2/posts?per_page=20.
func (Posts) Iter(ctx context.Context, client *http.Client, baseURL string) Result {
	headers := http.Header{
		"Accept": []string{"application/json"},
	}
	return timeRequest(ctx, client, http.MethodGet, baseURL+"/wp-json/wp/v2/posts?per_page=20", headers)
}
