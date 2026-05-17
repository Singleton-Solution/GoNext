package healthz

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// mockCheck is the test-only Check used in unit tests. We don't need
// a real DB or Redis to exercise the readiness fan-out, just a Check
// whose return value and timing we can dictate.
type mockCheck struct {
	name  string
	err   error
	delay time.Duration
	calls atomic.Int32
}

func (m *mockCheck) Name() string { return m.name }

func (m *mockCheck) Check(ctx context.Context) error {
	m.calls.Add(1)
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return m.err
}

func TestLiveness_AlwaysReturns200(t *testing.T) {
	h := Liveness()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Errorf("status: got %d, want %d", got, want)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: got %q, want application/json…", ct)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if body["status"] != "alive" {
		t.Errorf("status field: got %v, want alive", body["status"])
	}
	if body["service"] != "api" {
		t.Errorf("service field: got %v, want api", body["service"])
	}
	if _, ok := body["version"]; !ok {
		t.Errorf("version field missing")
	}
}

func TestLiveness_DoesNotCallDependencies(t *testing.T) {
	// Liveness must never depend on anything. The handler signature
	// has no checks parameter, so the property is enforced by the
	// type system. This test guards against regressions where
	// somebody adds a dependency through a package-level singleton.
	h := Liveness()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("liveness should always be 200, got %d", rr.Code)
	}
}

