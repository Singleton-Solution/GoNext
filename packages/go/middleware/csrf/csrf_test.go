package csrf_test

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/middleware/csrf"
)

// testSecret is a 32-byte key used across the test suite. Not derived
// from anything sensitive; it's a hardcoded constant for test reproducibility.
var testSecret = []byte("test-secret-key-1234567890abcdef")

// okHandler returns 200 OK. The middleware's job is to either allow or
// 403 a request before this is reached; tests assert on what the
// outermost middleware does.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
})

// build wraps the okHandler with a CSRF middleware constructed from the
// provided options. Returns the composed handler ready for ServeHTTP.
func build(t *testing.T, opts csrf.Options) http.Handler {
	t.Helper()
	mw := csrf.New(testSecret, opts)
	return mw(okHandler)
}

// freshTokenFromGET runs a GET against the handler and pulls the csrf
// cookie out of the response Set-Cookie. The middleware mints a cookie
// on every safe-method request that lacks one, so this is the canonical
// way for tests to obtain a valid (header, cookie) pair.
func freshTokenFromGET(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, c := range rec.Result().Cookies() {
		if c.Name == "csrf" {
			return c.Value
		}
	}
	t.Fatalf("GET did not set a csrf cookie; headers: %v", rec.Header())
	return ""
}

func TestNew_PanicsOnWeakSecret(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on a too-short secret")
		}
	}()
	_ = csrf.New([]byte("short"), csrf.Options{})
}

func TestNew_PanicsOnNegativeTTL(t *testing.T) {
	// Negative TTL would produce a Set-Cookie with Max-Age=0, which
	// browsers interpret as "delete this cookie immediately" per
	// RFC 6265. The old behavior silently broke real-browser flows;
	// we now panic at construction time so the misconfig is caught
	// at boot rather than at runtime.
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on a negative TTL")
		}
	}()
	_ = csrf.New(testSecret, csrf.Options{TTL: -1 * time.Second})
}

func TestNew_ZeroOptionsAreValid(t *testing.T) {
	// The zero value of Options should produce a usable middleware.
	h := build(t, csrf.Options{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("zero-options GET: status %d, want 200", rec.Code)
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Error("zero-options GET should mint a csrf cookie")
	}
}

func TestSafeMethodsNeverBlocked(t *testing.T) {
	h := build(t, csrf.Options{})
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(m, func(t *testing.T) {
			rec := httptest.NewRecorder()
			// No cookie, no header — would 403 on POST.
			h.ServeHTTP(rec, httptest.NewRequest(m, "/somewhere", nil))
			if rec.Code != http.StatusOK {
				t.Errorf("%s: got %d, want 200", m, rec.Code)
			}
		})
	}
}

func TestSafeMethodMintsCookie(t *testing.T) {
	h := build(t, csrf.Options{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	var got *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "csrf" {
			got = c
		}
	}
	if got == nil {
		t.Fatal("GET did not mint a csrf cookie")
	}
	if got.HttpOnly {
		t.Error("csrf cookie must NOT be HttpOnly (JS must read it)")
	}
	if got.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite: got %v, want Lax", got.SameSite)
	}
	if got.Path != "/" {
		t.Errorf("Path: got %q, want /", got.Path)
	}
	if got.Value == "" {
		t.Error("cookie value is empty")
	}
	if !strings.Contains(got.Value, ".") {
		t.Errorf("cookie value missing '.' separators: %q", got.Value)
	}
	// MaxAge MUST be positive — Max-Age=0 means delete-immediately
	// per RFC 6265, which would silently break the entire middleware.
	if got.MaxAge <= 0 {
		t.Errorf("MaxAge: got %d, want > 0 (RFC 6265: 0/negative means delete)", got.MaxAge)
	}
}

