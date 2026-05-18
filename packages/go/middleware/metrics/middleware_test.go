package metrics

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// gather returns the family with name from reg, or nil if absent. The
// helper exists because every test needs to fish one specific family
// out of the registry and the Gather() API hands back a slice.
func gather(t *testing.T, reg *prometheus.Registry, name string) *dto.MetricFamily {
	t.Helper()
	mf, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, f := range mf {
		if f.GetName() == name {
			return f
		}
	}
	return nil
}

// counterValue returns the value of a CounterVec sample with the given
// label values, or 0 if no matching sample is present. The function is
// label-order-aware: pass labels as a map so the test reads naturally.
func counterValue(t *testing.T, reg *prometheus.Registry, name string, want map[string]string) float64 {
	t.Helper()
	f := gather(t, reg, name)
	if f == nil {
		return 0
	}
	for _, m := range f.GetMetric() {
		if labelsMatch(m.GetLabel(), want) {
			return m.GetCounter().GetValue()
		}
	}
	return 0
}

// gaugeValue mirrors counterValue for GaugeVec samples.
func gaugeValue(t *testing.T, reg *prometheus.Registry, name string, want map[string]string) float64 {
	t.Helper()
	f := gather(t, reg, name)
	if f == nil {
		return 0
	}
	for _, m := range f.GetMetric() {
		if labelsMatch(m.GetLabel(), want) {
			return m.GetGauge().GetValue()
		}
	}
	return 0
}

// histogramSampleCount returns the total observation count for a
// HistogramVec sample matching want, or 0 if no matching sample is
// present. We don't assert on bucket boundaries here — those are owned
// by the metrics package's bucket-set tests — only that an observation
// landed.
func histogramSampleCount(t *testing.T, reg *prometheus.Registry, name string, want map[string]string) uint64 {
	t.Helper()
	f := gather(t, reg, name)
	if f == nil {
		return 0
	}
	for _, m := range f.GetMetric() {
		if labelsMatch(m.GetLabel(), want) {
			return m.GetHistogram().GetSampleCount()
		}
	}
	return 0
}

func labelsMatch(got []*dto.LabelPair, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, lp := range got {
		if want[lp.GetName()] != lp.GetValue() {
			return false
		}
	}
	return true
}

// labelSetCount returns the number of distinct sample sets recorded
// under name. Used by the cardinality-guard test to assert that 1000
// unknown-route requests collapse to one series.
func labelSetCount(t *testing.T, reg *prometheus.Registry, name string) int {
	t.Helper()
	f := gather(t, reg, name)
	if f == nil {
		return 0
	}
	return len(f.GetMetric())
}

// servePattern wires a single-route std-mux so r.Pattern is populated
// from the matched template. Helpers that exercise the "happy path"
// (routed request) use this; tests for the "unknown" fallback go
// straight to the handler bypassing the mux.
func servePattern(pattern string, handler http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(pattern, handler)
	return mux
}

func TestMiddleware_RecordsAllThreeFamilies(t *testing.T) {
	reg := prometheus.NewRegistry()
	mw := Middleware(reg)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	mux := servePattern("POST /widgets", handler)

	req := httptest.NewRequest(http.MethodPost, "/widgets", nil)
	mux.ServeHTTP(httptest.NewRecorder(), req)

	wantLabels := map[string]string{"method": "POST", "route": "/widgets", "status": "201"}
	if got := counterValue(t, reg, MetricRequestsTotal, wantLabels); got != 1 {
		t.Errorf("requests_total: got %v, want 1", got)
	}

	noStatus := map[string]string{"method": "POST", "route": "/widgets"}
	if got := histogramSampleCount(t, reg, MetricRequestDurationSeconds, noStatus); got != 1 {
		t.Errorf("request_duration sample count: got %v, want 1", got)
	}

	// In-flight gauge must be back to 0 after the request completed.
	if got := gaugeValue(t, reg, MetricInflightRequests, noStatus); got != 0 {
		t.Errorf("inflight gauge after completion: got %v, want 0", got)
	}
}

