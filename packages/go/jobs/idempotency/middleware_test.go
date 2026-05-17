package idempotency

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// helloHandler is the inner handler under test. It counts invocations
// so the test can assert "handler was NOT called on replay".
type helloHandler struct {
	calls atomic.Int64
	body  string
	code  int
}

func (h *helloHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.calls.Add(1)
	if h.code != 0 {
		w.WriteHeader(h.code)
	}
	body := h.body
	if body == "" {
		body = `{"ok":true}`
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

// memoryStore is a Store implementation that holds entries in-memory
// for the middleware tests. We don't reuse RedisStore-with-fakeRedis
// here because we want the middleware tests to be agnostic to the
// backing tier — they test the HTTP state machine, not the store
// internals.
type memoryStore struct {
	mu      sync.Mutex
	entries map[string]memEntry
}

type memEntry struct {
	hash   []byte
	status Status
	result Result
	expiry time.Time
}

func newMemoryStore() *memoryStore {
	return &memoryStore{entries: map[string]memEntry{}}
}

func (m *memoryStore) Claim(_ context.Context, k Key, ttl time.Duration) (ClaimOutcome, Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	e, ok := m.entries[k.Value]
	if ok && !e.expiry.After(now) {
		delete(m.entries, k.Value)
		ok = false
	}
	if !ok {
		m.entries[k.Value] = memEntry{
			hash:   k.RequestHash,
			status: StatusInProgress,
			expiry: now.Add(ttl),
		}
		return ClaimNew, Result{}, nil
	}
	if string(e.hash) != string(k.RequestHash) {
		return ClaimMismatch, Result{}, nil
	}
	if e.status == StatusInProgress {
		return ClaimPending, Result{}, nil
	}
	return ClaimReplay, e.result, nil
}

func (m *memoryStore) Finish(_ context.Context, k Key, status Status, result Result, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[k.Value]
	e.status = status
	e.result = result
	e.expiry = time.Now().Add(ttl)
	m.entries[k.Value] = e
	return nil
}

func (m *memoryStore) Get(_ context.Context, k Key) (Status, Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[k.Value]
	if !ok || e.status == StatusInProgress {
		return "", Result{}, ErrNotFound
	}
	return e.status, e.result, nil
}

// blockingMemoryStore wraps memoryStore so a test can hold the first
// Claim in-progress while a second request races to hit the Pending
// path.
type blockingMemoryStore struct {
	*memoryStore
	hold chan struct{}
}

// TestMiddleware_FirstRequestRunsHandler is the happy-path smoke
// test: a fresh key with a body invokes the inner handler and
// returns its response.
func TestMiddleware_FirstRequestRunsHandler(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	mw := New(store, Config{})
	h := &helloHandler{}
	srv := mw.Wrap(h)

	r := httptest.NewRequest("POST", "/api/payments", strings.NewReader(`{"amt":42}`))
	r.Header.Set(HeaderName, "key-first")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d, want 200", w.Code)
	}
	if h.calls.Load() != 1 {
		t.Fatalf("handler calls: %d, want 1", h.calls.Load())
	}
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("body: %q", w.Body.String())
	}
}

// TestMiddleware_ReplayReturnsStoredResult is the canonical replay:
// same key + same body → stored response, handler NOT called again.
func TestMiddleware_ReplayReturnsStoredResult(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	mw := New(store, Config{})
	h := &helloHandler{body: `{"created":"abc"}`, code: http.StatusCreated}
	srv := mw.Wrap(h)

	const key = "key-replay"
	const body = `{"amt":42}`

	// First request: handler runs, result stored.
	r1 := httptest.NewRequest("POST", "/api/payments", strings.NewReader(body))
	r1.Header.Set(HeaderName, key)
	w1 := httptest.NewRecorder()
	srv.ServeHTTP(w1, r1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first: status %d, want 201", w1.Code)
	}

	// Second request: same key, same body, NEW recorder. Handler
	// must not be invoked; recorder shows the stored 201.
	r2 := httptest.NewRequest("POST", "/api/payments", strings.NewReader(body))
	r2.Header.Set(HeaderName, key)
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, r2)

	if h.calls.Load() != 1 {
		t.Fatalf("handler invoked %d times, want 1 (no replay re-run)", h.calls.Load())
	}
	if w2.Code != http.StatusCreated {
		t.Fatalf("replay: status %d, want 201", w2.Code)
	}
	if w2.Body.String() != `{"created":"abc"}` {
		t.Fatalf("replay body: %q", w2.Body.String())
	}
	if w2.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("Idempotency-Replayed header missing")
	}
}