func TestSafeMethodKeepsExistingValidCookie(t *testing.T) {
	// If the request already carries a valid cookie, the middleware
	// must NOT clobber it on a GET — that would invalidate any in-flight
	// state-changing call the client is about to make.
	h := build(t, csrf.Options{})
	tok := freshTokenFromGET(t, h)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "csrf", Value: tok})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	for _, c := range rec.Result().Cookies() {
		if c.Name == "csrf" && c.Value != tok {
			t.Errorf("middleware overwrote valid cookie: got %q, was %q", c.Value, tok)
		}
	}
}

func TestStateChangingRequest_MissingToken_403(t *testing.T) {
	h := build(t, csrf.Options{})
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(m, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(m, "/", nil))
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s missing token: status %d, want 403", m, rec.Code)
			}
		})
	}
}

func TestStateChangingRequest_MismatchedTokenAndCookie_403(t *testing.T) {
	h := build(t, csrf.Options{})
	// Mint two different valid tokens by separate GETs.
	tokA := freshTokenFromGET(t, h)
	tokB := freshTokenFromGET(t, h)
	if tokA == tokB {
		t.Fatal("two GETs returned identical tokens; entropy failure?")
	}

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{Name: "csrf", Value: tokA})
	req.Header.Set("X-CSRF-Token", tokB) // valid token, but not our cookie
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403 on mismatch", rec.Code)
	}
}

func TestStateChangingRequest_ValidHeader_Passes(t *testing.T) {
	h := build(t, csrf.Options{})
	tok := freshTokenFromGET(t, h)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{Name: "csrf", Value: tok})
	req.Header.Set("X-CSRF-Token", tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("valid POST: status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestStateChangingRequest_ValidFormField_Passes(t *testing.T) {
	h := build(t, csrf.Options{})
	tok := freshTokenFromGET(t, h)

	body := url.Values{"csrf_token": {tok}, "foo": {"bar"}}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf", Value: tok})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("form POST: status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestStateChangingRequest_HeaderPreferredOverForm(t *testing.T) {
	// If both header and form are present, the header wins. The form
	// is only consulted when the header is empty.
	h := build(t, csrf.Options{})
	tok := freshTokenFromGET(t, h)

	body := url.Values{"csrf_token": {"forged-form-value"}}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", tok)
	req.AddCookie(&http.Cookie{Name: "csrf", Value: tok})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status %d, want 200 (header preferred); body=%s", rec.Code, rec.Body.String())
	}
}

func TestStateChangingRequest_MissingCookie_403(t *testing.T) {
	// Token in header but no cookie at all — attacker forged the header.
	h := build(t, csrf.Options{})
	tok := freshTokenFromGET(t, h)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	// no AddCookie
	req.Header.Set("X-CSRF-Token", tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403 (no cookie)", rec.Code)
	}
}

func TestExpiredToken_403(t *testing.T) {
	// Use a clock the test can rewind.
	clock := time.Unix(1_700_000_000, 0)
	now := func() time.Time { return clock }
	mintMW := csrf.New(testSecret, csrf.Options{TTL: time.Hour, Now: now})

	// Mint a token "now".
	rec := httptest.NewRecorder()
	mintMW(okHandler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	var tok string
	for _, c := range rec.Result().Cookies() {
		if c.Name == "csrf" {
			tok = c.Value
		}
	}
	if tok == "" {
		t.Fatal("could not mint token")
	}

	// Advance the clock past TTL.
	clock = clock.Add(2 * time.Hour)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{Name: "csrf", Value: tok})
	req.Header.Set("X-CSRF-Token", tok)
	rec2 := httptest.NewRecorder()
	mintMW(okHandler).ServeHTTP(rec2, req)

	if rec2.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403 (expired)", rec2.Code)
	}
}

