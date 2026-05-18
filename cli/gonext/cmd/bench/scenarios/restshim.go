package scenarios

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// RestShim mirrors tools/load/k6/scenarios/rest-shim.js: it cycles
// through a fixed mix of WP-compat REST queries so every shape gets
// exercised over a long enough run. SLO bucket is "anonRead".
type RestShim struct {
	// counter cycles iterations across the query list. Use atomic
	// access so the runner's VUs do not race each other into the same
	// path every tick. Pointer receiver via mutation is fine because
	// the runner instantiates one Scenario value per scenario.
	counter atomic.Uint64
}

// Name implements [Scenario].
func (*RestShim) Name() string { return "restshim" }

// Bucket implements [Scenario]. Values from lib/baseline.js anonRead.
func (*RestShim) Bucket() SLO {
	return SLO{
		P95:          400 * time.Millisecond,
		P99:          800 * time.Millisecond,
		MaxErrorRate: 0.01,
	}
}

// Setup is a no-op.
func (*RestShim) Setup(_ context.Context, _ string) error { return nil }

// restShimQueries is the same mix the k6 scenario walks, in the same
// order. Keep these in sync.
var restShimQueries = []string{
	"/wp-json/wp/v2/posts?per_page=20&page=1",
	"/wp-json/wp/v2/posts?per_page=20&page=2",
	"/wp-json/wp/v2/posts?per_page=5",
	"/wp-json/wp/v2/posts?per_page=10&orderby=title&order=asc",
	"/wp-json/wp/v2/posts?search=hello&per_page=10",
	"/wp-json/wp/v2/pages?per_page=10",
	"/wp-json/wp/v2/categories?per_page=20",
	"/wp-json/wp/v2/tags?per_page=20",
	"/wp-json/wp/v2/users?per_page=10",
}

// Iter picks the next query in the mix and times it.
func (s *RestShim) Iter(ctx context.Context, client *http.Client, baseURL string) Result {
	n := s.counter.Add(1) - 1
	path := restShimQueries[n%uint64(len(restShimQueries))]
	headers := http.Header{"Accept": []string{"application/json"}}
	return timeRequest(ctx, client, http.MethodGet, baseURL+path, headers)
}

// Note: All() in scenarios.go wraps this in a pointer because the
// counter requires a stable address. The single-shot value used there
// is fine — each call to All() yields a fresh zero counter.
