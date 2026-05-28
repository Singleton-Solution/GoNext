package siteeditor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/theme"
)

const testBase = "/api/v1/admin/site-editor"

// adminPrincipal returns a Principal whose roles grant manage_themes.
func adminPrincipal() *policy.Principal {
	return &policy.Principal{UserID: "user:admin", Roles: []policy.Role{policy.RoleAdmin}}
}

// subscriberPrincipal returns a Principal whose roles grant nothing the
// site editor cares about. Used by the 403 test.
func subscriberPrincipal() *policy.Principal {
	return &policy.Principal{UserID: "user:joe", Roles: []policy.Role{policy.RoleSubscriber}}
}

// fixtureTheme returns the manifest the tests share. Declares two
// template parts (header + footer) so the editor surface has something
// to enumerate.
func fixtureTheme() *theme.ThemeJSON {
	return &theme.ThemeJSON{
		Version: theme.CurrentVersion,
		Title:   "gn-test",
		TemplateParts: []theme.TemplatePartDef{
			{Name: "header", Title: "Header", Area: "header"},
			{Name: "footer", Title: "Footer", Area: "footer"},
		},
	}
}

// seedThemeDir lays down a temp directory with the structure
// $TMP/{slug}/parts/{header,footer}.html and returns the root path.
// Each part starts with a short marker so the read tests can assert
// what they're getting back.
func seedThemeDir(t *testing.T, slug string) string {
	t.Helper()
	root := t.TempDir()
	partsDir := filepath.Join(root, slug, "parts")
	if err := os.MkdirAll(partsDir, 0o755); err != nil {
		t.Fatalf("mkdir parts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(partsDir, "header.html"),
		[]byte("<header>HEADER FIXTURE</header>"), 0o644); err != nil {
		t.Fatalf("seed header: %v", err)
	}
	if err := os.WriteFile(filepath.Join(partsDir, "footer.html"),
		[]byte("<footer>FOOTER FIXTURE</footer>"), 0o644); err != nil {
		t.Fatalf("seed footer: %v", err)
	}
	return root
}

// newRouter returns an *http.ServeMux wrapped in an inline middleware
// that stashes principal on the context. Mirrors what production's auth
// middleware does for real requests.
func newRouter(t *testing.T, themeDir, slug string, manifest *theme.ThemeJSON, principal *policy.Principal) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	deps := Deps{
		ThemeDir: themeDir,
		Active: func(context.Context) (string, error) {
			if slug == "" {
				return "", ErrNoActiveTheme
			}
			return slug, nil
		},
		Loader: func(_ context.Context, requested string) (*theme.ThemeJSON, error) {
			if requested != slug {
				return nil, ErrNoActiveTheme
			}
			return manifest, nil
		},
		Policy: policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
	}
	if _, err := Mount(mux, testBase, deps); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if principal != nil {
			r = r.WithContext(policy.WithPrincipal(r.Context(), *principal))
		}
		mux.ServeHTTP(w, r)
	})
}

// TestMount_Validation covers the boot-time errors callers see when
// Deps is malformed.
func TestMount_Validation(t *testing.T) {
	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	active := func(context.Context) (string, error) { return "x", nil }
	loader := func(context.Context, string) (*theme.ThemeJSON, error) {
		return fixtureTheme(), nil
	}

	cases := map[string]Deps{
		"missing_dir":    {Active: active, Loader: loader, Policy: pol},
		"missing_active": {ThemeDir: "/tmp", Loader: loader, Policy: pol},
		"missing_loader": {ThemeDir: "/tmp", Active: active, Policy: pol},
		"missing_policy": {ThemeDir: "/tmp", Active: active, Loader: loader},
	}
	for name, d := range cases {
		t.Run(name, func(t *testing.T) {
			mux := http.NewServeMux()
			if _, err := Mount(mux, testBase, d); err == nil {
				t.Fatalf("expected Mount to fail")
			}
		})
	}

	t.Run("ok", func(t *testing.T) {
		mux := http.NewServeMux()
		if _, err := Mount(mux, testBase, Deps{
			ThemeDir: "/tmp", Active: active, Loader: loader, Policy: pol,
		}); err != nil {
			t.Fatalf("Mount(ok): %v", err)
		}
	})
}