func TestFutureToken_Rejected(t *testing.T) {
	// A token whose timestamp is significantly in the future (clock-skew
	// attack) must be rejected.
	clock := time.Unix(1_700_000_000, 0)
	mw1 := csrf.New(testSecret, csrf.Options{TTL: time.Hour, Now: func() time.Time { return clock }})
	rec := httptest.NewRecorder()
	mw1(okHandler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	var tok string
	for _, c := range rec.Result().Cookies() {
		if c.Name == "csrf" {
			tok = c.Value
		}
	}
	if tok == "" {
		t.Fatal("could not mint token")
	}

	// Now the server's clock is 10 minutes BEHIND when the token claims
	// to have been minted.
	mw2 := csrf.New(testSecret, csrf.Options{TTL: time.Hour, Now: func() time.Time { return clock.Add(-10 * time.Minute) }})
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{Name: "csrf", Value: tok})
	req.Header.Set("X-CSRF-Token", tok)
	rec2 := httptest.NewRecorder()
	mw2(okHandler).ServeHTTP(rec2, req)

	if rec2.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403 (future token)", rec2.Code)
	}
}

func TestMalformedToken_403(t *testing.T) {
	// Cookie+header both present but the value is not a valid token.
	h := build(t, csrf.Options{})
	for _, bad := range []string{
		"not-a-token",
		"only.two",
		".empty.parts.",
		"a..c",
		"a.b.",
		strings.Repeat("a", 4096),
	} {
		t.Run(bad, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.AddCookie(&http.Cookie{Name: "csrf", Value: bad})
			req.Header.Set("X-CSRF-Token", bad)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("malformed %q: status %d, want 403", bad, rec.Code)
			}
		})
	}
}

func TestForgedHMAC_403(t *testing.T) {
	// Mint a token under a different secret and try to use it.
	other := []byte("attacker-secret-1234567890abcdef")
	atkMW := csrf.New(other, csrf.Options{})
	rec := httptest.NewRecorder()
	atkMW(okHandler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	var atkTok string
	for _, c := range rec.Result().Cookies() {
		if c.Name == "csrf" {
			atkTok = c.Value
		}
	}

	srvMW := csrf.New(testSecret, csrf.Options{})
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{Name: "csrf", Value: atkTok})
	req.Header.Set("X-CSRF-Token", atkTok)
	rec2 := httptest.NewRecorder()
	srvMW(okHandler).ServeHTTP(rec2, req)

	if rec2.Code != http.StatusForbidden {
		t.Errorf("foreign-HMAC token: status %d, want 403", rec2.Code)
	}
}

func TestSkipPaths_BypassValidation(t *testing.T) {
	h := build(t, csrf.Options{
		SkipPaths: []string{"/auth/login", "/webhooks/", "/auth/oidc/callback"},
	})

	for _, p := range []string{"/auth/login", "/webhooks/stripe", "/webhooks/github", "/auth/oidc/callback"} {
		t.Run(p, func(t *testing.T) {
			// POST without any cookie/header.
			req := httptest.NewRequest(http.MethodPost, p, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("skipped POST %q: status %d, want 200", p, rec.Code)
			}
		})
	}
}

