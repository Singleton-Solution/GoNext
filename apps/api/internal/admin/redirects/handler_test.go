package redirects

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	rdr "github.com/Singleton-Solution/GoNext/packages/go/redirects"
)

func newTestMux(t *testing.T) (*http.ServeMux, *rdr.InMemoryStore) {
	t.Helper()
	store := rdr.NewInMemoryStore()
	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1/admin/redirects", Deps{Store: store}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return mux, store
}

func TestHandler_CreateAndList(t *testing.T) {
	mux, _ := newTestMux(t)
	body, _ := json.Marshal(map[string]any{
		"source_path":      "/old",
		"destination_path": "/new",
		"status":           301,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/redirects", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/redirects", nil)
	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status=%d", listRec.Code)
	}
	if !strings.Contains(listRec.Body.String(), "/old") {
		t.Fatalf("list body did not contain rule: %s", listRec.Body.String())
	}
}

func TestHandler_RejectsBadStatus(t *testing.T) {
	mux, _ := newTestMux(t)
	body, _ := json.Marshal(map[string]any{
		"source_path":      "/x",
		"destination_path": "/y",
		"status":           418,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/redirects", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_TestRegex(t *testing.T) {
	mux, _ := newTestMux(t)
	body, _ := json.Marshal(map[string]any{
		"pattern":     "^/blog/(.+)$",
		"destination": "/posts/$1",
		"sample_path": "/blog/hello",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/redirects/test-regex", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "/posts/hello") {
		t.Fatalf("response missing substituted destination: %s", rec.Body.String())
	}
}

func TestHandler_DuplicateConflict(t *testing.T) {
	mux, _ := newTestMux(t)
	body, _ := json.Marshal(map[string]any{
		"source_path":      "/dup",
		"destination_path": "/x",
		"status":           301,
	})
	for i, want := range []int{http.StatusCreated, http.StatusConflict} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
			"/api/v1/admin/redirects", bytes.NewReader(body)))
		if rec.Code != want {
			t.Fatalf("attempt %d: status=%d want %d body=%s", i, rec.Code, want, rec.Body.String())
		}
	}
}
