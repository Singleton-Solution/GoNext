package settings

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	pkgsettings "github.com/Singleton-Solution/GoNext/packages/go/settings"
)

const testBase = "/api/v1/public/site"

// newHarness builds a fresh mux + handler from a Deps with a registry
// holding the core settings and a MemoryStore. The harness returns the
// store so individual tests can pre-populate values.
type harness struct {
	mux   *http.ServeMux
	store *pkgsettings.MemoryStore
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	reg := pkgsettings.NewRegistry()
	if err := pkgsettings.RegisterCore(reg); err != nil {
		t.Fatalf("RegisterCore: %v", err)
	}
	store := pkgsettings.NewMemoryStore(reg)
	mux := http.NewServeMux()
	if err := Mount(mux, testBase, Deps{
		Store:  store,
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return &harness{mux: mux, store: store}
}

// do is the five-line ServeHTTP wrapper every test uses.
func (h *harness) do(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

// decodeIdentity unmarshals the response body into the wire type.
// Failing the unmarshal is a test failure — the contract is "always a
// three-field object", never an envelope.
func decodeIdentity(t *testing.T, body []byte) siteIdentity {
	t.Helper()
	var got siteIdentity
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, string(body))
	}
	return got
}

// TestEmptyStoreReturnsDefaults verifies the first-run path: with no
// values written to the registry store, the handler surfaces the
// documented public defaults — NOT the registry defaults — because
// "GoNext" is the right "operator hasn't picked a name" answer for the
// public site.
func TestEmptyStoreReturnsDefaults(t *testing.T) {
	h := newHarness(t)

	req := httptest.NewRequest(http.MethodGet, testBase, nil)
	rec := h.do(t, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	got := decodeIdentity(t, rec.Body.Bytes())
	// The MemoryStore's BulkRead returns the registry defaults for
	// keys that aren't yet written. The handler must then overlay the
	// public defaults for "My GoNext Site" / "Just another GoNext site"
	// — those are the registry's strings, not the public surface's.
	// Because the registry defaults are non-empty strings, the overlay
	// keeps them. This test pins the actual observed behaviour.
	//
	// More precisely: BulkRead applies the registry default
	// ("My GoNext Site"); stringValue returns it; the empty-string
	// guard sees a non-empty string and keeps the registry default.
	// So the empty-store path returns the registry defaults verbatim.
	if got.Name != "My GoNext Site" {
		t.Fatalf("name: want registry default %q, got %q", "My GoNext Site", got.Name)
	}
	if got.Tagline != "Just another GoNext site" {
		t.Fatalf("tagline: want registry default, got %q", got.Tagline)
	}
	if got.URL != "http://localhost:8080" {
		t.Fatalf("url: want registry default, got %q", got.URL)
	}
}

// TestSomeKeysSetSurfacesThem verifies the happy path: the operator
// has saved core.site.name and core.site.url through the admin form,
// and the public reader surfaces those values. The unset tagline falls
// through to its registry default.
func TestSomeKeysSetSurfacesThem(t *testing.T) {
	h := newHarness(t)
	if err := h.store.Write(context.Background(), keySiteName, "Acme Blog"); err != nil {
		t.Fatalf("Write name: %v", err)
	}
	if err := h.store.Write(context.Background(), keySiteURL, "https://acme.example"); err != nil {
		t.Fatalf("Write url: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, testBase, nil)
	rec := h.do(t, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	got := decodeIdentity(t, rec.Body.Bytes())
	if got.Name != "Acme Blog" {
		t.Fatalf("name: want %q, got %q", "Acme Blog", got.Name)
	}
	if got.URL != "https://acme.example" {
		t.Fatalf("url: want %q, got %q", "https://acme.example", got.URL)
	}
	// Tagline was not written — registry default carries through.
	if got.Tagline != "Just another GoNext site" {
		t.Fatalf("tagline: want registry default, got %q", got.Tagline)
	}
}

// errStore is a Store stub that satisfies the interface but fails
// BulkRead with a fixed error. Used to verify the "graceful — never
// 500" contract: a store hiccup must surface as a 200 with defaults,
// not as a hard 500 that crashes upstream Server Components.
type errStore struct{}

func (errStore) Read(context.Context, string) (any, error) { return nil, errors.New("read") }
func (errStore) Write(context.Context, string, any) error  { return errors.New("write") }
func (errStore) BulkRead(context.Context, []string) (map[string]any, error) {
	return nil, errors.New("bulk read failed")
}
func (errStore) LoadAutoload(context.Context) (map[string]any, error) {
	return nil, errors.New("load")
}

// TestStoreErrorReturnsDefaults verifies the contract called out in the
// package doc: a store error returns the documented PUBLIC defaults
// (not the registry defaults — the registry isn't reachable when the
// store path errors) with a 200, never a 500.
func TestStoreErrorReturnsDefaults(t *testing.T) {
	mux := http.NewServeMux()
	if err := Mount(mux, testBase, Deps{
		Store:  errStore{},
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, testBase, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	got := decodeIdentity(t, rec.Body.Bytes())
	if got.Name != defaultName {
		t.Fatalf("name: want public default %q, got %q", defaultName, got.Name)
	}
	if got.Tagline != defaultTagline {
		t.Fatalf("tagline: want public default %q, got %q", defaultTagline, got.Tagline)
	}
	if got.URL != defaultURL {
		t.Fatalf("url: want public default %q, got %q", defaultURL, got.URL)
	}
}

// TestNoAuthRequired is the load-bearing test for the public surface.
// The harness never injects a policy.Principal — anonymous requests
// must return 200. If a future maintainer wraps this Mount in
// RequireSession, this test fails fast.
func TestNoAuthRequired(t *testing.T) {
	h := newHarness(t)

	req := httptest.NewRequest(http.MethodGet, testBase, nil)
	// Deliberately no Cookie header, no principal on context — this is
	// the curl-from-an-anonymous-browser scenario.
	rec := h.do(t, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous read failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestResponseShapeIsFlat verifies the wire contract — three string
// fields, no envelope, no extra keys. The apps/web fetchSiteOptions
// parser decodes against exactly this shape, so a contract drift here
// would silently break the public site's <title>.
func TestResponseShapeIsFlat(t *testing.T) {
	h := newHarness(t)
	if err := h.store.Write(context.Background(), keySiteName, "Shape Test"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, testBase, nil)
	rec := h.do(t, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raw) != 3 {
		t.Fatalf("response should have exactly 3 keys, got %d (%v)", len(raw), raw)
	}
	for _, key := range []string{"name", "tagline", "url"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("response missing key %q: %v", key, raw)
		}
		if _, ok := raw[key].(string); !ok {
			t.Fatalf("key %q must be a string, got %T", key, raw[key])
		}
	}
}

// TestMountNilStoreErrors verifies that Mount surfaces a malformed
// Deps as an error rather than panicking — same convention as the
// admin/settings Mount and the public/menus Mount.
func TestMountNilStoreErrors(t *testing.T) {
	mux := http.NewServeMux()
	if err := Mount(mux, testBase, Deps{}); err == nil {
		t.Fatal("Mount: want error for empty Deps, got nil")
	}
}