func TestSkipPath_StillMintsCookie(t *testing.T) {
	// A GET to a skipped path should still leave the client with a
	// cookie so the next non-skipped state-changing call has one.
	h := build(t, csrf.Options{SkipPaths: []string{"/auth/login"}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "csrf" && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Error("skipped GET should still mint a csrf cookie")
	}
}

// TestSkipPaths_PathTraversalBypass exercises the bug the PR #290
// reviewer reported: a request like POST /webhooks/../admin/users
// matches the /webhooks/ skip prefix under naive HasPrefix, but the
// downstream router (http.ServeMux, chi, gorilla/mux) canonicalizes
// the path to /admin/users — bypassing CSRF on a sensitive endpoint.
//
// The middleware now rejects non-canonical paths with 400 BEFORE any
// SkipPaths check.
func TestSkipPaths_PathTraversalBypass(t *testing.T) {
	h := build(t, csrf.Options{
		SkipPaths: []string{"/webhooks/", "/auth/login"},
	})

	traversals := []string{
		"/webhooks/../admin/users",
		"/webhooks/../../admin",
		"/webhooks/./../admin",
		"/auth/login/../admin",
		"/auth/login/../../etc/passwd",
		"/webhooks//../admin",       // double-slash then traversal
		"/webhooks/sub/../../admin", // nested traversal
	}
	for _, p := range traversals {
		t.Run(p, func(t *testing.T) {
			// Use httptest.NewRequest's url.URL directly so that the
			// raw, non-canonical path survives all the way to the
			// middleware. (NewRequest uses url.Parse, which preserves
			// the path as-is.)
			req := httptest.NewRequest(http.MethodPost, p, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("traversal POST %q: status %d, want 400 (non-canonical path)", p, rec.Code)
			}
		})
	}
}

// TestSafeMethod_PathTraversal_Rejected verifies the canonical-path
// guard fires for GET too (it sits before the method check). An
// attacker probing the cookie-mint endpoint via traversal should not
// be able to mint a cookie either.
func TestSafeMethod_PathTraversal_Rejected(t *testing.T) {
	h := build(t, csrf.Options{SkipPaths: []string{"/webhooks/"}})
	req := httptest.NewRequest(http.MethodGet, "/webhooks/../admin", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("traversal GET: status %d, want 400", rec.Code)
	}
}

// TestCanonicalPathStillAllowed ensures the guard doesn't false-positive
// on perfectly valid paths the application uses every day.
func TestCanonicalPathStillAllowed(t *testing.T) {
	h := build(t, csrf.Options{SkipPaths: []string{"/webhooks/"}})
	canonical := []string{
		"/",
		"/admin",
		"/admin/users",
		"/webhooks/stripe",
		"/api/v1/posts",
	}
	for _, p := range canonical {
		t.Run(p, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("canonical GET %q: status %d, want 200", p, rec.Code)
			}
		})
	}
}

func TestCustomNames(t *testing.T) {
	h := build(t, csrf.Options{
		CookieName: "x_my_csrf",
		HeaderName: "X-My-Csrf",
		FormField:  "my_csrf_field",
	})
	// Mint a token via GET.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	var tok string
	for _, c := range rec.Result().Cookies() {
		if c.Name == "x_my_csrf" {
			tok = c.Value
		}
	}
	if tok == "" {
		t.Fatal("custom cookie name not set")
	}

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{Name: "x_my_csrf", Value: tok})
	req.Header.Set("X-My-Csrf", tok)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Errorf("custom names valid call: status %d, want 200", rec2.Code)
	}
}

func TestToken_Helper(t *testing.T) {
	// Token(r) returns the cookie value, or "" if absent.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := csrf.Token(r); got != "" {
		t.Errorf("Token on empty request: got %q, want empty", got)
	}

	r.AddCookie(&http.Cookie{Name: "csrf", Value: "hello-world"})
	if got := csrf.Token(r); got != "hello-world" {
		t.Errorf("Token: got %q, want %q", got, "hello-world")
	}
}

func TestTokenFromCookie_CustomName(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "x_my_csrf", Value: "abc"})
	if got := csrf.TokenFromCookie(r, "x_my_csrf"); got != "abc" {
		t.Errorf("TokenFromCookie: got %q, want abc", got)
	}
	if got := csrf.TokenFromCookie(r, ""); got != "" {
		t.Errorf("TokenFromCookie with empty name falls back to default; got %q, want empty", got)
	}
}

