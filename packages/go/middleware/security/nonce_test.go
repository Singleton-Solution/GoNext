package security

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestWithNonce_SetsContextAndHeader verifies the documented contract:
// each request gets a fresh nonce, the nonce is exposed on
// X-Script-Nonce, and the same nonce is retrievable from the request
// context inside the wrapped handler.
func TestWithNonce_SetsContextAndHeader(t *testing.T) {
	var seenInHandler string
	h := WithNonce()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenInHandler = NonceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	got := rec.Header().Get(NonceHeader)
	if got == "" {
		t.Fatalf("X-Script-Nonce response header missing")
	}
	if seenInHandler == "" {
		t.Fatalf("NonceFromContext returned empty inside handler")
	}
	if got != seenInHandler {
		t.Errorf("nonce mismatch: header=%q, context=%q", got, seenInHandler)
	}
}

// TestWithNonce_FreshPerRequest exercises the per-request uniqueness
// guarantee. Two sequential requests through the same middleware
// instance must produce distinct nonces.
func TestWithNonce_FreshPerRequest(t *testing.T) {
	h := WithNonce()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	seen := map[string]struct{}{}
	for i := 0; i < 16; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		n := rec.Header().Get(NonceHeader)
		if n == "" {
			t.Fatalf("iter %d: empty nonce", i)
		}
		if _, dup := seen[n]; dup {
			t.Fatalf("iter %d: duplicate nonce %q", i, n)
		}
		seen[n] = struct{}{}
	}
}

// TestWithNonce_ConcurrentRequestsUnique runs many requests in parallel
// through one middleware instance and asserts no nonce collision. This
// catches accidental sharing of buffers or context keys across
// goroutines.
func TestWithNonce_ConcurrentRequestsUnique(t *testing.T) {
	h := WithNonce()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const n = 64
	var mu sync.Mutex
	seen := map[string]struct{}{}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			n := rec.Header().Get(NonceHeader)
			mu.Lock()
			seen[n] = struct{}{}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(seen) != n {
		t.Errorf("concurrent nonces collided: got %d unique, want %d", len(seen), n)
	}
}

// TestWithNonce_BytesAreBase64Encoded16 verifies that each nonce decodes
// to exactly 16 raw bytes — the documented entropy budget.
func TestWithNonce_BytesAreBase64Encoded16(t *testing.T) {
	h := WithNonce()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	got := rec.Header().Get(NonceHeader)
	raw, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		t.Fatalf("nonce %q is not valid base64: %v", got, err)
	}
	if len(raw) != 16 {
		t.Errorf("nonce raw length: got %d bytes, want 16", len(raw))
	}
}

// TestNonceFromContext_NilAndEmpty checks the two zero-value paths
// (no middleware → no nonce, nil ctx → empty).
func TestNonceFromContext_NilAndEmpty(t *testing.T) {
	if got := NonceFromContext(nil); got != "" {
		t.Errorf("nil ctx: got %q, want empty", got)
	}
	if got := NonceFromContext(context.Background()); got != "" {
		t.Errorf("background ctx: got %q, want empty", got)
	}
}

// TestNonceHeaderConstant pins the header name so that callers in
// downstream services (Next.js frontends) won't silently miss it after
// a rename.
func TestNonceHeaderConstant(t *testing.T) {
	if NonceHeader != "X-Script-Nonce" {
		t.Errorf("NonceHeader: got %q, want X-Script-Nonce", NonceHeader)
	}
}

// TestWithNonce_DownstreamCanComposeWithHeaders confirms the nonce and
// the security headers cooperate cleanly when stacked. WithNonce sits
// outside Headers because nonce delivery is independent of the header
// matrix.
func TestWithNonce_DownstreamCanComposeWithHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := WithNonce()(Headers(PublicSite())(inner))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get(NonceHeader); got == "" {
		t.Errorf("X-Script-Nonce missing when composed with Headers")
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options: got %q, want nosniff", got)
	}
}
