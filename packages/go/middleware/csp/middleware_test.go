package csp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/middleware/security"
)

// runMiddleware wraps p+opts around a tiny OK handler and returns the
// recorded response after a single GET / request. Tests using this
// helper focus on the header content rather than the chain wiring.
func runMiddleware(t *testing.T, p *Policy, opts Options, withNonce bool) *httptest.ResponseRecorder {
	t.Helper()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	var handler http.Handler = Middleware(p, opts)(inner)
	if withNonce {
		handler = security.WithNonce()(handler)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec
}

// TestMiddleware_EmitsHeader verifies the no-nonce path: with no
// WithNonce in front, the middleware still emits the policy as-is.
func TestMiddleware_EmitsHeader(t *testing.T) {
	p := PublicSitePolicy(PolicyOptions{ReportURI: "/_/csp-report"})
	rec := runMiddleware(t, p, Options{}, false)
	got := rec.Header().Get("Content-Security-Policy")
	if got == "" {
		t.Fatalf("CSP header missing")
	}
	if !strings.Contains(got, "default-src 'self'") {
		t.Errorf("policy not emitted: %s", got)
	}
	// No nonce in chain ⇒ no 'nonce-…' in the header.
	if strings.Contains(got, "'nonce-") {
		t.Errorf("unexpected nonce in no-nonce path: %s", got)
	}
}

// TestMiddleware_ReportOnlyHeaderName verifies the Options.ReportOnly
// switch flips the header name. The body of the header is unchanged.
func TestMiddleware_ReportOnlyHeaderName(t *testing.T) {
	p := PublicSitePolicy(PolicyOptions{})
	rec := runMiddleware(t, p, Options{ReportOnly: true}, false)
	if got := rec.Header().Get("Content-Security-Policy-Report-Only"); got == "" {
		t.Fatalf("Report-Only header missing")
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != "" {
		t.Errorf("enforcement header should be empty in report-only mode, got %q", got)
	}
}

// TestMiddleware_HeaderOverride exercises the rare-but-supported
// Options.Header override.
func TestMiddleware_HeaderOverride(t *testing.T) {
	p := PublicSitePolicy(PolicyOptions{})
	rec := runMiddleware(t, p, Options{Header: "X-Content-Security-Policy"}, false)
	if got := rec.Header().Get("X-Content-Security-Policy"); got == "" {
		t.Fatalf("override header missing")
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != "" {
		t.Errorf("default header should not be set with override, got %q", got)
	}
}

// TestMiddleware_FoldsNonceFromContext is the integration test that
// verifies the canonical chain: security.WithNonce() before
// csp.Middleware. The emitted CSP must contain 'nonce-…' matching the
// X-Script-Nonce response header.
func TestMiddleware_FoldsNonceFromContext(t *testing.T) {
	p := PublicSitePolicy(PolicyOptions{})
	rec := runMiddleware(t, p, Options{}, true)

	nonce := rec.Header().Get(security.NonceHeader)
	if nonce == "" {
		t.Fatalf("X-Script-Nonce missing — WithNonce chain broken")
	}
	csp := rec.Header().Get("Content-Security-Policy")
	want := "'nonce-" + nonce + "'"
	if !strings.Contains(csp, want) {
		t.Errorf("CSP did not include %s: %s", want, csp)
	}
}

// TestMiddleware_FoldsCustomNonceFn proves Options.NonceFromContext is
// honored when set, bypassing the default security.NonceFromContext.
func TestMiddleware_FoldsCustomNonceFn(t *testing.T) {
	p := PublicSitePolicy(PolicyOptions{})
	opts := Options{
		NonceFromContext: func(r *http.Request) string { return "FAKENONCE" },
	}
	rec := runMiddleware(t, p, opts, false)
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "'nonce-FAKENONCE'") {
		t.Errorf("custom nonce fn not honored: %s", rec.Header().Get("Content-Security-Policy"))
	}
}

// TestMiddleware_NilPolicyIsPassthrough verifies the documented
// passthrough behavior when the caller passes nil (useful for tests
// disabling CSP without re-wiring the chain).
func TestMiddleware_NilPolicyIsPassthrough(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	rec := httptest.NewRecorder()
	Middleware(nil, Options{})(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("inner handler not reached: code=%d", rec.Code)
	}
	if rec.Header().Get("Content-Security-Policy") != "" {
		t.Errorf("nil policy should not set the header")
	}
}

// TestMiddleware_DoesNotMutateSourcePolicy guards against the
// regression where WithNonce mutates the shared Policy.
func TestMiddleware_DoesNotMutateSourcePolicy(t *testing.T) {
	p := PublicSitePolicy(PolicyOptions{})
	originalString := p.String()

	for i := 0; i < 5; i++ {
		runMiddleware(t, p, Options{}, true)
	}
	if p.String() != originalString {
		t.Errorf("source policy mutated across requests\nbefore: %s\nafter:  %s", originalString, p.String())
	}
}

// TestMiddleware_HeaderNameDefaults pins the default header names for
// both modes via the Options.headerName helper.
func TestMiddleware_HeaderNameDefaults(t *testing.T) {
	o1 := Options{}
	if o1.headerName() != "Content-Security-Policy" {
		t.Errorf("default headerName wrong")
	}
	o2 := Options{ReportOnly: true}
	if o2.headerName() != "Content-Security-Policy-Report-Only" {
		t.Errorf("report-only headerName wrong")
	}
	o3 := Options{Header: "X-Foo"}
	if o3.headerName() != "X-Foo" {
		t.Errorf("override headerName wrong")
	}
}