func TestMiddleware_4xxStatusLabel(t *testing.T) {
	reg := prometheus.NewRegistry()
	mw := Middleware(reg)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	mux := servePattern("GET /missing", handler)

	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	mux.ServeHTTP(httptest.NewRecorder(), req)

	want := map[string]string{"method": "GET", "route": "/missing", "status": "404"}
	if got := counterValue(t, reg, MetricRequestsTotal, want); got != 1 {
		t.Errorf("requests_total{status=404}: got %v, want 1", got)
	}
}

func TestMiddleware_InflightGaugeReflectsConcurrency(t *testing.T) {
	reg := prometheus.NewRegistry()
	mw := Middleware(reg)

	const n = 5

	// release is closed once we've confirmed n in-flight requests; the
	// handlers block on it so we can sample the gauge mid-flight.
	release := make(chan struct{})
	// entered counts handlers that have begun serving; the test waits
	// until entered == n before it samples the gauge.
	var entered int32

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&entered, 1)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	mux := servePattern("GET /slow", handler)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/slow", nil)
			mux.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}

	// Wait for all n handlers to be in-flight. We poll because there's
	// no synchronization primitive that says "all goroutines are inside
	// the handler"; the deadline guards against the test hanging.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&entered) < n {
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d handlers in flight after 2s", atomic.LoadInt32(&entered), n)
		}
		time.Sleep(time.Millisecond)
	}

	wantLabels := map[string]string{"method": "GET", "route": "/slow"}
	if got := gaugeValue(t, reg, MetricInflightRequests, wantLabels); got != float64(n) {
		t.Errorf("inflight gauge mid-flight: got %v, want %d", got, n)
	}

	close(release)
	wg.Wait()

	if got := gaugeValue(t, reg, MetricInflightRequests, wantLabels); got != 0 {
		t.Errorf("inflight gauge after drain: got %v, want 0", got)
	}
}

func TestMiddleware_CardinalityGuardCollapsesToUnknown(t *testing.T) {
	reg := prometheus.NewRegistry()
	mw := Middleware(reg)

	// Bypass any mux so r.Pattern stays empty. The middleware MUST fall
	// back to "unknown" rather than r.URL.Path; the assertion is on the
	// number of distinct label sets after 1000 unique paths.
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const n = 1000
	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/path-%d", i), nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	// Exactly one series per family — {method=GET, route=unknown, ...}.
	if got := labelSetCount(t, reg, MetricRequestsTotal); got != 1 {
		t.Errorf("requests_total label sets after %d unique paths: got %d, want 1", n, got)
	}
	if got := labelSetCount(t, reg, MetricRequestDurationSeconds); got != 1 {
		t.Errorf("request_duration label sets: got %d, want 1", got)
	}
	if got := labelSetCount(t, reg, MetricInflightRequests); got != 1 {
		t.Errorf("inflight label sets: got %d, want 1", got)
	}

	want := map[string]string{"method": "GET", "route": unknownRoute, "status": "200"}
	if got := counterValue(t, reg, MetricRequestsTotal, want); got != float64(n) {
		t.Errorf("counter for unknown route: got %v, want %d", got, n)
	}
}

func TestMiddleware_RaceUnderConcurrentLoad(t *testing.T) {
	reg := prometheus.NewRegistry()
	mw := Middleware(reg)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	mux := servePattern("GET /race", handler)

	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/race", nil)
			mux.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}
	wg.Wait()

	want := map[string]string{"method": "GET", "route": "/race", "status": "200"}
	if got := counterValue(t, reg, MetricRequestsTotal, want); got != float64(n) {
		t.Errorf("requests_total after %d concurrent requests: got %v, want %d", n, got, n)
	}
}

