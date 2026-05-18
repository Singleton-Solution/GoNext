package rum

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

func TestPercentiles_UnauthIs401(t *testing.T) {
	t.Parallel()
	h := newQueryForTest(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/rum/percentiles?metric=LCP", nil)
	rec := httptest.NewRecorder()
	h.ServePercentiles(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401; got %d", rec.Code)
	}
}

func TestPercentiles_ForbiddenWithoutCapability(t *testing.T) {
	t.Parallel()
	h := newQueryForTest(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/rum/percentiles?metric=LCP", nil)
	req = req.WithContext(policy.WithPrincipal(req.Context(), policy.Principal{
		UserID: "u1",
		Roles:  []policy.Role{policy.RoleSubscriber},
	}))
	rec := httptest.NewRecorder()
	h.ServePercentiles(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403; got %d", rec.Code)
	}
}

func TestPercentiles_MissingMetricIs400(t *testing.T) {
	t.Parallel()
	h := newQueryForTest(t)
	req := newAdminReq(t, "/api/v1/admin/rum/percentiles")
	rec := httptest.NewRecorder()
	h.ServePercentiles(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d", rec.Code)
	}
}

func TestPercentiles_UnknownMetricIs400(t *testing.T) {
	t.Parallel()
	h := newQueryForTest(t)
	req := newAdminReq(t, "/api/v1/admin/rum/percentiles?metric=ZZZ")
	rec := httptest.NewRecorder()
	h.ServePercentiles(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d", rec.Code)
	}
}

func TestPercentiles_UnknownPeriodIs400(t *testing.T) {
	t.Parallel()
	h := newQueryForTest(t)
	req := newAdminReq(t, "/api/v1/admin/rum/percentiles?metric=LCP&period=42m")
	rec := httptest.NewRecorder()
	h.ServePercentiles(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d", rec.Code)
	}
}

func TestPercentiles_HappyPath(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	clock := time.Unix(1_700_000_000, 0).UTC()
	events := make([]Event, 0, 50)
	for i := 1; i <= 50; i++ {
		events = append(events, Event{
			Metric:    "LCP",
			Value:     float64(i * 10),
			Rating:    "good",
			PagePath:  "/",
			SessionID: "s",
		})
	}
	if err := store.Insert(context.Background(), clock.Add(-time.Minute), events); err != nil {
		t.Fatalf("insert: %v", err)
	}
	h := newQueryForTestWithStore(t, store, clock)
	req := newAdminReq(t, "/api/v1/admin/rum/percentiles?metric=LCP&path=/&period=1h")
	rec := httptest.NewRecorder()
	h.ServePercentiles(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d (%s)", rec.Code, rec.Body.String())
	}
	var got PercentileResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Sample != 50 {
		t.Fatalf("expected n=50; got %d", got.Sample)
	}
	if got.Metric != "LCP" || got.Period != "1h" {
		t.Fatalf("unexpected response shape: %+v", got)
	}
	if got.P50 <= 0 || got.P95 < got.P50 {
		t.Fatalf("percentiles look wrong: %+v", got)
	}
}

func TestPercentiles_CacheHit(t *testing.T) {
	t.Parallel()
	store := &countingStore{inner: NewMemoryStore()}
	clock := time.Unix(1_700_000_000, 0).UTC()
	_ = store.inner.Insert(context.Background(), clock.Add(-time.Minute), []Event{
		{Metric: "LCP", Value: 100, Rating: "good", PagePath: "/", SessionID: "s"},
		{Metric: "LCP", Value: 200, Rating: "good", PagePath: "/", SessionID: "s"},
	})
	h := newQueryForTestWithStore(t, store, clock)

	doReq := func() int {
		req := newAdminReq(t, "/api/v1/admin/rum/percentiles?metric=LCP&period=1h")
		rec := httptest.NewRecorder()
		h.ServePercentiles(rec, req)
		return rec.Code
	}
	if code := doReq(); code != http.StatusOK {
		t.Fatalf("first call: expected 200; got %d", code)
	}
	if code := doReq(); code != http.StatusOK {
		t.Fatalf("second call: expected 200; got %d", code)
	}
	if store.percentileCalls != 1 {
		t.Fatalf("expected one Percentiles call due to caching; got %d", store.percentileCalls)
	}
}

func TestPercentiles_CacheExpires(t *testing.T) {
	t.Parallel()
	store := &countingStore{inner: NewMemoryStore()}
	now := time.Unix(1_700_000_000, 0).UTC()
	_ = store.inner.Insert(context.Background(), now.Add(-time.Minute), []Event{
		{Metric: "LCP", Value: 100, Rating: "good", PagePath: "/", SessionID: "s"},
	})
	clock := &advancingClock{t: now}
	h, err := NewQueryHandler(store, policy.NewBasicPolicy(policy.DefaultRoleCapabilities()), clock.now, nil)
	if err != nil {
		t.Fatalf("NewQueryHandler: %v", err)
	}
	doReq := func() {
		req := newAdminReq(t, "/api/v1/admin/rum/percentiles?metric=LCP&period=1h")
		rec := httptest.NewRecorder()
		h.ServePercentiles(rec, req)
	}
	doReq()
	clock.advance(cacheTTL + time.Second)
	doReq()
	if store.percentileCalls != 2 {
		t.Fatalf("expected two Percentiles calls after expiry; got %d", store.percentileCalls)
	}
}

func TestSlowestRoutes_HappyPath(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	clock := time.Unix(1_700_000_000, 0).UTC()
	events := []Event{}
	for i := 0; i < 5; i++ {
		events = append(events, Event{Metric: "LCP", Value: 100, Rating: "good", PagePath: "/a", SessionID: "s"})
	}
	for i := 0; i < 5; i++ {
		events = append(events, Event{Metric: "LCP", Value: 5000, Rating: "poor", PagePath: "/b", SessionID: "s"})
	}
	if err := store.Insert(context.Background(), clock.Add(-time.Minute), events); err != nil {
		t.Fatalf("insert: %v", err)
	}
	h := newQueryForTestWithStore(t, store, clock)
	req := newAdminReq(t, "/api/v1/admin/rum/slow-routes?metric=LCP&period=1h")
	rec := httptest.NewRecorder()
	h.ServeSlowestRoutes(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Routes []RouteSlowRow `json:"routes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Routes) != 2 || body.Routes[0].Path != "/b" {
		t.Fatalf("unexpected routes: %+v", body.Routes)
	}
}

func TestSlowestRoutes_InvalidLimitIs400(t *testing.T) {
	t.Parallel()
	h := newQueryForTest(t)
	req := newAdminReq(t, "/api/v1/admin/rum/slow-routes?metric=LCP&limit=0")
	rec := httptest.NewRecorder()
	h.ServeSlowestRoutes(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d", rec.Code)
	}
}

func TestSlowestRoutes_UnauthIs401(t *testing.T) {
	t.Parallel()
	h := newQueryForTest(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/rum/slow-routes?metric=LCP", nil)
	rec := httptest.NewRecorder()
	h.ServeSlowestRoutes(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401; got %d", rec.Code)
	}
}

// --- helpers ---

func newQueryForTest(t *testing.T) *QueryHandler {
	t.Helper()
	return newQueryForTestWithStore(t, NewMemoryStore(), time.Unix(1_700_000_000, 0).UTC())
}

func newQueryForTestWithStore(t *testing.T, store EventStore, clock time.Time) *QueryHandler {
	t.Helper()
	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	h, err := NewQueryHandler(store, pol, func() time.Time { return clock }, nil)
	if err != nil {
		t.Fatalf("NewQueryHandler: %v", err)
	}
	return h
}

// newAdminReq builds a GET request with an admin Principal on
// context so the capability gate passes.
func newAdminReq(t *testing.T, target string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	return r.WithContext(policy.WithPrincipal(r.Context(), policy.Principal{
		UserID: "admin-user",
		Roles:  []policy.Role{policy.RoleAdmin},
	}))
}

// countingStore wraps a real EventStore and counts calls so the
// cache tests can assert the store was hit the expected number of
// times.
type countingStore struct {
	inner            *MemoryStore
	percentileCalls  int
	slowestCalls     int
}

func (c *countingStore) Insert(ctx context.Context, ts time.Time, events []Event) error {
	return c.inner.Insert(ctx, ts, events)
}

func (c *countingStore) Percentiles(ctx context.Context, metric, path string, from, to time.Time) (PercentileResult, error) {
	c.percentileCalls++
	return c.inner.Percentiles(ctx, metric, path, from, to)
}

func (c *countingStore) SlowestRoutes(ctx context.Context, metric string, from, to time.Time, limit int) ([]RouteSlowRow, error) {
	c.slowestCalls++
	return c.inner.SlowestRoutes(ctx, metric, from, to, limit)
}

// advancingClock is a mutable clock for cache-expiry tests.
type advancingClock struct {
	t time.Time
}

func (c *advancingClock) now() time.Time {
	return c.t
}

func (c *advancingClock) advance(d time.Duration) {
	c.t = c.t.Add(d)
}
