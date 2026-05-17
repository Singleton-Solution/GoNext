package audit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddleware_EmitsForStateChangingMethods(t *testing.T) {
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(m, func(t *testing.T) {
			store := NewMemoryStore()
			emitter := NewEmitter(store)

			called := false
			h := Middleware(emitter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(m, "/admin/users/42", nil)
			req.RemoteAddr = "203.0.113.4:1234"
			req.Header.Set("User-Agent", "test-ua")
			h.ServeHTTP(httptest.NewRecorder(), req)

			if !called {
				t.Error("handler not called")
			}

			events, _ := store.List(context.Background(), Filter{})
			if len(events) != 1 {
				t.Fatalf("len: got %d want 1", len(events))
			}
			got := events[0]
			if got.EventType != "http.request" {
				t.Errorf("EventType: got %q want http.request", got.EventType)
			}
			if got.IP != "203.0.113.4" {
				t.Errorf("IP: got %q", got.IP)
			}
			if got.UserAgent != "test-ua" {
				t.Errorf("UA: got %q", got.UserAgent)
			}
			if got.Metadata["method"] != m {
				t.Errorf("method metadata: got %v want %s", got.Metadata["method"], m)
			}
			if got.Metadata["path"] != "/admin/users/42" {
				t.Errorf("path metadata: got %v", got.Metadata["path"])
			}
		})
	}
}

func TestMiddleware_SkipsSafeMethods(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace} {
		t.Run(m, func(t *testing.T) {
			store := NewMemoryStore()
			emitter := NewEmitter(store)

			h := Middleware(emitter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(m, "/x", nil)
			h.ServeHTTP(httptest.NewRecorder(), req)

			events, _ := store.List(context.Background(), Filter{})
			if len(events) != 0 {
				t.Errorf("safe method %s emitted: %+v", m, events)
			}
		})
	}
}

func TestMiddleware_PanicsOnNilEmitter(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic")
		}
	}()
	Middleware(nil)
}

func TestMiddleware_PassesContextThrough(t *testing.T) {
	// The middleware should not strip context — handlers must still see
	// any values the caller put on the request context before us.
	type ctxKey string
	const k ctxKey = "marker"

	store := NewMemoryStore()
	emitter := NewEmitter(store)

	var seen any
	h := Middleware(emitter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Context().Value(k)
	}))

	ctx := context.WithValue(context.Background(), k, "carry")
	req := httptest.NewRequest(http.MethodPost, "/x", nil).WithContext(ctx)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if seen != "carry" {
		t.Errorf("context value lost: got %v", seen)
	}
}

func TestIsStateChanging(t *testing.T) {
	mutating := []string{"POST", "PUT", "PATCH", "DELETE"}
	safe := []string{"GET", "HEAD", "OPTIONS", "TRACE", "CONNECT", ""}
	for _, m := range mutating {
		if !isStateChanging(m) {
			t.Errorf("%q should be state-changing", m)
		}
	}
	for _, m := range safe {
		if isStateChanging(m) {
			t.Errorf("%q should NOT be state-changing", m)
		}
	}
}