// TestMiddleware_DifferentBodyReturns422 is the mismatch path.
func TestMiddleware_DifferentBodyReturns422(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	mw := New(store, Config{})
	h := &helloHandler{}
	srv := mw.Wrap(h)

	r1 := httptest.NewRequest("POST", "/api/payments", strings.NewReader(`{"a":1}`))
	r1.Header.Set(HeaderName, "key-mismatch")
	srv.ServeHTTP(httptest.NewRecorder(), r1)

	r2 := httptest.NewRequest("POST", "/api/payments", strings.NewReader(`{"b":2}`))
	r2.Header.Set(HeaderName, "key-mismatch")
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, r2)

	if w2.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: %d, want 422", w2.Code)
	}
	var eb errorBody
	if err := json.Unmarshal(w2.Body.Bytes(), &eb); err != nil {
		t.Fatalf("unmarshal error body: %v", err)
	}
	if eb.ErrorCode != errCodeReused {
		t.Fatalf("error_code: %q, want %q", eb.ErrorCode, errCodeReused)
	}
}

// TestMiddleware_ConcurrentClaimsOneWins exercises the in_progress
// state machine. Two goroutines fire the same request simultaneously;
// one gets a normal 200, the other gets 409.
func TestMiddleware_ConcurrentClaimsOneWins(t *testing.T) {
	t.Parallel()

	// Use a handler that blocks on a channel so we control the race
	// precisely — without this the first request can complete before
	// the second starts.
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	store := newMemoryStore()
	mw := New(store, Config{})
	srv := mw.Wrap(handler)

	const key = "key-concurrent"
	const body = `{"amt":42}`

	startedFirst := make(chan struct{})
	type result struct {
		code int
		body string
	}
	results := make(chan result, 2)

	go func() {
		r := httptest.NewRequest("POST", "/api/payments", strings.NewReader(body))
		r.Header.Set(HeaderName, key)
		w := httptest.NewRecorder()
		close(startedFirst)
		srv.ServeHTTP(w, r)
		results <- result{code: w.Code, body: w.Body.String()}
	}()

	<-startedFirst
	// Give the goroutine a moment to claim — the memory store's
	// Claim is non-blocking but the goroutine scheduling isn't free.
	// We loop until we observe the in-progress entry.
	deadline := time.Now().Add(time.Second)
	for {
		k := Key{Value: key, RequestHash: makeKey(t, key, body).RequestHash}
		st, _, _ := store.Get(context.Background(), k)
		// in_progress entries are reported as ErrNotFound by Get,
		// so we instead look directly at the map under the lock.
		store.mu.Lock()
		_, ok := store.entries[key]
		store.mu.Unlock()
		_ = st
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first goroutine never claimed")
		}
		time.Sleep(time.Millisecond)
	}

	// Second request races against the still-pending first.
	r2 := httptest.NewRequest("POST", "/api/payments", strings.NewReader(body))
	r2.Header.Set(HeaderName, key)
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, r2)
	results <- result{code: w2.Code, body: w2.Body.String()}

	close(release)

	// Collect both. Exactly one is 200, exactly one is 409.
	got := []result{<-results, <-results}
	var has200, has409 bool
	for _, r := range got {
		if r.code == http.StatusOK {
			has200 = true
		}
		if r.code == http.StatusConflict {
			has409 = true
		}
	}
	if !has200 || !has409 {
		t.Fatalf("expected one 200 and one 409, got %+v", got)
	}
}

