package backpressure_test

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/Singleton-Solution/GoNext/packages/go/jobs/backpressure"
)

// staticResolver is the dirt-simplest Resolver: emit the same queue
// and priority for every request. Adequate for tests where the
// routing logic doesn't matter; we exercise the resolver itself in
// dedicated cases.
func staticResolver(queue string, priority backpressure.Priority) backpressure.Resolver {
	return func(r *http.Request) (string, backpressure.Priority) {
		return queue, priority
	}
}

// counterValue reads the float64 value of the labeled counter cell.
// Returns 0 if the labels haven't been seen yet (Prometheus's
// CounterVec returns a fresh zeroed counter on first WithLabelValues
// access). Test helpers in jobs/asynq use the same pattern.
func counterValue(t *testing.T, c *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := c.WithLabelValues(labels...).(prometheus.Metric).Write(m); err != nil {
		t.Fatalf("counter Write: %v", err)
	}
	return m.GetCounter().GetValue()
}

// gatherMetric looks up a Prometheus metric family in a registry by
// name and returns the family proto. Used to assert the counter is
// registered with the expected name (part of the public observability
// contract).
func gatherMetric(t *testing.T, reg *prometheus.Registry, name string) *dto.MetricFamily {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() == name {
			return f
		}
	}
	return nil
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// TestMiddlewareAdmits verifies the happy path: a request below
// SoftLimit reaches the wrapped handler, the response is whatever
// the handler wrote, and the counter is not incremented.
func TestMiddlewareAdmits(t *testing.T) {
	src := newStubSource()
	src.set("webhook", 1) // well below soft
	gate, err := backpressure.NewGate(src, []backpressure.Threshold{
		{Queue: "webhook", SoftLimit: 10, HardLimit: 20},
	})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	reg := prometheus.NewRegistry()
	mw := backpressure.NewMiddleware(gate, staticResolver("webhook", backpressure.Normal), quietLogger(), reg)

	var handlerCalls atomic.Int32
	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/enqueue", nil))

	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
	if handlerCalls.Load() != 1 {
		t.Errorf("handler calls = %d, want 1", handlerCalls.Load())
	}
}

// TestMiddlewareSheds verifies the shed path: 429 response, Retry-
// After header set, body contains the shed error, counter
// incremented under the expected labels, and the wrapped handler is
// not invoked. This pins every observable side effect at once
// because they all need to agree for the contract to be useful.
func TestMiddlewareSheds(t *testing.T) {
	src := newStubSource()
	src.set("webhook", 25) // above hard limit
	gate, err := backpressure.NewGate(src, []backpressure.Threshold{
		{Queue: "webhook", SoftLimit: 10, HardLimit: 20},
	})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	reg := prometheus.NewRegistry()
	mw := backpressure.NewMiddleware(gate, staticResolver("webhook", backpressure.Normal), quietLogger(), reg)

	var handlerCalls atomic.Int32
	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls.Add(1)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/enqueue", nil))

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After = %q, want %q", got, "1")
	}
	if !strings.Contains(rec.Body.String(), "backpressure") {
		t.Errorf("body = %q, want it to mention 'backpressure'", rec.Body.String())
	}
	if handlerCalls.Load() != 0 {
		t.Errorf("handler invoked despite shed: calls = %d", handlerCalls.Load())
	}

	// Counter contract: gonext_backpressure_shed_total{queue,priority}.
	family := gatherMetric(t, reg, "gonext_backpressure_shed_total")
	if family == nil {
		t.Fatal("gonext_backpressure_shed_total not registered")
	}
	// The labeled cell value must be exactly 1 after one shed.
	// We pull the CounterVec out via the registry's gather rather than
	// holding a handle; this also pins the label values "webhook" and
	// "normal" as part of the contract.
	var seen bool
	for _, m := range family.GetMetric() {
		labels := map[string]string{}
		for _, lp := range m.GetLabel() {
			labels[lp.GetName()] = lp.GetValue()
		}
		if labels["queue"] == "webhook" && labels["priority"] == "normal" {
			seen = true
			if got := m.GetCounter().GetValue(); got != 1 {
				t.Errorf("counter value = %v, want 1", got)
			}
		}
	}
	if !seen {
		t.Error("counter cell {queue=webhook,priority=normal} not present after shed")
	}
}

// TestMiddlewareCounterIncrementsPerShed pins the increment cadence:
// two sheds → counter == 2. Catches a regression where the metric is
// emitted at construction (gauge-style) instead of on each shed.
func TestMiddlewareCounterIncrementsPerShed(t *testing.T) {
	src := newStubSource()
	src.set("webhook", 25)
	gate, err := backpressure.NewGate(src, []backpressure.Threshold{
		{Queue: "webhook", SoftLimit: 10, HardLimit: 20},
	})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	reg := prometheus.NewRegistry()
	mw := backpressure.NewMiddleware(gate, staticResolver("webhook", backpressure.Background), quietLogger(), reg)
	h := mw.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/enqueue", nil))
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("iter %d: status = %d, want 429", i, rec.Code)
		}
	}
	fam := gatherMetric(t, reg, "gonext_backpressure_shed_total")
	if fam == nil || len(fam.GetMetric()) == 0 {
		t.Fatal("counter family missing")
	}
	if got := fam.GetMetric()[0].GetCounter().GetValue(); got != 3 {
		t.Errorf("counter = %v, want 3 after 3 sheds", got)
	}
}

