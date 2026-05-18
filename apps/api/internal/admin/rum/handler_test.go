package rum

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

func TestMount_RequiresStore(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	err := Mount(mux, "/_/rum/beacon", "/api/v1/admin/rum", Deps{
		Policy: policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
	})
	if err == nil {
		t.Fatal("expected error when Store is nil")
	}
}

func TestMount_RequiresPolicy(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	err := Mount(mux, "/_/rum/beacon", "/api/v1/admin/rum", Deps{
		Store: NewMemoryStore(),
	})
	if err == nil {
		t.Fatal("expected error when Policy is nil")
	}
}

func TestMount_RoutesAreRegistered(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	if err := Mount(mux, "/_/rum/beacon", "/api/v1/admin/rum", Deps{
		Store:  NewMemoryStore(),
		Policy: policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	// Beacon: POST works (returns 400 for empty body, but the
	// route is mounted), GET returns 405.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_/rum/beacon", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 from GET /_/rum/beacon; got %d", rec.Code)
	}

	// Percentiles: returns 401 without principal (handler ran).
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/rum/percentiles?metric=LCP", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 from /percentiles; got %d", rec.Code)
	}

	// Slow-routes: returns 401 without principal (handler ran).
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/rum/slow-routes?metric=LCP", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 from /slow-routes; got %d", rec.Code)
	}
}

func TestMount_BeaconMiddlewareWraps(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	var hits int
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits++
			next.ServeHTTP(w, r)
		})
	}
	if err := Mount(mux, "/_/rum/beacon", "/api/v1/admin/rum", Deps{
		Store:            NewMemoryStore(),
		Policy:           policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
		BeaconMiddleware: mw,
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/_/rum/beacon", strings.NewReader("")))
	if hits != 1 {
		t.Fatalf("expected middleware to run once; got %d", hits)
	}
}