// TestMiddleware_NoKeyHeaderPassThrough confirms that requests
// without an Idempotency-Key bypass the middleware entirely.
func TestMiddleware_NoKeyHeaderPassThrough(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	mw := New(store, Config{})
	h := &helloHandler{}
	srv := mw.Wrap(h)

	r := httptest.NewRequest("POST", "/api/payments", strings.NewReader(`{"a":1}`))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)

	if h.calls.Load() != 1 {
		t.Fatalf("handler not called: %d", h.calls.Load())
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
}

// TestMiddleware_GetRequestsSkipMiddleware checks the default method
// allowlist — GET/HEAD/OPTIONS pass through.
func TestMiddleware_GetRequestsSkipMiddleware(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	mw := New(store, Config{})
	h := &helloHandler{}
	srv := mw.Wrap(h)

	r := httptest.NewRequest("GET", "/api/payments", nil)
	r.Header.Set(HeaderName, "key-get") // header is set but should be ignored
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)

	if h.calls.Load() != 1 {
		t.Fatalf("handler not called: %d", h.calls.Load())
	}

	// Second identical request: still calls the handler, the
	// store should never have seen the key.
	r2 := httptest.NewRequest("GET", "/api/payments", nil)
	r2.Header.Set(HeaderName, "key-get")
	srv.ServeHTTP(httptest.NewRecorder(), r2)

	if h.calls.Load() != 2 {
		t.Fatalf("handler should be called twice for GET, got %d", h.calls.Load())
	}
}

// TestMiddleware_RejectsInvalidHeader makes sure a malformed
// Idempotency-Key returns 400 without ever invoking the handler.
func TestMiddleware_RejectsInvalidHeader(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	mw := New(store, Config{})
	h := &helloHandler{}
	srv := mw.Wrap(h)

	r := httptest.NewRequest("POST", "/api/payments", strings.NewReader(`{}`))
	r.Header.Set(HeaderName, "bad\nkey")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: %d, want 400", w.Code)
	}
	if h.calls.Load() != 0 {
		t.Fatalf("handler called %d times for invalid header", h.calls.Load())
	}
	var eb errorBody
	if err := json.Unmarshal(w.Body.Bytes(), &eb); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if eb.ErrorCode != errCodeInvalidKey {
		t.Fatalf("error_code: %q", eb.ErrorCode)
	}
}

// TestMiddleware_StoresFailureResults verifies that a non-2xx
// response is also cached — a retry of a deterministic 422 should
// get the same 422, not a fresh execution that might find new state.
func TestMiddleware_StoresFailureResults(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	mw := New(store, Config{})
	h := &helloHandler{body: `{"error":"bad"}`, code: http.StatusUnprocessableEntity}
	srv := mw.Wrap(h)

	r1 := httptest.NewRequest("POST", "/api/payments", strings.NewReader(`{}`))
	r1.Header.Set(HeaderName, "fail-key")
	srv.ServeHTTP(httptest.NewRecorder(), r1)

	r2 := httptest.NewRequest("POST", "/api/payments", strings.NewReader(`{}`))
	r2.Header.Set(HeaderName, "fail-key")
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, r2)

	if h.calls.Load() != 1 {
		t.Fatalf("handler called %d times, want 1", h.calls.Load())
	}
	if w2.Code != http.StatusUnprocessableEntity {
		t.Fatalf("replay status: %d, want 422", w2.Code)
	}
	if w2.Body.String() != `{"error":"bad"}` {
		t.Fatalf("replay body: %q", w2.Body.String())
	}
}