func TestSPAFlow_EndToEnd(t *testing.T) {
	// Simulate an SPA: GET /, read cookie, send POST with X-CSRF-Token
	// header constructed from the cookie value. This is the canonical
	// pattern described in docs/06 §9.
	h := build(t, csrf.Options{})

	// Step 1: SPA loads, browser sends GET, server sets cookie.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/dashboard: status %d", rec.Code)
	}

	var cookieVal string
	for _, c := range rec.Result().Cookies() {
		if c.Name == "csrf" {
			cookieVal = c.Value
		}
	}
	if cookieVal == "" {
		t.Fatal("server did not set csrf cookie on dashboard GET")
	}

	// Step 2: SPA reads cookie via document.cookie (simulated below),
	// constructs the next fetch with X-CSRF-Token header.
	req := httptest.NewRequest(http.MethodPost, "/admin/posts", strings.NewReader(`{"title":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "csrf", Value: cookieVal})
	req.Header.Set("X-CSRF-Token", cookieVal)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("SPA POST: status %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}

	// Step 3: an attacker page sends the same POST but forgets the
	// header (browsers send the cookie automatically; the header is
	// what the attacker can't forge from a cross-origin page).
	req2 := httptest.NewRequest(http.MethodPost, "/admin/posts", strings.NewReader(`{"title":"evil"}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.AddCookie(&http.Cookie{Name: "csrf", Value: cookieVal})
	// no X-CSRF-Token header
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req2)
	if rec3.Code != http.StatusForbidden {
		t.Errorf("attacker POST with no header: status %d, want 403", rec3.Code)
	}
}

// TestRealBrowserCookieRoundTrip uses a live httptest.Server plus a real
// http.Client with a cookie jar — not direct req.AddCookie injection —
// to verify that the cookie the middleware emits is one a browser will
// actually keep and replay on the next request.
//
// Direct-injection tests (req.AddCookie) bypass RFC-6265 cookie-jar
// semantics, so a Max-Age=0 cookie still appears as "received" in those
// tests even though no real browser would store it. The original PR
// shipped exactly such a test for negative TTL; this case is the
// regression guard.
func TestRealBrowserCookieRoundTrip(t *testing.T) {
	mw := csrf.New(testSecret, csrf.Options{TTL: time.Hour})
	srv := httptest.NewServer(mw(okHandler))
	defer srv.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{Jar: jar}

	// Step 1: GET — server mints cookie, browser stores it.
	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("first GET: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first GET status: %d", resp.StatusCode)
	}

	// Verify the jar actually has the cookie. With Max-Age <= 0
	// the jar would discard the Set-Cookie and this list would be
	// empty.
	u, _ := url.Parse(srv.URL)
	cookies := jar.Cookies(u)
	if len(cookies) == 0 {
		t.Fatal("real cookie jar did not retain the issued cookie (Max-Age<=0?)")
	}
	var tok string
	for _, c := range cookies {
		if c.Name == "csrf" {
			tok = c.Value
		}
	}
	if tok == "" {
		t.Fatal("real cookie jar has no csrf cookie")
	}

	// Step 2: POST with the jar AND the matching header. Should pass.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/admin/posts", strings.NewReader(""))
	req.Header.Set("X-CSRF-Token", tok)
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp2.Body)
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("POST via real browser flow: status %d, want 200", resp2.StatusCode)
	}
}

func TestSecureCookieOnTLS(t *testing.T) {
	h := build(t, csrf.Options{})

	// HTTPS request: Secure must be set.
	srv := httptest.NewTLSServer(h)
	defer srv.Close()
	client := srv.Client()
	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("https GET: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	var got *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "csrf" {
			got = c
		}
	}
	if got == nil {
		t.Fatal("no csrf cookie set on TLS request")
	}
	if !got.Secure {
		t.Error("Secure attribute not set on TLS-issued cookie")
	}
}