// TestListParts_HappyPath verifies the GET /parts response.
func TestListParts_HappyPath(t *testing.T) {
	dir := seedThemeDir(t, "gn-test")
	router := newRouter(t, dir, "gn-test", fixtureTheme(), adminPrincipal())

	req := httptest.NewRequest(http.MethodGet, testBase+"/parts", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s); want 200", w.Code, w.Body.String())
	}

	var got listResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Theme != "gn-test" {
		t.Fatalf("Theme = %q; want gn-test", got.Theme)
	}
	if len(got.Parts) != 2 {
		t.Fatalf("Parts = %d; want 2", len(got.Parts))
	}
	// Sorted alphabetically -> footer first.
	if got.Parts[0].Name != "footer" {
		t.Fatalf("Parts[0].Name = %q; want footer", got.Parts[0].Name)
	}
	if !strings.Contains(got.Parts[0].Content, "FOOTER FIXTURE") {
		t.Fatalf("Parts[0].Content missing fixture: %s", got.Parts[0].Content)
	}
	if got.Parts[0].Area != "footer" {
		t.Fatalf("Parts[0].Area = %q; want footer", got.Parts[0].Area)
	}
	if got.Parts[1].Name != "header" {
		t.Fatalf("Parts[1].Name = %q; want header", got.Parts[1].Name)
	}
	if !strings.Contains(got.Parts[1].Content, "HEADER FIXTURE") {
		t.Fatalf("Parts[1].Content missing fixture: %s", got.Parts[1].Content)
	}
}

// TestListParts_NoActiveTheme covers the fresh-deploy case.
func TestListParts_NoActiveTheme(t *testing.T) {
	dir := t.TempDir()
	router := newRouter(t, dir, "", fixtureTheme(), adminPrincipal())

	req := httptest.NewRequest(http.MethodGet, testBase+"/parts", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", w.Code)
	}
}

// TestListParts_Forbidden ensures non-admin reads are refused.
func TestListParts_Forbidden(t *testing.T) {
	dir := seedThemeDir(t, "gn-test")
	router := newRouter(t, dir, "gn-test", fixtureTheme(), subscriberPrincipal())

	req := httptest.NewRequest(http.MethodGet, testBase+"/parts", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403", w.Code)
	}
}

// TestListParts_Unauthenticated guards the gate's 401 path.
func TestListParts_Unauthenticated(t *testing.T) {
	dir := seedThemeDir(t, "gn-test")
	router := newRouter(t, dir, "gn-test", fixtureTheme(), nil)

	req := httptest.NewRequest(http.MethodGet, testBase+"/parts", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", w.Code)
	}
}

// TestGetPart_HappyPath verifies fetching a single declared part.
func TestGetPart_HappyPath(t *testing.T) {
	dir := seedThemeDir(t, "gn-test")
	router := newRouter(t, dir, "gn-test", fixtureTheme(), adminPrincipal())

	req := httptest.NewRequest(http.MethodGet, testBase+"/parts/header", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	var got Part
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "header" {
		t.Fatalf("Name = %q; want header", got.Name)
	}
	if !strings.Contains(got.Content, "HEADER FIXTURE") {
		t.Fatalf("Content missing fixture: %s", got.Content)
	}
}

// TestGetPart_UndeclaredName returns 404 even if a file with the name
// exists on disk — the theme's declaration is the source of truth.
func TestGetPart_UndeclaredName(t *testing.T) {
	dir := seedThemeDir(t, "gn-test")
	// Drop a rogue file. The handler must not surface it.
	if err := os.WriteFile(filepath.Join(dir, "gn-test", "parts", "secret.html"),
		[]byte("nope"), 0o644); err != nil {
		t.Fatalf("seed rogue: %v", err)
	}
	router := newRouter(t, dir, "gn-test", fixtureTheme(), adminPrincipal())

	req := httptest.NewRequest(http.MethodGet, testBase+"/parts/secret", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d (body %s); want 404", w.Code, w.Body.String())
	}
}

// TestGetPart_PathTraversal returns 400 — the name must be a single
// segment. The browser would already URL-encode '/' but a hostile
// client could omit that, so we reject explicitly.
func TestGetPart_PathTraversal(t *testing.T) {
	dir := seedThemeDir(t, "gn-test")
	router := newRouter(t, dir, "gn-test", fixtureTheme(), adminPrincipal())

	// Use a name with parent-directory tokens AFTER URL decoding to
	// confirm we reject. The router won't route ".." through path
	// values without encoding, but encoded variants land in PathValue.
	cases := []string{
		"..%2Fetc",     // ../etc with encoded slash → name becomes "../etc"
		"%2E%2E%2Fpwd", // ../pwd fully encoded
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, testBase+"/parts/"+raw, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("raw=%q status = %d (body %s); want 400", raw, w.Code, w.Body.String())
			}
		})
	}
}