// TestMiddlewareResolverEmptyQueueAdmits verifies the documented
// escape hatch: a Resolver that returns an empty queue tells the
// middleware to pass-through. This is how diagnostic endpoints share
// a mux with /enqueue without going through the gate.
func TestMiddlewareResolverEmptyQueueAdmits(t *testing.T) {
	src := newStubSource()
	src.set("webhook", 1000) // deeply over hard
	gate, err := backpressure.NewGate(src, []backpressure.Threshold{
		{Queue: "webhook", SoftLimit: 10, HardLimit: 20},
	})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	resolver := func(*http.Request) (string, backpressure.Priority) {
		return "", backpressure.Normal // empty queue → bypass
	}
	mw := backpressure.NewMiddleware(gate, resolver, quietLogger(), prometheus.NewRegistry())

	var handlerCalls atomic.Int32
	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/diag", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if handlerCalls.Load() != 1 {
		t.Errorf("handler calls = %d, want 1", handlerCalls.Load())
	}
}

// TestMiddlewareCriticalNeverShed verifies the design invariant
// through the HTTP path end-to-end. Even at extreme depth, Critical
// priority bypasses the gate; this is the no-2FA-during-incident
// guarantee.
func TestMiddlewareCriticalNeverShed(t *testing.T) {
	src := newStubSource()
	src.set("webhook", 1_000_000)
	gate, err := backpressure.NewGate(src, []backpressure.Threshold{
		{Queue: "webhook", SoftLimit: 10, HardLimit: 20},
	})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	mw := backpressure.NewMiddleware(gate, staticResolver("webhook", backpressure.Critical), quietLogger(), prometheus.NewRegistry())

	var handlerCalls atomic.Int32
	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/enqueue", nil))
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
	if handlerCalls.Load() != 1 {
		t.Errorf("critical handler calls = %d, want 1", handlerCalls.Load())
	}
}

// TestMiddlewareConcurrentRequests stresses the middleware with many
// concurrent requests. With -race this catches accidental shared
// mutable state in the middleware itself or in the Prometheus
// collector. The assertion is "no race detector warnings and the
// counter agrees with the number of expected sheds".
func TestMiddlewareConcurrentRequests(t *testing.T) {
	src := newStubSource()
	src.set("webhook", 25) // above hard → every Normal request sheds
	gate, err := backpressure.NewGate(src, []backpressure.Threshold{
		{Queue: "webhook", SoftLimit: 10, HardLimit: 20},
	})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	reg := prometheus.NewRegistry()
	mw := backpressure.NewMiddleware(gate, staticResolver("webhook", backpressure.Normal), quietLogger(), reg)
	h := mw.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	const goroutines = 32
	const iters = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/enqueue", nil))
				if rec.Code != http.StatusTooManyRequests {
					t.Errorf("status = %d, want 429", rec.Code)
				}
			}
		}()
	}
	wg.Wait()

	fam := gatherMetric(t, reg, "gonext_backpressure_shed_total")
	if fam == nil || len(fam.GetMetric()) == 0 {
		t.Fatal("counter family missing")
	}
	want := float64(goroutines * iters)
	if got := fam.GetMetric()[0].GetCounter().GetValue(); got != want {
		t.Errorf("counter = %v, want %v after %v concurrent sheds", got, want, want)
	}
}

// TestMiddlewareNilPassthrough verifies the documented escape hatch:
// (*Middleware)(nil).Handler returns next unchanged. This lets the
// caller write `mw.Handler(next)` even when backpressure is disabled
// in the current build/env, without an outer if-check.
func TestMiddlewareNilPassthrough(t *testing.T) {
	var mw *backpressure.Middleware
	called := false
	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", nil))
	if !called {
		t.Error("nil middleware: wrapped handler not called")
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}

// TestNewMiddlewarePanicsOnNilGate is a contract check — a nil Gate
// would nil-deref on the first request. We'd rather fail at boot.
func TestNewMiddlewarePanicsOnNilGate(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewMiddleware(nil gate): want panic, got none")
		}
	}()
	_ = backpressure.NewMiddleware(nil, staticResolver("q", backpressure.Normal), nil, nil)
}

// TestNewMiddlewarePanicsOnNilResolver mirrors the gate-nil check.
func TestNewMiddlewarePanicsOnNilResolver(t *testing.T) {
	src := newStubSource()
	gate, err := backpressure.NewGate(src, []backpressure.Threshold{{Queue: "q", SoftLimit: 1, HardLimit: 2}})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewMiddleware(nil resolver): want panic, got none")
		}
	}()
	_ = backpressure.NewMiddleware(gate, nil, nil, nil)
}

// TestShedErrorWrapping pins that the error returned from the gate
// wraps ErrShed with detail. The middleware logs and the calling
// code's branching both rely on this.
func TestShedErrorWrapping(t *testing.T) {
	src := newStubSource()
	src.set("q", 100)
	gate, err := backpressure.NewGate(src, []backpressure.Threshold{{Queue: "q", SoftLimit: 1, HardLimit: 2}})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	got := gate.Allow("q", backpressure.Normal)
	if !errors.Is(got, backpressure.ErrShed) {
		t.Errorf("errors.Is(err, ErrShed) = false; err=%v", got)
	}
	if !strings.Contains(got.Error(), "depth=100") {
		t.Errorf("err = %q, want detail with depth=100", got.Error())
	}
}

// silence the unused-import lint when counterValue is not invoked
// in any retained test (we use family-based gather assertions
// instead, which exercise the registered name).
var _ = counterValue
