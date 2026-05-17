package healthz

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/Singleton-Solution/GoNext/packages/go/buildinfo"
)

// serviceName is the value returned in the liveness "service" field.
// It's a package-level constant rather than a parameter because the
// API server is the only consumer; if a second service ever mounts
// these handlers, we'll thread it through.
const serviceName = "api"

// DefaultCheckTimeout is the per-check budget used by Readiness.
// Operators typically configure Kubernetes readiness timeoutSeconds
// to 3-5s; the per-check budget should be well under that while
// covering the realistic RTT to a healthy backend plus handshake.
// 2s is the right answer for Postgres and Redis on the same pod
// network — a fraction of the probe timeout, with comfortable
// headroom for cold connections.
const DefaultCheckTimeout = 2 * time.Second

// Check is a single readiness probe. Name returns the label that
// appears in the response payload ("db", "redis", "auth-cache", …);
// Check performs the actual probe with the supplied context.
//
// A nil error means healthy. Any non-nil error renders as "err: <msg>"
// in the response and turns the aggregate status to not_ready.
type Check interface {
	Name() string
	Check(ctx context.Context) error
}

// Liveness returns a handler that always responds 200 with the
// process identity. No dependency checks — the only failure mode is
// the process not running, in which case the handler can't respond
// at all and Kubernetes restarts the container.
//
// This is the handler you wire to livenessProbe in your Deployment.
func Liveness() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		bi := buildinfo.Get(serviceName)
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "alive",
			"service": serviceName,
			"version": bi.Version,
		})
	})
}

// Readiness returns a handler that runs each Check concurrently and
// reports the aggregate result.
//
// 200 OK with {"status":"ready", ...} if every check returns nil.
// 503 Service Unavailable with {"status":"not_ready", ...} if any
// check returns an error or its per-check context deadline elapses.
//
// Per-check timeout defaults to DefaultCheckTimeout. The response
// always includes a duration_ms field for observability.
//
// This is the handler you wire to readinessProbe in your Deployment.
// On 503, Kubernetes takes the pod out of rotation but does NOT
// restart it — failed readiness without failed liveness is the
// correct response to a transient backend blip.
func Readiness(checks ...Check) http.Handler {
	return readinessWithTimeout(DefaultCheckTimeout, checks...)
}

// readinessWithTimeout is Readiness with an explicit timeout. Split
// out so tests can use a shorter budget without sleeping for 2s.
func readinessWithTimeout(timeout time.Duration, checks ...Check) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		results := runChecks(r.Context(), timeout, checks)

		allOK := true
		body := make(map[string]string, len(results))
		for _, res := range results {
			if res.err == nil {
				body[res.name] = "ok"
				continue
			}
			allOK = false
			body[res.name] = "err: " + res.err.Error()
		}

		status := "ready"
		code := http.StatusOK
		if !allOK {
			status = "not_ready"
			code = http.StatusServiceUnavailable
		}

		writeJSON(w, code, map[string]any{
			"status":      status,
			"checks":      body,
			"duration_ms": time.Since(start).Milliseconds(),
		})
	})
}

// checkResult carries a single Check's outcome. Internal to the
// fan-out logic — callers see only the JSON payload.
type checkResult struct {
	name string
	err  error
}

// runChecks runs every Check in parallel under a per-check timeout
// derived from parent. Returns results in the same order checks were
// supplied so the response payload is stable.
//
// Implementation note: we intentionally use a fixed-size slice
// indexed by position rather than a channel — the result order
// matters for stable test assertions, and the goroutine count is
// bounded by len(checks), which is bounded by what's registered at
// boot. No goroutine leak concern.
func runChecks(parent context.Context, timeout time.Duration, checks []Check) []checkResult {
	results := make([]checkResult, len(checks))
	var wg sync.WaitGroup
	wg.Add(len(checks))

	for i, c := range checks {
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(parent, timeout)
			defer cancel()
			results[i] = checkResult{name: c.Name(), err: c.Check(ctx)}
		}()
	}

	wg.Wait()
	return results
}

// --- concrete Check implementations ---

// DBCheck builds a Check that pings a pgxpool.Pool.
//
// Returns a Check that, on a healthy pool, executes a sub-millisecond
// SELECT 1 round-trip. On an unhealthy pool, surfaces the underlying
// error verbatim — pgx already keeps the DSN out of its errors.
func DBCheck(pool *pgxpool.Pool) Check {
	return &dbCheck{pool: pool}
}

type dbCheck struct {
	pool *pgxpool.Pool
}

func (d *dbCheck) Name() string { return "db" }

func (d *dbCheck) Check(ctx context.Context) error {
	return d.pool.Ping(ctx)
}

// RedisCheck builds a Check that pings a redis.Client.
//
// Returns a Check that runs a PING command. On a healthy server, this
// is sub-millisecond. On failure (unreachable, auth error, etc.) the
// underlying go-redis error is surfaced.
func RedisCheck(rdb *redis.Client) Check {
	return &redisCheck{rdb: rdb}
}

type redisCheck struct {
	rdb *redis.Client
}

func (r *redisCheck) Name() string { return "redis" }

func (r *redisCheck) Check(ctx context.Context) error {
	return r.rdb.Ping(ctx).Err()
}

// Custom wraps an arbitrary check function with a name. Use this for
// dependencies that don't have a dedicated *Check constructor — an
// external API ping, a queue depth assertion, a feature-flag service
// health endpoint.
//
//	healthz.Custom("auth-svc", func(ctx context.Context) error {
//	    return authClient.Ping(ctx)
//	})
func Custom(name string, fn func(ctx context.Context) error) Check {
	return &customCheck{name: name, fn: fn}
}

type customCheck struct {
	name string
	fn   func(ctx context.Context) error
}

func (c *customCheck) Name() string                    { return c.name }
func (c *customCheck) Check(ctx context.Context) error { return c.fn(ctx) }

// writeJSON is the package's house response writer. Identical shape
// to main.go's helper, kept local so handlers don't depend on cmd/.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
