package csrf_test

import (
	"io"
	"net/http"
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

func TestNegativeTTL_DisablesFreshnessCheck(t *testing.T) {
	// TTL < 0 means HMAC-only — useful for tests, not recommended
	// for production. Verify the contract.
	clock := time.Unix(1_700_000_000, 0)
	now := func() time.Time { return clock }
	mw := csrf.New(testSecret, csrf.Options{TTL: -1, Now: now})
	rec := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	var tok string
	for _, c := range rec.Result().Cookies() {
		if c.Name == "csrf" {
			tok = c.Value
		}
	}
	if tok == "" {
		t.Fatal("could not mint token")
	}

	// 100 years later — still accepted because TTL is disabled.
	mw2 := csrf.New(testSecret, csrf.Options{TTL: -1, Now: func() time.Time { return clock.Add(100 * 365 * 24 * time.Hour) }})
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{Name: "csrf", Value: tok})
	req.Header.Set("X-CSRF-Token", tok)
	rec2 := httptest.NewRecorder()
	mw2(okHandler).ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Errorf("status %d, want 200 (TTL disabled)", rec2.Code)
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

func TestSecureCookieOnForwardedProto(t *testing.T) {
	// Behind a TLS-terminating proxy, the request arrives over plain HTTP
	// but X-Forwarded-Proto: https. Cookie should still be Secure.
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
	if got == nil || !got.Secure {
		t.Errorf("X-Forwarded-Proto=https should produce Secure cookie; got %+v", got)
	}
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
