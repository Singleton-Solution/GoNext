package audit

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/log"
)

// failingStore wraps a real store but always returns the configured
// error from Emit. Used to exercise the middleware's failure path.
type failingStore struct {
	err error
}

func (s *failingStore) Emit(ctx context.Context, _ Event) error { return s.err }
func (s *failingStore) List(_ context.Context, _ Filter) ([]Event, error) {
	return nil, nil
}

// recordingRecorder captures the labels passed to IncEmitFailure.
type recordingRecorder struct {
	mu     sync.Mutex
	calls  []map[string]string
}

func (r *recordingRecorder) IncEmitFailure(labels map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Copy labels to avoid sharing the map with the caller.
	cp := make(map[string]string, len(labels))
	for k, v := range labels {
		cp[k] = v
	}
	r.calls = append(r.calls, cp)
}

func (r *recordingRecorder) snapshot() []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]string, len(r.calls))
	copy(out, r.calls)
	return out
}

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

func TestMiddleware_EmitFailure_LogsAtWarn_AndIncrementsMetric(t *testing.T) {
	wantErr := errors.New("audit store down")
	emitter := NewEmitter(&failingStore{err: wantErr})
	recorder := &recordingRecorder{}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	completed := false
	h := Middleware(emitter, WithEmitFailureRecorder(recorder))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the user-facing request is NOT aborted: handler still runs.
		completed = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "203.0.113.4:1234"
	// Attach the test logger to the request context so the middleware
	// emits to our buffer (otherwise it hits slog.Default).
	ctx := log.WithLogger(req.Context(), logger)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !completed {
		t.Error("downstream handler not called — emit failure must not abort the request")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d want 200 (emit failure must not change response)", rec.Code)
	}

	calls := recorder.snapshot()
	if len(calls) != 1 {
		t.Fatalf("recorder calls: got %d want 1", len(calls))
	}
	if got := calls[0]["event_type"]; got != "http.request" {
		t.Errorf("event_type label: got %q want http.request", got)
	}

	logged := buf.String()
	if !strings.Contains(logged, `"level":"WARN"`) {
		t.Errorf("expected WARN-level log line, got: %s", logged)
	}
	if !strings.Contains(logged, "audit: emit failed") {
		t.Errorf("expected emit-failure message, got: %s", logged)
	}
	if !strings.Contains(logged, "audit store down") {
		t.Errorf("expected wrapped error in log, got: %s", logged)
	}
}

func TestMiddleware_EmitFailure_DefaultCounter_UsedWhenNoRecorder(t *testing.T) {
	// When no EmitFailureRecorder is wired in, the middleware falls
	// back to the package-default counter. Operators who haven't yet
	// integrated a metrics pipeline still get a process-local count.
	ResetDefaultEmitFailureCount()
	t.Cleanup(ResetDefaultEmitFailureCount)

	emitter := NewEmitter(&failingStore{err: errors.New("boom")})
	// No WithEmitFailureRecorder option supplied.
	h := Middleware(emitter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/x", nil)
		req.RemoteAddr = "203.0.113.4:1234"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	if got := DefaultEmitFailureCount("http.request"); got != 3 {
		t.Errorf("per-event counter: got %d want 3", got)
	}
	if got := DefaultEmitFailureCount(""); got != 3 {
		t.Errorf("total counter: got %d want 3", got)
	}
}

func TestMiddleware_EmitFailure_NotInvokedOnSafeMethods(t *testing.T) {
	// Safe methods should not trigger emit at all, so a failing store
	// should produce zero recorder calls.
	emitter := NewEmitter(&failingStore{err: errors.New("nope")})
	recorder := &recordingRecorder{}
	h := Middleware(emitter, WithEmitFailureRecorder(recorder))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		req := httptest.NewRequest(m, "/x", nil)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	if got := recorder.snapshot(); len(got) != 0 {
		t.Errorf("recorder called for safe methods: %v", got)
	}
}

func TestMiddleware_IncludesRequestIDInMetadata(t *testing.T) {
	// When the request carries X-Request-Id (set by httpx.RequestID),
	// the middleware must propagate it into Metadata["request_id"] so
	// audit rows correlate with HTTP request logs.
	store := NewMemoryStore()
	emitter := NewEmitter(store)
	h := Middleware(emitter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const rid = "req-abc-123-456-789ab"
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "203.0.113.4:1234"
	req.Header.Set(HeaderRequestID, rid)
	h.ServeHTTP(httptest.NewRecorder(), req)

	events, _ := store.List(context.Background(), Filter{})
	if len(events) != 1 {
		t.Fatalf("len: got %d want 1", len(events))
	}
	got, _ := events[0].Metadata["request_id"].(string)
	if got != rid {
		t.Errorf("request_id metadata: got %q want %q", got, rid)
	}
}

func TestMiddleware_OmitsRequestIDWhenHeaderMissing(t *testing.T) {
	// If no X-Request-Id header is present, the middleware must not
	// invent one — that's httpx.RequestID's job. The metadata key
	// should simply be absent.
	store := NewMemoryStore()
	emitter := NewEmitter(store)
	h := Middleware(emitter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "203.0.113.4:1234"
	h.ServeHTTP(httptest.NewRecorder(), req)

	events, _ := store.List(context.Background(), Filter{})
	if _, ok := events[0].Metadata["request_id"]; ok {
		t.Errorf("request_id should be absent when header is missing: %+v", events[0].Metadata)
	}
}

func TestEmitFailureFunc_AdaptsPlainFunction(t *testing.T) {
	var seen map[string]string
	f := EmitFailureFunc(func(labels map[string]string) {
		seen = labels
	})
	f.IncEmitFailure(map[string]string{"event_type": "x"})
	if seen["event_type"] != "x" {
		t.Errorf("EmitFailureFunc did not forward labels: %v", seen)
	}
}