// TestMiddleware_RejectsOversizedBody confirms the 413 path triggers
// before the inner handler ever sees the request.
func TestMiddleware_RejectsOversizedBody(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	mw := New(store, Config{MaxBodySize: 100})
	h := &helloHandler{}
	srv := mw.Wrap(h)

	r := httptest.NewRequest("POST", "/api/payments", strings.NewReader(strings.Repeat("x", 256)))
	r.Header.Set(HeaderName, "big-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: %d, want 413", w.Code)
	}
	if h.calls.Load() != 0 {
		t.Fatalf("handler called for oversized request")
	}
}

// TestMiddleware_TTLElapsesClaimResets ties the TTL config through
// to the underlying store. Once the entry expires, the same key with
// the same body invokes the handler again.
func TestMiddleware_TTLElapsesClaimResets(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	mw := New(store, Config{TTL: 50 * time.Millisecond})
	h := &helloHandler{}
	srv := mw.Wrap(h)

	r1 := httptest.NewRequest("POST", "/api/payments", strings.NewReader(`{}`))
	r1.Header.Set(HeaderName, "ttl-key")
	srv.ServeHTTP(httptest.NewRecorder(), r1)

	// Wait past the TTL.
	time.Sleep(120 * time.Millisecond)

	r2 := httptest.NewRequest("POST", "/api/payments", strings.NewReader(`{}`))
	r2.Header.Set(HeaderName, "ttl-key")
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, r2)

	if h.calls.Load() != 2 {
		t.Fatalf("post-TTL handler calls: %d, want 2", h.calls.Load())
	}
	if w2.Code != http.StatusOK {
		t.Fatalf("status: %d", w2.Code)
	}
}

// TestMiddleware_PassesBodyToInnerHandler confirms that consuming the
// body to compute the canonical hash doesn't starve the handler — the
// inner sees the same bytes.
func TestMiddleware_PassesBodyToInnerHandler(t *testing.T) {
	t.Parallel()
	store := newMemoryStore()
	mw := New(store, Config{})

	got := make(chan string, 1)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- string(b)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	srv := mw.Wrap(h)
	r := httptest.NewRequest("POST", "/api/payments", strings.NewReader(`{"amount":42}`))
	r.Header.Set(HeaderName, "body-key")
	srv.ServeHTTP(httptest.NewRecorder(), r)

	select {
	case body := <-got:
		if body != `{"amount":42}` {
			t.Fatalf("inner body: %q, want %q", body, `{"amount":42}`)
		}
	case <-time.After(time.Second):
		t.Fatal("inner handler never called")
	}
}

// TestNew_AppliesDefaults checks the zero-config constructor wires up
// sensible defaults — TTL = 24h, body cap = 1 MiB, method allowlist =
// POST/PUT/PATCH/DELETE.
func TestNew_AppliesDefaults(t *testing.T) {
	t.Parallel()
	mw := New(newMemoryStore(), Config{})
	if mw.ttl != DefaultTTL {
		t.Errorf("ttl: %v, want %v", mw.ttl, DefaultTTL)
	}
	if mw.maxBody != DefaultMaxBodySize {
		t.Errorf("maxBody: %d, want %d", mw.maxBody, DefaultMaxBodySize)
	}
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if _, ok := mw.methods[m]; !ok {
			t.Errorf("method %s missing from allowlist", m)
		}
	}
	if _, ok := mw.methods["GET"]; ok {
		t.Errorf("GET should not be in default allowlist")
	}
}

// TestNew_AppliesMethodOverride confirms an explicit allowlist
// replaces — does not extend — the default.
func TestNew_AppliesMethodOverride(t *testing.T) {
	t.Parallel()
	mw := New(newMemoryStore(), Config{MethodAllowList: []string{"POST"}})
	if _, ok := mw.methods["POST"]; !ok {
		t.Error("POST missing")
	}
	if _, ok := mw.methods["PUT"]; ok {
		t.Error("PUT should not appear")
	}
}
