package ratelimit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// stubLimiter implements Limiter for middleware tests.
type stubLimiter struct {
	allow      bool
	retryAfter time.Duration
	err        error
	lastKey    string
}

func (s *stubLimiter) Allow(_ context.Context, key string) (bool, time.Duration, error) {
	s.lastKey = key
	return s.allow, s.retryAfter, s.err
}

// TestMiddleware_AllowsWhenLimiterPasses confirms a passing limiter
// forwards the request to the handler unchanged.
func TestMiddleware_AllowsWhenLimiterPasses(t *testing.T) {
	stub := &stubLimiter{allow: true}
	handled := false
	mw := Middleware(stub, func(*http.Request) string { return "test" })
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handled = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !handled {
		t.Error("handler was not called when limiter allows")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if stub.lastKey != "test" {
		t.Errorf("keyFn output = %q, want %q", stub.lastKey, "test")
	}
}

// TestMiddleware_Returns429WithRetryAfter is the headline test from the
// scope: limit exceeded → 429 + Retry-After header.
func TestMiddleware_Returns429WithRetryAfter(t *testing.T) {
	stub := &stubLimiter{allow: false, retryAfter: 5 * time.Second}
	called := false
	mw := Middleware(stub, func(*http.Request) string { return "ip-1" })
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if called {
		t.Error("handler should not be called when denied")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get(HeaderRetryAfter); got != "5" {
		t.Errorf("Retry-After = %q, want %q", got, "5")
	}
}

// TestMiddleware_RetryAfterRoundsUp ensures sub-second waits don't
// round to zero (which would tell the client to retry immediately).
func TestMiddleware_RetryAfterRoundsUp(t *testing.T) {
	cases := []struct {
		wait time.Duration
		want string
	}{
		{0, "1"},                       // floor
		{1 * time.Nanosecond, "1"},     // tiny wait → 1s
		{500 * time.Millisecond, "1"},  // sub-second → 1s
		{1500 * time.Millisecond, "2"}, // 1.5s → 2s
		{30 * time.Second, "30"},
	}
	for _, tc := range cases {
		t.Run(tc.wait.String(), func(t *testing.T) {
			stub := &stubLimiter{allow: false, retryAfter: tc.wait}
			mw := Middleware(stub, func(*http.Request) string { return "k" })
			h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			if got := rec.Header().Get(HeaderRetryAfter); got != tc.want {
				t.Errorf("Retry-After = %q, want %q (wait=%v)", got, tc.want, tc.wait)
			}
			// Parse must succeed per RFC 7231.
			if _, err := strconv.Atoi(rec.Header().Get(HeaderRetryAfter)); err != nil {
				t.Errorf("Retry-After is not an integer: %v", err)
			}
		})
	}
}

// TestMiddleware_EmptyKeyBypasses confirms the escape hatch: when keyFn
// returns "" the request is allowed without consulting the limiter.
func TestMiddleware_EmptyKeyBypasses(t *testing.T) {
	stub := &stubLimiter{allow: false}
	handled := false
	mw := Middleware(stub, func(*http.Request) string { return "" })
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handled = true
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !handled {
		t.Error("empty key should bypass the limiter")
	}
	if stub.lastKey != "" {
		t.Errorf("limiter should not have been called; lastKey=%q", stub.lastKey)
	}
}

// TestMiddleware_FailsOpenOnBackendError verifies that a Redis outage
// (or similar) does NOT 503 the user — the request proceeds, per the
// fail-open design decision documented in middleware.go.
func TestMiddleware_FailsOpenOnBackendError(t *testing.T) {
	stub := &stubLimiter{err: errors.New("redis unreachable")}
	handled := false
	mw := Middleware(stub, func(*http.Request) string { return "k" })
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handled = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !handled {
		t.Error("handler should be called when limiter errors (fail open)")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (fail open)", rec.Code)
	}
}

// TestMiddleware_NilDepsPanic enforces eager-panic on bad construction.
func TestMiddleware_NilDepsPanic(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil limiter")
		}
	}()
	_ = Middleware(nil, func(*http.Request) string { return "" })
}

func TestMiddleware_NilKeyFnPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil keyFn")
		}
	}()
	_ = Middleware(&stubLimiter{}, nil)
}

// TestKeyByIP_HonorsXFF verifies the leftmost X-Forwarded-For entry
// wins, with port stripping for RemoteAddr fallback.
func TestKeyByIP_HonorsXFF(t *testing.T) {
	cases := []struct {
		name       string
		xff        string
		remoteAddr string
		want       string
	}{
		{"xff single", "203.0.113.5", "192.0.2.1:9999", "203.0.113.5"},
		{"xff list", "203.0.113.5, 10.0.0.1, 10.0.0.2", "192.0.2.1:9999", "203.0.113.5"},
		{"xff trimmed", " 203.0.113.5 ", "192.0.2.1:9999", "203.0.113.5"},
		{"no xff", "", "192.0.2.1:9999", "192.0.2.1"},
		{"no xff no port", "", "192.0.2.1", "192.0.2.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := KeyByIP(req); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

// TestKeyByRemoteAddr ignores XFF entirely.
func TestKeyByRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:65535"
	req.Header.Set("X-Forwarded-For", "spoofed.example")
	if got := KeyByRemoteAddr(req); got != "192.0.2.1" {
		t.Errorf("got %q want 192.0.2.1", got)
	}
}
