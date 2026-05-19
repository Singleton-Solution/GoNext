package search_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	restsearch "github.com/Singleton-Solution/GoNext/apps/api/internal/rest/search"
	pkgsearch "github.com/Singleton-Solution/GoNext/packages/go/search"
)

// fakeSearcher mirrors the admin/search test fake. It captures
// what the handler asked for so we can pin the public-endpoint
// invariants (Status always = "published", SkipTotal toggleable).
type fakeSearcher struct {
	res   *pkgsearch.Results
	err   error
	gotQ  string
	gotOp pkgsearch.SearchOpts
}

func (f *fakeSearcher) Search(_ context.Context, q string, opts pkgsearch.SearchOpts) (*pkgsearch.Results, error) {
	f.gotQ = q
	f.gotOp = opts
	if f.err != nil {
		return nil, f.err
	}
	return f.res, nil
}

// TestPublic_StatusIsPinnedToPublished is the headline safety
// contract: a client sending ?status=private must NOT be able to
// search private rows. The handler pins Status verbatim.
func TestPublic_StatusIsPinnedToPublished(t *testing.T) {
	fs := &fakeSearcher{res: &pkgsearch.Results{}}
	h := restsearch.NewHandler(fs, nil)

	// Even with the malicious query parameter present, the handler
	// must hand "published" — not "private" — to the searcher.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=secret&status=private", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if fs.gotOp.Status != "published" {
		t.Errorf("Status passed to searcher = %q, want published", fs.gotOp.Status)
	}
}

// TestPublic_SkipTotalDefaultTrue: the public endpoint defaults to
// SkipTotal=true to keep latency low; clients opt-in via ?total=1.
func TestPublic_SkipTotalDefaultTrue(t *testing.T) {
	fs := &fakeSearcher{res: &pkgsearch.Results{}}
	h := restsearch.NewHandler(fs, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=hello", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !fs.gotOp.SkipTotal {
		t.Errorf("SkipTotal = false by default, want true")
	}
}

// TestPublic_SkipTotalOptIn: ?total=1 flips the bit so the COUNT
// query runs.
func TestPublic_SkipTotalOptIn(t *testing.T) {
	fs := &fakeSearcher{res: &pkgsearch.Results{}}
	h := restsearch.NewHandler(fs, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=hello&total=1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if fs.gotOp.SkipTotal {
		t.Errorf("SkipTotal = true after ?total=1, want false")
	}
}

// TestPublic_EmptyQueryReturns400 documents the same contract as
// the admin endpoint.
func TestPublic_EmptyQueryReturns400(t *testing.T) {
	fs := &fakeSearcher{}
	h := restsearch.NewHandler(fs, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=%20%20", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// TestPublic_HappyPath: the handler writes a JSON body that
// round-trips into Results.
func TestPublic_HappyPath(t *testing.T) {
	fs := &fakeSearcher{
		res: &pkgsearch.Results{
			Hits: []pkgsearch.Hit{
				{ID: "p1", Type: "post", Title: "Hello", ExcerptHTML: "Hello <mark>world</mark>"},
			},
			Total: -1,
		},
	}
	h := restsearch.NewHandler(fs, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=world", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	var body pkgsearch.Results
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Hits) != 1 {
		t.Errorf("Hits = %#v", body.Hits)
	}
}

// TestPublic_StoreErrorReturns500 and does not leak the cause.
func TestPublic_StoreErrorReturns500(t *testing.T) {
	fs := &fakeSearcher{err: errors.New("postgres-down")}
	h := restsearch.NewHandler(fs, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=hi", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "postgres-down") {
		t.Errorf("response body leaked internal error")
	}
}

// TestPublic_NilLimiterMountStillWorks: a nil limiter (dev wiring)
// must not panic; the handler is mounted without throttling.
func TestPublic_NilLimiterMountStillWorks(t *testing.T) {
	fs := &fakeSearcher{res: &pkgsearch.Results{}}
	h := restsearch.NewHandler(fs, nil)

	mux := http.NewServeMux()
	if err := restsearch.Mount(mux, "/api/v1", nil, h); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=hi", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
}