// TestPutPart_WritesFile verifies the happy-path PUT — content goes to
// disk and a re-read sees it.
func TestPutPart_WritesFile(t *testing.T) {
	dir := seedThemeDir(t, "gn-test")
	router := newRouter(t, dir, "gn-test", fixtureTheme(), adminPrincipal())

	body := `{"content":"<header>EDITED</header>"}`
	req := httptest.NewRequest(http.MethodPut, testBase+"/parts/header",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s); want 200", w.Code, w.Body.String())
	}

	// Disk reflects the write.
	got, err := os.ReadFile(filepath.Join(dir, "gn-test", "parts", "header.html"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "<header>EDITED</header>" {
		t.Fatalf("file = %q; want edited", string(got))
	}

	// And a follow-up GET surfaces the new content.
	req = httptest.NewRequest(http.MethodGet, testBase+"/parts/header", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("re-read status = %d", w.Code)
	}
	var part Part
	_ = json.Unmarshal(w.Body.Bytes(), &part)
	if part.Content != "<header>EDITED</header>" {
		t.Fatalf("re-read content = %q; want edited", part.Content)
	}
}

// TestPutPart_CreatesMissingFile verifies that a declared part without
// a file on disk yet can be written through the editor — the parts
// directory is mkdir-all'd if needed.
func TestPutPart_CreatesMissingFile(t *testing.T) {
	root := t.TempDir()
	// Don't seed any parts; the manifest declares them but disk is empty.
	router := newRouter(t, root, "gn-test", fixtureTheme(), adminPrincipal())

	body := `{"content":"<footer>NEW</footer>"}`
	req := httptest.NewRequest(http.MethodPut, testBase+"/parts/footer",
		strings.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s); want 200", w.Code, w.Body.String())
	}

	got, err := os.ReadFile(filepath.Join(root, "gn-test", "parts", "footer.html"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "<footer>NEW</footer>" {
		t.Fatalf("file = %q", string(got))
	}
}

// TestPutPart_UndeclaredName returns 404 — a write must target a part
// the theme declares. This is the security hinge against a write-only
// graveyard.
func TestPutPart_UndeclaredName(t *testing.T) {
	dir := seedThemeDir(t, "gn-test")
	router := newRouter(t, dir, "gn-test", fixtureTheme(), adminPrincipal())

	body := `{"content":"x"}`
	req := httptest.NewRequest(http.MethodPut, testBase+"/parts/sidebar",
		strings.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d (body %s); want 404", w.Code, w.Body.String())
	}
	// No file should have been created.
	if _, err := os.Stat(filepath.Join(dir, "gn-test", "parts", "sidebar.html")); !os.IsNotExist(err) {
		t.Fatalf("file created despite 404: %v", err)
	}
}

// TestPutPart_PathTraversal rejects ".."-laden names. Even if the
// theme manifest mentioned one (which it shouldn't), the
// validatePartName check would still catch it.
func TestPutPart_PathTraversal(t *testing.T) {
	dir := seedThemeDir(t, "gn-test")
	router := newRouter(t, dir, "gn-test", fixtureTheme(), adminPrincipal())

	body := `{"content":"x"}`
	req := httptest.NewRequest(http.MethodPut, testBase+"/parts/..%2Fevil",
		strings.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", w.Code)
	}
}

// TestPutPart_InvalidJSON rejects malformed bodies with 400.
func TestPutPart_InvalidJSON(t *testing.T) {
	dir := seedThemeDir(t, "gn-test")
	router := newRouter(t, dir, "gn-test", fixtureTheme(), adminPrincipal())

	req := httptest.NewRequest(http.MethodPut, testBase+"/parts/header",
		strings.NewReader(`not json`))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", w.Code)
	}
}

// TestPutPart_Forbidden ensures non-admin writes are refused.
func TestPutPart_Forbidden(t *testing.T) {
	dir := seedThemeDir(t, "gn-test")
	router := newRouter(t, dir, "gn-test", fixtureTheme(), subscriberPrincipal())

	body := `{"content":"x"}`
	req := httptest.NewRequest(http.MethodPut, testBase+"/parts/header",
		strings.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403", w.Code)
	}
}

// TestValidatePartName_Rejects covers the edge inputs the path-traversal
// test relies on — verifies the standalone validator behaves the way the
// handler tests assume.
func TestValidatePartName_Rejects(t *testing.T) {
	bad := []string{
		"",
		"..",
		".",
		"../etc",
		"foo/bar",
		"foo\\bar",
		"FOO",   // case
		" foo ", // whitespace
		"foo.bar",
		"foo..bar",
	}
	for _, name := range bad {
		t.Run(name, func(t *testing.T) {
			if err := validatePartName(name); err == nil {
				t.Fatalf("expected error for %q", name)
			}
		})
	}
	good := []string{"header", "footer", "post-meta", "sidebar_left", "abc123"}
	for _, name := range good {
		t.Run(name, func(t *testing.T) {
			if err := validatePartName(name); err != nil {
				t.Fatalf("unexpected error for %q: %v", name, err)
			}
		})
	}
}

// TestCanonicalArea folds the various theme.json area shapes into the
// three buckets the admin UI knows.
func TestCanonicalArea(t *testing.T) {
	cases := map[string]string{
		"header":        "header",
		"HEADER":        "header",
		"footer":        "footer",
		"uncategorized": "general",
		"":              "general",
		"sidebar":       "general",
		"  ":            "general",
	}
	for in, want := range cases {
		if got := canonicalArea(in); got != want {
			t.Errorf("canonicalArea(%q) = %q; want %q", in, got, want)
		}
	}
}
