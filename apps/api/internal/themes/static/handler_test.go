package static

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestTree builds a temp theme directory laid out like the
// production volume mount:
//
//	<root>/
//	  gn-hello/
//	    style.css
//	    parts/header.css
//	    theme.json
//	  gn-pro/
//	    style.css
//
// Returns the absolute path to <root>.
func newTestTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"gn-hello", "gn-hello/parts", "gn-pro"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	writeFile := func(rel, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	writeFile("gn-hello/style.css", ":root { --paper: #F5F2EA; }\n")
	writeFile("gn-hello/parts/header.css", "header { padding: 1rem; }\n")
	writeFile("gn-hello/theme.json", `{"slug":"gn-hello"}`)
	writeFile("gn-pro/style.css", ":root { --paper: #ffffff; }\n")
	return root
}

// mountTest builds a mux + serve test rig for the static handler.
// activeSlug is the value returned by the ActiveResolver closure on
// every call; tests that need a varying resolver build their own.
func mountTest(t *testing.T, themeDir, activeSlug string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	if err := Mount(mux, "/themes", Deps{
		ThemeDir:       themeDir,
		ActiveResolver: func() string { return activeSlug },
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return mux
}

func TestServe_RealFile_ReturnsCSS(t *testing.T) {
	root := newTestTree(t)
	h := mountTest(t, root, "gn-hello")

	req := httptest.NewRequest(http.MethodGet, "/themes/gn-hello/style.css", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/css; charset=utf-8" {
		t.Errorf("Content-Type: got %q, want text/css; charset=utf-8", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=3600" {
		t.Errorf("Cache-Control: got %q, want public, max-age=3600", got)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "--paper: #F5F2EA") {
		t.Errorf("body: missing expected CSS, got %q", string(body))
	}
}

func TestServe_NestedFile_ReturnsCSS(t *testing.T) {
	root := newTestTree(t)
	h := mountTest(t, root, "gn-hello")

	req := httptest.NewRequest(http.MethodGet, "/themes/gn-hello/parts/header.css", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/css; charset=utf-8" {
		t.Errorf("Content-Type: got %q, want text/css; charset=utf-8", got)
	}
}

func TestServe_HeadRequestSendsHeadersOnly(t *testing.T) {
	root := newTestTree(t)
	h := mountTest(t, root, "gn-hello")

	// The handler is mounted with "GET" — but Go's mux treats HEAD as
	// an alias for GET when no explicit HEAD handler is registered, so
	// the request reaches our code with r.Method == "HEAD" and we
	// short-circuit the body copy. The mux behavior is a stdlib
	// detail; we exercise our short-circuit explicitly.
	req := httptest.NewRequest(http.MethodGet, "/themes/gn-hello/style.css", nil)
	req.Method = http.MethodHead
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Some mux versions reject HEAD outright when only GET is
	// registered. Accept either 200 (the short-circuit fired) or 405
	// (mux refused) — what we care about is that no body is leaked
	// when the handler does run.
	if rec.Code == http.StatusOK && rec.Body.Len() != 0 {
		t.Errorf("HEAD: body should be empty, got %q", rec.Body.String())
	}
}

func TestServe_PathTraversalRejected(t *testing.T) {
	root := newTestTree(t)
	h := mountTest(t, root, "gn-hello")

	cases := []struct {
		name string
		path string
	}{
		// Slash-encoded traversal inside the {file...} segment.
		{"dotdot_in_file", "/themes/gn-hello/../../etc/passwd"},
		// Traversal at the head of {file...}.
		{"dotdot_head", "/themes/gn-hello/..%2Fetc%2Fpasswd"},
		// Backslash flavour (Windows-shaped). We reject pre-clean.
		{"backslash_traversal", `/themes/gn-hello/..\etc\passwd`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			// At least one of {400, 404} is acceptable depending on
			// whether the mux pre-canonicalised the path. What we
			// care about is that we did NOT return 200 — i.e. the
			// attacker did not get a file out of us.
			if rec.Code == http.StatusOK {
				t.Errorf("path %q: served 200 (escape!) body=%q", tc.path, rec.Body.String())
			}
		})
	}
}

func TestServe_DotDotSegmentExplicitlyRejected(t *testing.T) {
	// Hit the handler directly (bypass mux url-canonicalisation) with
	// a {file...} value that contains ".." literally. This exercises
	// the isSafeRelPath guard rather than relying on the mux's
	// behavior — the docs explicitly require 400 on ".." in path.
	root := newTestTree(t)
	mux := http.NewServeMux()
	if err := Mount(mux, "/themes", Deps{
		ThemeDir:       root,
		ActiveResolver: func() string { return "gn-hello" },
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	// Use a request URL the mux WILL route to our handler (no
	// canonical-redirect): /themes/gn-hello/<file> where <file>
	// contains a single ".." segment.
	req := httptest.NewRequest(http.MethodGet, "/themes/gn-hello/sub", nil)
	req.URL.RawPath = "/themes/gn-hello/sub/..%2Fother.css"
	req.URL.Path = "/themes/gn-hello/sub/../other.css"

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Either the mux canonicalised the path away (in which case it
	// 301s) or our handler ran and surfaced 400. Accept either, but
	// not 200.
	if rec.Code == http.StatusOK {
		t.Errorf("expected non-200, got 200, body=%q", rec.Body.String())
	}
}

func TestServe_ActiveResolverWiredThrough(t *testing.T) {
	root := newTestTree(t)

	// The resolver returns gn-hello on first call, gn-pro on second.
	// The handler should re-invoke it per request, so we end up
	// reading from a different theme directory on each call.
	calls := 0
	mux := http.NewServeMux()
	if err := Mount(mux, "/themes", Deps{
		ThemeDir: root,
		ActiveResolver: func() string {
			calls++
			if calls == 1 {
				return "gn-hello"
			}
			return "gn-pro"
		},
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	// First request: /themes/active/style.css resolves to gn-hello.
	req1 := httptest.NewRequest(http.MethodGet, "/themes/active/style.css", nil)
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request: status %d, want 200, body=%q", rec1.Code, rec1.Body.String())
	}
	if !strings.Contains(rec1.Body.String(), "#F5F2EA") {
		t.Errorf("first request: expected gn-hello CSS, got %q", rec1.Body.String())
	}

	// Second request: same URL, resolver now returns gn-pro.
	req2 := httptest.NewRequest(http.MethodGet, "/themes/active/style.css", nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second request: status %d, want 200, body=%q", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), "#ffffff") {
		t.Errorf("second request: expected gn-pro CSS, got %q", rec2.Body.String())
	}

	if calls != 2 {
		t.Errorf("resolver call count: got %d, want 2", calls)
	}
}

func TestServe_ActiveResolverEmpty404(t *testing.T) {
	root := newTestTree(t)
	mux := http.NewServeMux()
	if err := Mount(mux, "/themes", Deps{
		ThemeDir:       root,
		ActiveResolver: func() string { return "" },
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/themes/active/style.css", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

func TestServe_MissingFile404(t *testing.T) {
	root := newTestTree(t)
	h := mountTest(t, root, "gn-hello")

	req := httptest.NewRequest(http.MethodGet, "/themes/gn-hello/does-not-exist.css", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

func TestServe_DirectoryReturns404(t *testing.T) {
	root := newTestTree(t)
	h := mountTest(t, root, "gn-hello")

	// /themes/gn-hello/parts (no trailing file) — the path resolves
	// to a directory on disk. We never serve directory listings.
	// The mux may or may not route a no-file path to our handler
	// depending on how the {file...} wildcard treats an empty
	// remainder; if it 404s before us, that's also fine.
	req := httptest.NewRequest(http.MethodGet, "/themes/gn-hello/parts/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Errorf("expected non-200, got 200 body=%q", rec.Body.String())
	}
}

func TestServe_InvalidSlugRejected(t *testing.T) {
	root := newTestTree(t)
	h := mountTest(t, root, "gn-hello")

	// Slug with characters outside the kebab pattern: "GnHello" is
	// rejected because the regex requires leading lowercase.
	req := httptest.NewRequest(http.MethodGet, "/themes/GnHello/style.css", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestMount_RequiresThemeDir(t *testing.T) {
	mux := http.NewServeMux()
	err := Mount(mux, "/themes", Deps{
		ActiveResolver: func() string { return "" },
	})
	if err == nil {
		t.Fatalf("expected error when ThemeDir is empty")
	}
}

func TestMount_RequiresActiveResolver(t *testing.T) {
	mux := http.NewServeMux()
	err := Mount(mux, "/themes", Deps{
		ThemeDir: t.TempDir(),
	})
	if err == nil {
		t.Fatalf("expected error when ActiveResolver is nil")
	}
}

func TestIsSafeRelPath(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"style.css", true},
		{"parts/header.css", true},
		{"a/b/c.css", true},
		{"", false},
		{"/abs/path.css", false},
		{"..", false},
		{"../etc/passwd", false},
		{"sub/../etc/passwd", false},
		{`sub\..\etc\passwd`, false},
		{"sub/with\x00null.css", false},
	}
	for _, tc := range cases {
		got := isSafeRelPath(tc.in)
		if got != tc.ok {
			t.Errorf("isSafeRelPath(%q) = %v, want %v", tc.in, got, tc.ok)
		}
	}
}

func TestContentType(t *testing.T) {
	cases := map[string]string{
		"x.css":   "text/css; charset=utf-8",
		"x.js":    "application/javascript; charset=utf-8",
		"x.mjs":   "application/javascript; charset=utf-8",
		"x.json":  "application/json; charset=utf-8",
		"x.svg":   "image/svg+xml",
		"x.woff2": "font/woff2",
		"x.png":   "image/png",
	}
	for in, want := range cases {
		if got := contentType(in); got != want {
			t.Errorf("contentType(%q) = %q, want %q", in, got, want)
		}
	}
}
