package customizer

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/theme"
)

// testBase is the route prefix used in every test. Matches the prefix
// the production wiring in main.go uses.
const testBase = "/api/v1/admin/customizer"

// baseTheme returns a small but realistic ThemeJSON value the tests
// share. Mirrors the gn-hello manifest closely enough that override
// validation reflects production behavior.
func baseTheme() *theme.ThemeJSON {
	return &theme.ThemeJSON{
		Version: theme.CurrentVersion,
		Title:   "gn-hello",
		Settings: theme.Settings{
			Color: theme.ColorSettings{
				Palette: []theme.ColorEntry{
					{Slug: "ink", Name: "Ink", Color: "#0f172a"},
					{Slug: "paper", Name: "Paper", Color: "#ffffff"},
					{Slug: "accent", Name: "Accent", Color: "#2563eb"},
				},
			},
			Typography: theme.TypographySet{
				FontFamilies: []theme.FontFamily{
					{Slug: "sans", Name: "Sans", FontFamily: "system-ui"},
				},
				FontSizes: []theme.FontSize{
					{Slug: "md", Name: "Medium", Size: "1rem"},
				},
			},
			Layout: theme.LayoutSettings{
				ContentSize: "720px",
				WideSize:    "1180px",
			},
		},
	}
}

// loaderReturning produces a ThemeLoader that yields t for the given
// slug and errs otherwise. Mirrors the active-theme contract: the loader
// is keyed by slug, not by request.
func loaderReturning(slug string, t *theme.ThemeJSON) ThemeLoader {
	return func(_ context.Context, requested string) (*theme.ThemeJSON, error) {
		if requested != slug {
			return nil, &loaderError{want: slug, got: requested}
		}
		return t, nil
	}
}

type loaderError struct{ want, got string }

func (e *loaderError) Error() string {
	return "loader: wanted slug " + e.want + " got " + e.got
}

// newRouter returns an *http.ServeMux with the customizer mounted on
// testBase, using the given store and loader. The router is wrapped in
// an inline test middleware that stashes a Principal with the
// theme.customize capability on the context — production wiring's
// auth middleware does this.
func newRouter(t *testing.T, store Store, loader ThemeLoader, principal *policy.Principal) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	if err := Mount(mux, testBase, Deps{
		Store:  store,
		Loader: loader,
		Policy: policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if principal != nil {
			r = r.WithContext(policy.WithPrincipal(r.Context(), *principal))
		}
		mux.ServeHTTP(w, r)
	})
}

// adminPrincipal returns a Principal whose roles grant theme.customize.
func adminPrincipal() *policy.Principal {
	return &policy.Principal{UserID: "user:admin", Roles: []policy.Role{policy.RoleAdmin}}
}

// subscriberPrincipal returns a Principal whose roles do NOT grant
// theme.customize. Used by the 403 test.
func subscriberPrincipal() *policy.Principal {
	return &policy.Principal{UserID: "user:joe", Roles: []policy.Role{policy.RoleSubscriber}}
}

// TestMount_Validation covers the boot-time errors callers see when
// Deps is malformed. Each case names the missing field.
func TestMount_Validation(t *testing.T) {
	loader := func(context.Context, string) (*theme.ThemeJSON, error) { return baseTheme(), nil }
	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	store := NewMemoryStore("gn-hello")

	cases := map[string]Deps{
		"missing_store":  {Loader: loader, Policy: pol},
		"missing_loader": {Store: store, Policy: pol},
		"missing_policy": {Store: store, Loader: loader},
	}
	for name, d := range cases {
		t.Run(name, func(t *testing.T) {
			mux := http.NewServeMux()
			if err := Mount(mux, testBase, d); err == nil {
				t.Fatalf("expected Mount to fail; got nil error")
			}
		})
	}

	t.Run("ok", func(t *testing.T) {
		mux := http.NewServeMux()
		if err := Mount(mux, testBase, Deps{Store: store, Loader: loader, Policy: pol}); err != nil {
			t.Fatalf("Mount(ok) returned %v", err)
		}
	})
}

