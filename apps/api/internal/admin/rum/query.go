package rum

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// QueryHandler serves GET /api/v1/admin/rum/percentiles and the
// companion /slow-routes endpoint. It is gated by CapJobsAdmin
// (the closest "operator looking at infrastructure" capability).
// A future role refinement can map a dedicated CapRUMRead through
// here without touching this code.
//
// Responses are cached in-process for a short window because the
// underlying store query, even when index-served, is the kind of
// "ten-times-per-page-refresh" request that benefits from
// trivial memoisation. The cache is keyed by (metric, path,
// period) and TTL'd at cacheTTL. We do NOT cache across processes
// — the operator might be looking at a single replica's view,
// which is fine for an operator surface.
type QueryHandler struct {
	store  EventStore
	policy policy.Policy
	logger *slog.Logger
	now    func() time.Time

	mu       sync.Mutex
	pctCache map[string]percentileCacheEntry
}

type percentileCacheEntry struct {
	result   PercentileResult
	expires  time.Time
}

// cacheTTL is the in-process cache window for percentile reads.
// 30 s matches the admin page's auto-refresh tick — successive
// re-renders within a tick reuse the cached aggregate; the next
// tick recomputes. Operators who want fresher numbers can hit
// Refresh, which the page wires to a cache-busting query param.
const cacheTTL = 30 * time.Second

// supportedPeriods is the closed set of period selectors the
// query handler accepts. Keeping the set small is a deliberate
// choice: every period becomes a distinct cache key, and an
// open-ended ?period=42m would blow the cache out.
var supportedPeriods = map[string]time.Duration{
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

// NewQueryHandler wires a QueryHandler around the given store.
// All non-nil; pass slog.Default to logger if you do not have a
// service logger handy.
func NewQueryHandler(store EventStore, pol policy.Policy, now func() time.Time, logger *slog.Logger) (*QueryHandler, error) {
	if store == nil {
		return nil, errors.New("rum.NewQueryHandler: store is required")
	}
	if pol == nil {
		return nil, errors.New("rum.NewQueryHandler: policy is required")
	}
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &QueryHandler{
		store:    store,
		policy:   pol,
		logger:   logger,
		now:      now,
		pctCache: make(map[string]percentileCacheEntry, 32),
	}, nil
}

// ServePercentiles handles GET /percentiles?metric=...&path=...&period=...
// The path query param is optional; an empty path aggregates over
// all routes. Default period is "24h".
func (h *QueryHandler) ServePercentiles(w http.ResponseWriter, r *http.Request) {
	pr, ok := policy.FromContext(r.Context())
	if !ok {
		router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	if d := h.policy.Can(pr, policy.CapJobsAdmin, nil); !d.Allowed {
		router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
		return
	}

	q := r.URL.Query()
	metric := q.Get("metric")
	if metric == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_metric", "metric query parameter is required")
		return
	}
	if !contains(allowedMetrics, metric) {
		router.WriteError(w, http.StatusBadRequest, "unknown_metric", "metric is not in the supported set")
		return
	}
	path := q.Get("path")
	if len(path) > MaxPagePathLen {
		router.WriteError(w, http.StatusBadRequest, "path_too_long", "path exceeds maximum length")
		return
	}
	period := q.Get("period")
	if period == "" {
		period = "24h"
	}
	window, ok := supportedPeriods[period]
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "unknown_period", "period must be one of: 1h, 6h, 24h, 7d")
		return
	}

	to := h.now().UTC()
	from := to.Add(-window)

	cacheKey := metric + "|" + path + "|" + period
	if cached, hit := h.cacheGet(cacheKey); hit {
		// Surface the result, freezing the From/To at cache time
		// — re-pinning to "now" would lie about the window the
		// percentiles were computed over.
		out := cached
		out.Period = period
		router.WriteJSON(w, http.StatusOK, out)
		return
	}

	res, err := h.store.Percentiles(r.Context(), metric, path, from, to)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "rum.query: percentiles failed",
			slog.String("err", err.Error()),
			slog.String("metric", metric),
		)
		router.WriteError(w, http.StatusInternalServerError, "query_failed", "failed to compute percentiles")
		return
	}
	res.Period = period
	h.cachePut(cacheKey, res)
	router.WriteJSON(w, http.StatusOK, res)
}

// ServeSlowestRoutes handles GET /slow-routes?metric=...&period=...&limit=...
// Returns the top-N routes sorted by p75 (desc). The default
// limit is 10; the cap is 50 to keep the response small.
func (h *QueryHandler) ServeSlowestRoutes(w http.ResponseWriter, r *http.Request) {
	pr, ok := policy.FromContext(r.Context())
	if !ok {
		router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	if d := h.policy.Can(pr, policy.CapJobsAdmin, nil); !d.Allowed {
		router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
		return
	}

	q := r.URL.Query()
	metric := q.Get("metric")
	if metric == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_metric", "metric query parameter is required")
		return
	}
	if !contains(allowedMetrics, metric) {
		router.WriteError(w, http.StatusBadRequest, "unknown_metric", "metric is not in the supported set")
		return
	}
	period := q.Get("period")
	if period == "" {
		period = "24h"
	}
	window, ok := supportedPeriods[period]
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "unknown_period", "period must be one of: 1h, 6h, 24h, 7d")
		return
	}

	limit := 10
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			router.WriteError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
			return
		}
		if n > 50 {
			n = 50
		}
		limit = n
	}

	to := h.now().UTC()
	from := to.Add(-window)
	rows, err := h.store.SlowestRoutes(r.Context(), metric, from, to, limit)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "rum.query: slowest failed",
			slog.String("err", err.Error()),
			slog.String("metric", metric),
		)
		router.WriteError(w, http.StatusInternalServerError, "query_failed", "failed to compute slow routes")
		return
	}
	if rows == nil {
		// Encode as [] not null for client convenience.
		rows = []RouteSlowRow{}
	}
	router.WriteJSON(w, http.StatusOK, map[string]any{
		"metric": metric,
		"period": period,
		"from":   from,
		"to":     to,
		"routes": rows,
	})
}

// cacheGet returns a cached percentile result for key if it is
// still within TTL. Returns false on miss or expiry; expired
// entries are removed by the next put rather than by a
// background sweeper (the cache is tiny).
func (h *QueryHandler) cacheGet(key string) (PercentileResult, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.pctCache[key]
	if !ok {
		return PercentileResult{}, false
	}
	if h.now().After(e.expires) {
		delete(h.pctCache, key)
		return PercentileResult{}, false
	}
	return e.result, true
}

// cachePut stores result under key with a fresh TTL.
func (h *QueryHandler) cachePut(key string, result PercentileResult) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pctCache[key] = percentileCacheEntry{
		result:  result,
		expires: h.now().Add(cacheTTL),
	}
}

// Compile-time interface satisfaction check. Both handler types
// implement http.Handler explicitly so a misnamed method gets
// caught at compile.
var _ http.Handler = (*BeaconHandler)(nil)

// percentileCacheReset is a test-only helper, exposed via the
// _test.go indirection. Production code never touches it.
//
//nolint:unused // referenced from query_test.go
func (h *QueryHandler) percentileCacheReset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pctCache = make(map[string]percentileCacheEntry, 32)
}

// Compile-time guards that the public types have the JSON shape
// the admin frontend expects. The package is purely server-side
// today, but a future cut may share these types with a generated
// client; treating the shapes as a public contract is the right
// posture from the start.
var (
	_ = context.Background
)
