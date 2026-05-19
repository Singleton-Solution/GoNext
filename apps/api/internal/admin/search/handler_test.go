package search_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	adminsearch "github.com/Singleton-Solution/GoNext/apps/api/internal/admin/search"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	pkgsearch "github.com/Singleton-Solution/GoNext/packages/go/search"
)

// fakeSearcher is a programmable Searcher: callers preload it with a
// canned result (or error) and read back the captured query/opts to
// assert what the handler asked for.
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

// withPrincipal wraps a request with an authenticated principal on
// the context so the handler doesn't 401.
func withPrincipal(r *http.Request) *http.Request {
	pr := policy.Principal{UserID: "u-1", Roles: []policy.Role{"editor"}}
	return r.WithContext(policy.WithPrincipal(r.Context(), pr))
}

// TestServeHTTP_HappyPath asserts the handler hands the query and
// parsed opts to the Searcher and writes the result as JSON.
func TestServeHTTP_HappyPath(t *testing.T) {
	fs := &fakeSearcher{
		res: &pkgsearch.Results{
			Hits: []pkgsearch.Hit{
				{ID: "p1", Type: "post", Title: "Hello"},
			},
			Total: 1,
		},
	}
	h := adminsearch.NewHandler(fs, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/search?q=hello&types=post,page&limit=10", nil)
	req = withPrincipal(req)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if fs.gotQ != "hello" {
		t.Errorf("captured query = %q, want hello", fs.gotQ)
	}
	if len(fs.gotOp.Types) != 2 || fs.gotOp.Types[0] != "post" || fs.gotOp.Types[1] != "page" {
		t.Errorf("captured Types = %#v, want [post page]", fs.gotOp.Types)
	}
	if fs.gotOp.Limit != 10 {
		t.Errorf("captured Limit = %d, want 10", fs.gotOp.Limit)
	}

	var body pkgsearch.Results
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; raw=%q", err, rec.Body.String())
	}
	if len(body.Hits) != 1 || body.Hits[0].ID != "p1" {
		t.Errorf("decoded body Hits = %#v", body.Hits)
	}
}

// TestServeHTTP_EmptyQuery returns 400 without invoking Search.
func TestServeHTTP_EmptyQuery(t *testing.T) {
	fs := &fakeSearcher{}
	h := adminsearch.NewHandler(fs, nil)

	req := withPrincipal(httptest.NewRequest(http.MethodGet, "/api/v1/admin/search?q=", nil))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if fs.gotQ != "" {
		t.Errorf("Search invoked for empty q (got %q)", fs.gotQ)
	}
}

// TestServeHTTP_WrongMethod returns 405 with an Allow header.
func TestServeHTTP_WrongMethod(t *testing.T) {
	h := adminsearch.NewHandler(&fakeSearcher{}, nil)

	req := withPrincipal(httptest.NewRequest(http.MethodPost, "/api/v1/admin/search?q=hi", nil))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != http.MethodGet {
		t.Errorf("Allow header = %q, want GET", got)
	}
}

// TestServeHTTP_StoreError surfaces a generic 500 with the error
// logged but not leaked into the response body.
func TestServeHTTP_StoreError(t *testing.T) {
	fs := &fakeSearcher{err: errors.New("boom")}
	h := adminsearch.NewHandler(fs, nil)

	req := withPrincipal(httptest.NewRequest(http.MethodGet, "/api/v1/admin/search?q=hello", nil))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("response leaked underlying error: %q", rec.Body.String())
	}
}

// TestMount_RequiresPrincipal: a request with no principal on the
// context (i.e. not authenticated) gets 401, not 500.
func TestMount_RequiresPrincipal(t *testing.T) {
	fs := &fakeSearcher{res: &pkgsearch.Results{}}
	h := adminsearch.NewHandler(fs, nil)

	mux := http.NewServeMux()
	if err := adminsearch.Mount(mux, "/api/v1/admin", policy.NewBasicPolicy(nil), h); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/search?q=hello", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// TestNewHandler_NilSearcherPanics is the boot-safety guarantee.
func TestNewHandler_NilSearcherPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("NewHandler(nil) did not panic")
		}
	}()
	_ = adminsearch.NewHandler(nil, nil)
}