// TestGetActive_HappyPath verifies the response shape: theme + slug +
// any current overrides. Overrides default to "{}" when no row exists.
func TestGetActive_HappyPath(t *testing.T) {
	store := NewMemoryStore("gn-hello")
	router := newRouter(t, store, loaderReturning("gn-hello", baseTheme()), adminPrincipal())

	req := httptest.NewRequest(http.MethodGet, testBase+"/active", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", w.Code, w.Body.String())
	}
	var got ActiveResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ThemeSlug != "gn-hello" {
		t.Fatalf("ThemeSlug = %q; want gn-hello", got.ThemeSlug)
	}
	if got.Theme == nil || got.Theme.Title != "gn-hello" {
		t.Fatalf("Theme = %+v; want gn-hello manifest", got.Theme)
	}
	if string(got.Overrides) != "{}" {
		t.Fatalf("Overrides = %s; want {}", string(got.Overrides))
	}
}

// TestGetActive_IncludesStoredOverrides verifies the GET reflects what
// PUT persisted.
func TestGetActive_IncludesStoredOverrides(t *testing.T) {
	store := NewMemoryStore("gn-hello")
	override := json.RawMessage(`{"settings":{"layout":{"contentSize":"800px"}}}`)
	if err := store.WriteOverrides(context.Background(), "gn-hello", override); err != nil {
		t.Fatalf("seed overrides: %v", err)
	}

	router := newRouter(t, store, loaderReturning("gn-hello", baseTheme()), adminPrincipal())
	req := httptest.NewRequest(http.MethodGet, testBase+"/active", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got ActiveResponse
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if !bytes.Equal(got.Overrides, override) {
		t.Fatalf("Overrides = %s; want %s", string(got.Overrides), string(override))
	}
}

// TestGetActive_Unauthenticated guards the gate's 401 path. No
// principal on the context means the auth middleware hasn't run.
func TestGetActive_Unauthenticated(t *testing.T) {
	store := NewMemoryStore("gn-hello")
	router := newRouter(t, store, loaderReturning("gn-hello", baseTheme()), nil)

	req := httptest.NewRequest(http.MethodGet, testBase+"/active", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", w.Code)
	}
}

// TestGetActive_Forbidden guards the gate's 403 path — a principal
// without theme.customize gets refused.
func TestGetActive_Forbidden(t *testing.T) {
	store := NewMemoryStore("gn-hello")
	router := newRouter(t, store, loaderReturning("gn-hello", baseTheme()), subscriberPrincipal())

	req := httptest.NewRequest(http.MethodGet, testBase+"/active", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d (body %s); want 403", w.Code, w.Body.String())
	}
}

// TestGetActive_NoActiveTheme covers the fresh-deploy-pre-seed case.
func TestGetActive_NoActiveTheme(t *testing.T) {
	store := NewMemoryStore("") // empty slug -> ErrNoActiveTheme
	router := newRouter(t, store, loaderReturning("gn-hello", baseTheme()), adminPrincipal())
	req := httptest.NewRequest(http.MethodGet, testBase+"/active", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", w.Code)
	}
}

// TestPutActive_SavesValidOverride is the happy-path write. The
// response carries the override back so the UI can refresh without a
// follow-up GET.
func TestPutActive_SavesValidOverride(t *testing.T) {
	store := NewMemoryStore("gn-hello")
	router := newRouter(t, store, loaderReturning("gn-hello", baseTheme()), adminPrincipal())

	body := `{"settings":{"color":{"palette":[{"slug":"accent","name":"Accent","color":"#ff0066"}]}}}`
	req := httptest.NewRequest(http.MethodPut, testBase+"/active", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", w.Code, w.Body.String())
	}

	// Verify persistence.
	stored, err := store.ReadOverrides(context.Background(), "gn-hello")
	if err != nil {
		t.Fatalf("ReadOverrides: %v", err)
	}
	if !bytes.Equal(stored, json.RawMessage(body)) {
		t.Fatalf("stored = %s; want %s", string(stored), body)
	}
}

// TestPutActive_RejectsInvalidColor covers the validation path: an
// override with a bad CSS color must return 400 with the JSON pointer
// to the offending field.
func TestPutActive_RejectsInvalidColor(t *testing.T) {
	store := NewMemoryStore("gn-hello")
	router := newRouter(t, store, loaderReturning("gn-hello", baseTheme()), adminPrincipal())

	body := `{"settings":{"color":{"palette":[{"slug":"accent","name":"Accent","color":"not-a-color"}]}}}`
	req := httptest.NewRequest(http.MethodPut, testBase+"/active", strings.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d (body %s); want 400", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "/settings/color/palette/0/color") {
		t.Fatalf("body missing offending path; got %s", w.Body.String())
	}
	// Store must be untouched on failure.
	stored, _ := store.ReadOverrides(context.Background(), "gn-hello")
	if len(stored) != 0 {
		t.Fatalf("store written despite validation failure: %s", string(stored))
	}
}

// TestPutActive_RejectsUnknownPath covers the "made up field" case —
// an override with a key not present in theme.ThemeJSON is rejected.
func TestPutActive_RejectsUnknownPath(t *testing.T) {
	store := NewMemoryStore("gn-hello")
	router := newRouter(t, store, loaderReturning("gn-hello", baseTheme()), adminPrincipal())

	body := `{"settings":{"frobnicate":{"value":"yes"}}}`
	req := httptest.NewRequest(http.MethodPut, testBase+"/active", strings.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d (body %s); want 400", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid_path") {
		t.Fatalf("body missing invalid_path code; got %s", w.Body.String())
	}
}

// TestPutActive_EmptyBody catches the "use DELETE instead" path so the
// UI doesn't accidentally upsert a no-op row.
func TestPutActive_EmptyBody(t *testing.T) {
	store := NewMemoryStore("gn-hello")
	router := newRouter(t, store, loaderReturning("gn-hello", baseTheme()), adminPrincipal())

	req := httptest.NewRequest(http.MethodPut, testBase+"/active", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "empty_override") {
		t.Fatalf("body missing empty_override code; got %s", w.Body.String())
	}
}

// TestPutActive_Forbidden ensures non-admin writes are refused.
func TestPutActive_Forbidden(t *testing.T) {
	store := NewMemoryStore("gn-hello")
	router := newRouter(t, store, loaderReturning("gn-hello", baseTheme()), subscriberPrincipal())

	body := `{"settings":{"layout":{"contentSize":"800px"}}}`
	req := httptest.NewRequest(http.MethodPut, testBase+"/active", strings.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d (body %s); want 403", w.Code, w.Body.String())
	}
	// Store must be untouched.
	stored, _ := store.ReadOverrides(context.Background(), "gn-hello")
	if len(stored) != 0 {
		t.Fatalf("store written despite 403: %s", string(stored))
	}
}

// TestDeleteActive_ClearsStoredOverrides covers the Reset action.
func TestDeleteActive_ClearsStoredOverrides(t *testing.T) {
	store := NewMemoryStore("gn-hello")
	override := json.RawMessage(`{"settings":{"layout":{"contentSize":"800px"}}}`)
	if err := store.WriteOverrides(context.Background(), "gn-hello", override); err != nil {
		t.Fatalf("seed: %v", err)
	}

	router := newRouter(t, store, loaderReturning("gn-hello", baseTheme()), adminPrincipal())
	req := httptest.NewRequest(http.MethodDelete, testBase+"/active", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d (body %s); want 204", w.Code, w.Body.String())
	}
	stored, _ := store.ReadOverrides(context.Background(), "gn-hello")
	if len(stored) != 0 {
		t.Fatalf("overrides not cleared: %s", string(stored))
	}
}

// TestDeleteActive_Idempotent verifies Reset on a fresh install
// returns 204 rather than 404 — the operator should not get an error
// for clearing nothing.
func TestDeleteActive_Idempotent(t *testing.T) {
	store := NewMemoryStore("gn-hello")
	router := newRouter(t, store, loaderReturning("gn-hello", baseTheme()), adminPrincipal())

	req := httptest.NewRequest(http.MethodDelete, testBase+"/active", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d; want 204", w.Code)
	}
}