// TestSecureCookieOnForwardedProto_OnlyWhenTrusted exercises the
// proxy-header gate: by default the middleware ignores X-Forwarded-Proto
// (otherwise an attacker on plain HTTP could force Secure on the cookie
// and DoS the user). Only when Options.TrustProxyHeaders is true does
// the header sway the Secure attribute.
func TestSecureCookieOnForwardedProto_OnlyWhenTrusted(t *testing.T) {
	t.Run("untrusted (default) ignores header", func(t *testing.T) {
		h := build(t, csrf.Options{})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		h.ServeHTTP(rec, req)

		var got *http.Cookie
		for _, c := range rec.Result().Cookies() {
			if c.Name == "csrf" {
				got = c
			}
		}
		if got == nil {
			t.Fatal("no csrf cookie")
		}
		if got.Secure {
			t.Error("untrusted X-Forwarded-Proto must NOT set Secure (defends against DoS via forced-Secure-on-plain-HTTP)")
		}
	})

	t.Run("trusted honors header", func(t *testing.T) {
		h := build(t, csrf.Options{TrustProxyHeaders: true})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		h.ServeHTTP(rec, req)

		var got *http.Cookie
		for _, c := range rec.Result().Cookies() {
			if c.Name == "csrf" {
				got = c
			}
		}
		if got == nil || !got.Secure {
			t.Errorf("trusted X-Forwarded-Proto=https should set Secure; got %+v", got)
		}
	})

	t.Run("trusted but no header still insecure", func(t *testing.T) {
		// Behind a proxy that didn't terminate TLS, the cookie should
		// still NOT be Secure.
		h := build(t, csrf.Options{TrustProxyHeaders: true})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		var got *http.Cookie
		for _, c := range rec.Result().Cookies() {
			if c.Name == "csrf" {
				got = c
			}
		}
		if got == nil {
			t.Fatal("no csrf cookie")
		}
		if got.Secure {
			t.Error("Secure should NOT be set when no X-Forwarded-Proto present")
		}
	})
}

func TestInsecureCookieOnPlainHTTP(t *testing.T) {
	h := build(t, csrf.Options{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	var got *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "csrf" {
			got = c
		}
	}
	if got == nil {
		t.Fatal("no cookie")
	}
	if got.Secure {
		t.Error("Secure should NOT be set on plain HTTP (dev would break otherwise)")
	}
}

// TestFormBody_RestoredForDownstreamHandler verifies the middleware no
// longer drains r.Body for a form-encoded request. The reviewer flagged
// this as a foot-gun: handlers downstream that call json.NewDecoder or
// io.ReadAll(r.Body) used to see EOF.
func TestFormBody_RestoredForDownstreamHandler(t *testing.T) {
	var downstreamSawBody string
	tap := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		downstreamSawBody = string(raw)
		w.WriteHeader(http.StatusOK)
	})

	mw := csrf.New(testSecret, csrf.Options{})
	h := mw(tap)

	// Mint a token to drive a valid POST.
	tok := freshTokenFromGET(t, h)

	formBody := url.Values{"csrf_token": {tok}, "title": {"hello"}, "body": {"world"}}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(formBody))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf", Value: tok})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if downstreamSawBody != formBody {
		t.Errorf("downstream handler saw body %q, want full original %q (middleware drained r.Body)", downstreamSawBody, formBody)
	}
}

// TestFormBody_ContentTypeWithCharset verifies that a charset suffix on
// the Content-Type (e.g. "application/x-www-form-urlencoded; charset=UTF-8")
// is correctly stripped before matching, so the form path is taken.
func TestFormBody_ContentTypeWithCharset(t *testing.T) {
	h := build(t, csrf.Options{})
	tok := freshTokenFromGET(t, h)

	body := url.Values{"csrf_token": {tok}}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.AddCookie(&http.Cookie{Name: "csrf", Value: tok})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("form POST with charset param: status %d, want 200", rec.Code)
	}
}

// TestFormBody_NilBody covers the r.Body == nil guard. httptest.NewRequest
// supplies a non-nil body even for nil input, so we use http.NewRequest
// directly which leaves Body nil.
func TestFormBody_NilBody(t *testing.T) {
	h := build(t, csrf.Options{})
	tok := freshTokenFromGET(t, h)

	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf", Value: tok})
	// No X-CSRF-Token header — middleware falls through to tokenFromForm
	// which hits the nil-body branch and returns "". Result: 403 (no
	// token).
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("nil body + form CT: status %d, want 403", rec.Code)
	}
}