func TestMiddleware_DefaultStatusIs200WhenHandlerOnlyWrites(t *testing.T) {
	// Regression guard: if the handler calls Write() but never
	// WriteHeader(), net/http writes a 200 and our counter must
	// agree.
	reg := prometheus.NewRegistry()
	mw := Middleware(reg)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	mux := servePattern("GET /ok", handler)

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	mux.ServeHTTP(httptest.NewRecorder(), req)

	want := map[string]string{"method": "GET", "route": "/ok", "status": "200"}
	if got := counterValue(t, reg, MetricRequestsTotal, want); got != 1 {
		t.Errorf("default 200 status not recorded: got %v, want 1", got)
	}
}

func TestMiddleware_PassesThroughResponseBody(t *testing.T) {
	// The wrapper must not swallow the body — it should just observe
	// the status code. Catches bugs where the wrapper forgets to call
	// the underlying Write.
	reg := prometheus.NewRegistry()
	mw := Middleware(reg)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	mux := servePattern("GET /body", handler)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/body", nil)
	mux.ServeHTTP(rec, req)

	if got := rec.Body.String(); got != "hello" {
		t.Errorf("body: got %q, want %q", got, "hello")
	}
}

func TestRouteLabel_FallsBackForNilRequest(t *testing.T) {
	if got := RouteLabel(nil); got != unknownRoute {
		t.Errorf("RouteLabel(nil): got %q, want %q", got, unknownRoute)
	}
}

func TestRouteLabel_StripsMethodPrefix(t *testing.T) {
	// Synthesize a request with Pattern set the way std-mux does.
	r := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	r.Pattern = "GET /users/{id}"
	if got := RouteLabel(r); got != "/users/{id}" {
		t.Errorf("RouteLabel: got %q, want %q", got, "/users/{id}")
	}
}

func TestRouteLabel_KeepsBarePattern(t *testing.T) {
	// Methodless pattern — std-mux supports these. Label should be the
	// pattern unchanged.
	r := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	r.Pattern = "/users/{id}"
	if got := RouteLabel(r); got != "/users/{id}" {
		t.Errorf("RouteLabel: got %q, want %q", got, "/users/{id}")
	}
}

func TestRouteLabel_LeavesUnusualPatternAlone(t *testing.T) {
	// A "pattern" that doesn't have an HTTP-method prefix before the
	// first space (e.g. a custom router that abuses the field) must
	// not have its first segment chopped off.
	r := httptest.NewRequest(http.MethodGet, "/anything", nil)
	r.Pattern = "/foo bar"
	if got := RouteLabel(r); got != "/foo bar" {
		t.Errorf("RouteLabel: got %q, want %q", got, "/foo bar")
	}
}

func TestRouteLabel_EmptyPatternFallsBack(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/whatever", nil)
	// Pattern intentionally left empty — request never went through a mux.
	if got := RouteLabel(r); got != unknownRoute {
		t.Errorf("RouteLabel for empty Pattern: got %q, want %q", got, unknownRoute)
	}
}

func TestMiddleware_NilRegistererUsesDefault(t *testing.T) {
	// Use a fresh DefaultRegisterer for the duration of this test by
	// stashing the global. Otherwise the test pollutes the package-wide
	// default and the next run fails on duplicate registration.
	saved := prometheus.DefaultRegisterer
	defer func() { prometheus.DefaultRegisterer = saved }()

	reg := prometheus.NewRegistry()
	prometheus.DefaultRegisterer = reg

	mw := Middleware(nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	mux := servePattern("GET /default", handler)

	req := httptest.NewRequest(http.MethodGet, "/default", nil)
	mux.ServeHTTP(httptest.NewRecorder(), req)

	want := map[string]string{"method": "GET", "route": "/default", "status": "200"}
	if got := counterValue(t, reg, MetricRequestsTotal, want); got != 1 {
		t.Errorf("nil-registerer path: got %v, want 1", got)
	}
}
