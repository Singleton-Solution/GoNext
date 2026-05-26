package httpcache_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/middleware/httpcache"
)

func TestMiddleware_EmitsETagOnGET(t *testing.T) {
	h := httpcache.Middleware(httpcache.Options{
		Vary:         []string{"Accept-Encoding"},
		CacheControl: "public, max-age=60",
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if etag := rec.Header().Get("ETag"); etag == "" {
		t.Fatalf("expected ETag header to be set")
	}
	if vary := rec.Header().Get("Vary"); vary != "Accept-Encoding" {
		t.Fatalf("Vary: got %q, want %q", vary, "Accept-Encoding")
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=60" {
		t.Fatalf("Cache-Control: got %q", cc)
	}
}

func TestMiddleware_ShortCircuitsOnIfNoneMatch(t *testing.T) {
	body := []byte(`{"ok":true}`)
	h := httpcache.Middleware(httpcache.Options{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))

	// First request to discover ETag.
	req1 := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	etag := rec1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("first response missing ETag")
	}

	// Second request echoes back the ETag.
	req2 := httptest.NewRequest(http.MethodGet, "/x", nil)
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNotModified {
		t.Fatalf("status: got %d, want 304", rec2.Code)
	}
	if got := rec2.Body.Len(); got != 0 {
		t.Fatalf("304 response should have empty body, got %d bytes", got)
	}
	if rec2.Header().Get("ETag") != etag {
		t.Fatalf("ETag should round-trip on 304")
	}
}

func TestMiddleware_PassthroughOnPOST(t *testing.T) {
	h := httpcache.Middleware(httpcache.Options{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(""))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("ETag") != "" {
		t.Fatalf("POST should not produce ETag")
	}
}

func TestMiddleware_HonorsNoStore(t *testing.T) {
	h := httpcache.Middleware(httpcache.Options{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte("secret"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("ETag") != "" {
		t.Fatalf("no-store response should not produce ETag")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control: got %q", rec.Header().Get("Cache-Control"))
	}
}

func TestMiddleware_HonorsPrivate(t *testing.T) {
	h := httpcache.Middleware(httpcache.Options{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, max-age=0")
		_, _ = w.Write([]byte("session-scoped"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("ETag") != "" {
		t.Fatalf("private response should not produce ETag")
	}
}

func TestMiddleware_MergesExistingVary(t *testing.T) {
	h := httpcache.Middleware(httpcache.Options{
		Vary: []string{"Accept-Language"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Vary", "Origin")
		_, _ = w.Write([]byte("x"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	vary := rec.Header().Get("Vary")
	if !strings.Contains(vary, "Origin") || !strings.Contains(vary, "Accept-Language") {
		t.Fatalf("Vary should merge: got %q", vary)
	}
}

func TestMiddleware_WildcardIfNoneMatch(t *testing.T) {
	h := httpcache.Middleware(httpcache.Options{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("anything"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("If-None-Match", "*")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Fatalf("status: got %d, want 304 for *", rec.Code)
	}
}

func TestMiddleware_OverflowFallsThrough(t *testing.T) {
	// Body larger than the configured max — middleware should stream
	// it directly without an ETag.
	big := strings.Repeat("a", 4096)
	h := httpcache.Middleware(httpcache.Options{MaxBodyBytes: 64})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, big)
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("ETag") != "" {
		t.Fatalf("overflow path should skip ETag")
	}
	if rec.Body.Len() != len(big) {
		t.Fatalf("body length: got %d, want %d", rec.Body.Len(), len(big))
	}
}

func TestMiddleware_WeakETagAccepted(t *testing.T) {
	h := httpcache.Middleware(httpcache.Options{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	req1 := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	etag := rec1.Header().Get("ETag")

	// Echo as weak form.
	req2 := httptest.NewRequest(http.MethodGet, "/x", nil)
	req2.Header.Set("If-None-Match", "W/"+etag)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNotModified {
		t.Fatalf("status: got %d, want 304 (weak compare)", rec2.Code)
	}
}