// TestFormBody_MalformedFormBody triggers url.ParseQuery's error path.
// url.ParseQuery only errors on invalid percent-encoding, so we feed
// "%ZZ" which is not valid hex.
func TestFormBody_MalformedFormBody(t *testing.T) {
	h := build(t, csrf.Options{})
	tok := freshTokenFromGET(t, h)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("csrf_token=%ZZ"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf", Value: tok})
	// No header — falls through to form. ParseQuery returns an error;
	// we return "" → 403.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("malformed form body: status %d, want 403", rec.Code)
	}
}

// TestFormBody_Multipart exercises the multipart/form-data branch.
func TestFormBody_Multipart(t *testing.T) {
	h := build(t, csrf.Options{})
	tok := freshTokenFromGET(t, h)

	// Hand-roll a minimal multipart body. The boundary is fixed for
	// reproducibility; real-world clients use random boundaries.
	const boundary = "----test-csrf-boundary"
	var body strings.Builder
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"csrf_token\"\r\n\r\n")
	body.WriteString(tok + "\r\n")
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"title\"\r\n\r\n")
	body.WriteString("hello\r\n")
	body.WriteString("--" + boundary + "--\r\n")

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body.String()))
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.AddCookie(&http.Cookie{Name: "csrf", Value: tok})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("multipart form POST: status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// TestFormBody_MultipartTooLarge_403 exercises the ParseMultipartForm
// error branch when MaxBytesReader trips.
func TestFormBody_MultipartTooLarge_403(t *testing.T) {
	// 1 KiB cap is tiny on purpose — even a header-only multipart will
	// exceed it once the boundary, disposition header, and token are in.
	h := build(t, csrf.Options{MaxFormBodyBytes: 64})
	tok := freshTokenFromGET(t, h)

	const boundary = "----test-csrf-boundary"
	var body strings.Builder
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"csrf_token\"\r\n\r\n")
	body.WriteString(tok + "\r\n")
	body.WriteString("--" + boundary + "--\r\n")

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body.String()))
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.AddCookie(&http.Cookie{Name: "csrf", Value: tok})
	// no X-CSRF-Token header → falls through to form path → MaxBytesReader
	// trips → ParseMultipartForm errors → tokenFromForm returns "".
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("oversize multipart: status %d, want 403", rec.Code)
	}
}

// TestMaxFormBodyBytes_Configurable verifies the size cap is configurable
// and the default fires when bodies exceed it.
func TestMaxFormBodyBytes_Configurable(t *testing.T) {
	// Default (64 KiB): a 128 KiB body must be rejected with 403 (token
	// extraction fails because the body is too big to scan).
	t.Run("default rejects oversize body", func(t *testing.T) {
		h := build(t, csrf.Options{})
		tok := freshTokenFromGET(t, h)
		// Build a > 64 KiB body. csrf_token first so the token would
		// be findable if we read the full body.
		filler := strings.Repeat("x", 128*1024)
		formBody := url.Values{"csrf_token": {tok}, "blob": {filler}}.Encode()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(formBody))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "csrf", Value: tok})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("oversize form body with default cap: status %d, want 403", rec.Code)
		}
	})

	// Configured higher (256 KiB): the same request now succeeds.
	t.Run("configured higher accepts large body", func(t *testing.T) {
		h := build(t, csrf.Options{MaxFormBodyBytes: 512 * 1024})
		tok := freshTokenFromGET(t, h)
		filler := strings.Repeat("x", 128*1024)
		formBody := url.Values{"csrf_token": {tok}, "blob": {filler}}.Encode()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(formBody))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "csrf", Value: tok})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("oversize form body with raised cap: status %d, want 200", rec.Code)
		}
	})

	// Negative cap disables the limit (documented foot-gun).
	t.Run("negative disables the cap", func(t *testing.T) {
		h := build(t, csrf.Options{MaxFormBodyBytes: -1})
		tok := freshTokenFromGET(t, h)
		filler := strings.Repeat("x", 128*1024)
		formBody := url.Values{"csrf_token": {tok}, "blob": {filler}}.Encode()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(formBody))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "csrf", Value: tok})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("negative cap should disable the limit: status %d, want 200", rec.Code)
		}
	})
}