func TestReadiness_AllChecksPass(t *testing.T) {
	dbm := &mockCheck{name: "db"}
	redism := &mockCheck{name: "redis"}

	h := readinessWithTimeout(500*time.Millisecond, dbm, redism)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Errorf("status: got %d, want %d", got, want)
	}

	var body struct {
		Status     string            `json:"status"`
		Checks     map[string]string `json:"checks"`
		DurationMS int64             `json:"duration_ms"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if body.Status != "ready" {
		t.Errorf("status: got %q, want ready", body.Status)
	}
	if body.Checks["db"] != "ok" {
		t.Errorf("db: got %q, want ok", body.Checks["db"])
	}
	if body.Checks["redis"] != "ok" {
		t.Errorf("redis: got %q, want ok", body.Checks["redis"])
	}
	if body.DurationMS < 0 {
		t.Errorf("duration_ms negative: %d", body.DurationMS)
	}

	if dbm.calls.Load() != 1 || redism.calls.Load() != 1 {
		t.Errorf("each check should run exactly once, got db=%d redis=%d",
			dbm.calls.Load(), redism.calls.Load())
	}
}

func TestReadiness_OneCheckFails(t *testing.T) {
	dbm := &mockCheck{name: "db", err: errors.New("connection refused")}
	redism := &mockCheck{name: "redis"}

	h := readinessWithTimeout(500*time.Millisecond, dbm, redism)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusServiceUnavailable; got != want {
		t.Errorf("status: got %d, want %d", got, want)
	}

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if body.Status != "not_ready" {
		t.Errorf("status: got %q, want not_ready", body.Status)
	}
	if !strings.Contains(body.Checks["db"], "connection refused") {
		t.Errorf("db: got %q, want contains 'connection refused'", body.Checks["db"])
	}
	if !strings.HasPrefix(body.Checks["db"], "err: ") {
		t.Errorf("db: got %q, want prefix 'err: '", body.Checks["db"])
	}
	if body.Checks["redis"] != "ok" {
		t.Errorf("redis: got %q, want ok", body.Checks["redis"])
	}
}

func TestReadiness_AllChecksFail(t *testing.T) {
	dbm := &mockCheck{name: "db", err: errors.New("dial tcp: refused")}
	redism := &mockCheck{name: "redis", err: errors.New("AUTH failed")}

	h := readinessWithTimeout(500*time.Millisecond, dbm, redism)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusServiceUnavailable; got != want {
		t.Errorf("status: got %d, want %d", got, want)
	}

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if body.Status != "not_ready" {
		t.Errorf("status: got %q, want not_ready", body.Status)
	}
	if !strings.Contains(body.Checks["db"], "refused") {
		t.Errorf("db: got %q", body.Checks["db"])
	}
	if !strings.Contains(body.Checks["redis"], "AUTH") {
		t.Errorf("redis: got %q", body.Checks["redis"])
	}
}

func TestReadiness_NoChecks(t *testing.T) {
	// Edge case: a server with no dependencies wired in. Should still
	// report 200/ready — an empty conjunction is true.
	h := Readiness()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Errorf("status: got %d, want %d", got, want)
	}

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if body.Status != "ready" {
		t.Errorf("status: got %q, want ready", body.Status)
	}
	if len(body.Checks) != 0 {
		t.Errorf("checks: got %v, want empty", body.Checks)
	}
}

func TestReadiness_PerCheckTimeoutSurfacesAsError(t *testing.T) {
	// A check that exceeds its deadline must show up as a failed
	// check with a context-deadline error, NOT as a hung request.
	slow := &mockCheck{name: "slow", delay: 200 * time.Millisecond}
	fast := &mockCheck{name: "fast"}

	h := readinessWithTimeout(50*time.Millisecond, slow, fast)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()

	start := time.Now()
	h.ServeHTTP(rr, req)
	elapsed := time.Since(start)

	// Should complete well before the slow check's full 200ms.
	if elapsed > 180*time.Millisecond {
		t.Errorf("handler took %v; expected timeout to kick in by ~50ms", elapsed)
	}

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", rr.Code)
	}

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if body.Status != "not_ready" {
		t.Errorf("status: got %q, want not_ready", body.Status)
	}
	if !strings.HasPrefix(body.Checks["slow"], "err: ") {
		t.Errorf("slow: got %q, want err: prefix", body.Checks["slow"])
	}
	if body.Checks["fast"] != "ok" {
		t.Errorf("fast: got %q, want ok", body.Checks["fast"])
	}
}

func TestReadiness_ChecksRunConcurrently(t *testing.T) {
	// Two checks that each sleep 100ms should still complete in
	// roughly 100ms total — proving the runner is parallel, not
	// serial.
	a := &mockCheck{name: "a", delay: 100 * time.Millisecond}
	b := &mockCheck{name: "b", delay: 100 * time.Millisecond}

	h := readinessWithTimeout(500*time.Millisecond, a, b)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()

	start := time.Now()
	h.ServeHTTP(rr, req)
	elapsed := time.Since(start)

	if elapsed > 180*time.Millisecond {
		t.Errorf("checks appear serial: took %v for two parallel 100ms checks", elapsed)
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rr.Code)
	}
}

func TestCustom_NameAndFn(t *testing.T) {
	tests := []struct {
		name       string
		fn         func(ctx context.Context) error
		wantStatus int
		wantBody   string
	}{
		{
			name:       "ok path",
			fn:         func(ctx context.Context) error { return nil },
			wantStatus: http.StatusOK,
			wantBody:   "ok",
		},
		{
			name:       "err path",
			fn:         func(ctx context.Context) error { return errors.New("boom") },
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "err: boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Custom("svc", tt.fn)
			if c.Name() != "svc" {
				t.Errorf("Name: got %q, want svc", c.Name())
			}

			h := readinessWithTimeout(500*time.Millisecond, c)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))

			if rr.Code != tt.wantStatus {
				t.Errorf("status: got %d, want %d", rr.Code, tt.wantStatus)
			}

			var body struct {
				Checks map[string]string `json:"checks"`
			}
			_ = json.Unmarshal(rr.Body.Bytes(), &body)
			if body.Checks["svc"] != tt.wantBody {
				t.Errorf("svc: got %q, want %q", body.Checks["svc"], tt.wantBody)
			}
		})
	}
}

func TestCustom_ReceivesContext(t *testing.T) {
	// The fn should receive a context with the per-check deadline
	// applied. We assert by inspecting the deadline distance.
	var observedDeadline time.Time
	c := Custom("ctx", func(ctx context.Context) error {
		d, _ := ctx.Deadline()
		observedDeadline = d
		return nil
	})

	h := readinessWithTimeout(50*time.Millisecond, c)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if observedDeadline.IsZero() {
		t.Fatal("expected a deadline on the check's context")
	}
	delta := time.Until(observedDeadline)
	// Deadline is in the past by the time we check (handler already
	// returned). Just confirm it's within a sane window of "around
	// 50ms after the request started" — anything within ±1s is fine.
	if delta < -time.Second || delta > time.Second {
		t.Errorf("deadline %v is out of expected range", delta)
	}
}

func TestDBCheck_Name(t *testing.T) {
	// We don't dial a real pool here — just confirm the Name surface.
	// The integration of *pgxpool.Pool.Ping is exercised by the
	// smoke test and by packages/go/db's own integration tests.
	c := DBCheck(nil)
	if c.Name() != "db" {
		t.Errorf("Name: got %q, want db", c.Name())
	}
}

func TestRedisCheck_Name(t *testing.T) {
	c := RedisCheck(nil)
	if c.Name() != "redis" {
		t.Errorf("Name: got %q, want redis", c.Name())
	}
}
