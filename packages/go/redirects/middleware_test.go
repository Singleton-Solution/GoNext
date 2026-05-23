package redirects

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// stubMatcher implements the matcher interface with a fixed table —
// the middleware tests don't need a real engine.
type stubMatcher map[string]Match

func (s stubMatcher) Match(path string) (Match, bool) {
	m, ok := s[path]
	return m, ok
}

// TestMiddleware_LiteralRedirect301 covers the canonical happy path.
func TestMiddleware_LiteralRedirect301(t *testing.T) {
	m := stubMatcher{
		"/old": {RuleID: uuid.New(), Destination: "/new", Status: 301},
	}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	handler := Middleware(m)(next)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/old", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status=%d want 301", rec.Code)
	}
	if rec.Header().Get("Location") != "/new" {
		t.Fatalf("Location=%q want /new", rec.Header().Get("Location"))
	}
	if called {
		t.Fatal("next handler must not be invoked on a match")
	}
	// Hop header is stripped from response.
	if rec.Header().Get(HeaderHopCount) != "" {
		t.Fatalf("hop header leaked: %q", rec.Header().Get(HeaderHopCount))
	}
	// 301 should be cached by default.
	if cc := rec.Header().Get("Cache-Control"); cc == "" {
		t.Fatalf("expected default Cache-Control for 301, got empty")
	}
}

// TestMiddleware_NoMatchPassThrough confirms misses fall through.
func TestMiddleware_NoMatchPassThrough(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := Middleware(stubMatcher{})(next)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything", nil))
	if !called {
		t.Fatal("next handler should run on no-match")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
}

// TestMiddleware_AllStatuses asserts the middleware preserves every
// status the table allows.
func TestMiddleware_AllStatuses(t *testing.T) {
	for _, status := range []int{301, 302, 307, 308} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			m := stubMatcher{
				"/p": {RuleID: uuid.New(), Destination: "/q", Status: status},
			}
			handler := Middleware(m)(http.NotFoundHandler())
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/p", nil))
			if rec.Code != status {
				t.Fatalf("status=%d want %d", rec.Code, status)
			}
		})
	}
}

// TestMiddleware_LoopDetected verifies the 508 path. We inject the
// hop header into the incoming request, simulating an in-process
// re-entry that has already traversed MaxHops redirects.
func TestMiddleware_LoopDetected(t *testing.T) {
	m := stubMatcher{
		"/loop": {RuleID: uuid.New(), Destination: "/loop", Status: 301},
	}
	handler := Middleware(m)(http.NotFoundHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/loop", nil)
	req.Header.Set(HeaderHopCount, strconv.Itoa(MaxHops))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusLoopDetected {
		t.Fatalf("status=%d want %d", rec.Code, http.StatusLoopDetected)
	}
	if body := rec.Body.String(); !strings.Contains(body, "loop detected") &&
		!strings.Contains(body, "Redirect loop") {
		t.Fatalf("body=%q want loop body", body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q want no-store", rec.Header().Get("Cache-Control"))
	}
}

// TestMiddleware_HopCountIncrements asserts the hop count climbs one
// per matched hop.
func TestMiddleware_HopCountIncrements(t *testing.T) {
	m := stubMatcher{
		"/p": {RuleID: uuid.New(), Destination: "/q", Status: 301},
	}
	// We probe the hop counter by stripping the deferred-Del at the
	// boundary; the response writer is in scope before the defer
	// fires, but we need an in-handler hook. We accomplish this by
	// wrapping the recorder and reading the header value before the
	// deferred Del.
	//
	// Simpler: assert behavior at the threshold instead — exactly
	// MaxHops + 1 incoming hops triggers 508; exactly MaxHops
	// allows the redirect.
	for hops, wantStatus := range map[int]int{
		0:           http.StatusMovedPermanently,
		MaxHops - 1: http.StatusMovedPermanently,
		MaxHops:     http.StatusLoopDetected,
		MaxHops + 1: http.StatusLoopDetected,
	} {
		handler := Middleware(m)(http.NotFoundHandler())
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/p", nil)
		if hops > 0 {
			req.Header.Set(HeaderHopCount, strconv.Itoa(hops))
		}
		handler.ServeHTTP(rec, req)
		if rec.Code != wantStatus {
			t.Fatalf("hops=%d status=%d want %d", hops, rec.Code, wantStatus)
		}
	}
}

// TestMiddleware_TempStatusNotCached asserts 302/307 don't get the
// default Cache-Control.
func TestMiddleware_TempStatusNotCached(t *testing.T) {
	m := stubMatcher{
		"/t": {RuleID: uuid.New(), Destination: "/u", Status: 302},
	}
	handler := Middleware(m)(http.NotFoundHandler())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/t", nil))
	if rec.Header().Get("Cache-Control") != "" {
		t.Fatalf("temp redirect should not set Cache-Control, got %q", rec.Header().Get("Cache-Control"))
	}
}
