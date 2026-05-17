package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve runs the security.Headers middleware around a tiny OK handler and
// returns the recorded response. It centralizes the boilerplate so each
// test focuses on the assertion that matters.
func serve(t *testing.T, opts Options) *httptest.ResponseRecorder {
	t.Helper()
	h := Headers(opts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec
}

func TestHeaders_DefaultOptionsAppliesFullBaseline(t *testing.T) {
	rec := serve(t, DefaultOptions())

	want := map[string]string{
		"Strict-Transport-Security":    "max-age=63072000; includeSubDomains; preload",
		"X-Content-Type-Options":       "nosniff",
		"Referrer-Policy":              "strict-origin-when-cross-origin",
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Embedder-Policy": "require-corp",
		"Cross-Origin-Resource-Policy": "same-site",
		"X-Frame-Options":              "DENY",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s: got %q, want %q", k, got, v)
		}
	}
	// Permissions-Policy is dynamic but must be present and contain the
	// deny tokens we promise downstream.
	pp := rec.Header().Get("Permissions-Policy")
	if pp == "" {
		t.Fatalf("Permissions-Policy: missing")
	}
	for _, must := range []string{"camera=()", "geolocation=()", "microphone=()", "interest-cohort=()"} {
		if !strings.Contains(pp, must) {
			t.Errorf("Permissions-Policy missing %q in: %s", must, pp)
		}
	}
}

func TestHeaders_PublicSiteRelaxesCOEP(t *testing.T) {
	rec := serve(t, PublicSite())
	if got := rec.Header().Get("Cross-Origin-Embedder-Policy"); got != "credentialless" {
		t.Errorf("COEP: got %q, want credentialless", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options: got %q, want DENY", got)
	}
}

func TestHeaders_AdminUsesStrictestSettings(t *testing.T) {
	rec := serve(t, Admin())
	checks := map[string]string{
		"Cross-Origin-Embedder-Policy": "require-corp",
		"X-Frame-Options":              "DENY",
		"Referrer-Policy":              "same-origin",
	}
	for k, v := range checks {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s: got %q, want %q", k, got, v)
		}
	}
}

func TestHeaders_RESTAPIDropsDocumentOnlyPolicies(t *testing.T) {
	rec := serve(t, RESTAPI())

	// COOP/COEP are document-only and intentionally omitted on JSON APIs.
	if got := rec.Header().Get("Cross-Origin-Opener-Policy"); got != "" {
		t.Errorf("COOP should be omitted on REST API, got %q", got)
	}
	if got := rec.Header().Get("Cross-Origin-Embedder-Policy"); got != "" {
		t.Errorf("COEP should be omitted on REST API, got %q", got)
	}

	// CORP relaxed so browser fetches from approved origins succeed.
	if got := rec.Header().Get("Cross-Origin-Resource-Policy"); got != "cross-origin" {
		t.Errorf("CORP: got %q, want cross-origin", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy: got %q, want no-referrer", got)
	}
	if got := rec.Header().Get("Permissions-Policy"); got != "interest-cohort=()" {
		t.Errorf("Permissions-Policy: got %q, want interest-cohort=()", got)
	}
	// HSTS and nosniff still apply.
	if got := rec.Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("HSTS should still be set on REST API")
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options: got %q, want nosniff", got)
	}
}

func TestHeaders_DisableFlagsOmitHeaders(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Options)
		header string
	}{
		{"hsts", func(o *Options) { o.DisableHSTS = true }, "Strict-Transport-Security"},
		{"nosniff", func(o *Options) { o.DisableContentTypeOptions = true }, "X-Content-Type-Options"},
		{"referrer", func(o *Options) { o.DisableReferrerPolicy = true }, "Referrer-Policy"},
		{"permissions", func(o *Options) { o.DisablePermissionsPolicy = true }, "Permissions-Policy"},
		{"coop", func(o *Options) { o.DisableCOOP = true }, "Cross-Origin-Opener-Policy"},
		{"coep", func(o *Options) { o.DisableCOEP = true }, "Cross-Origin-Embedder-Policy"},
		{"corp", func(o *Options) { o.DisableCORP = true }, "Cross-Origin-Resource-Policy"},
		{"xfo", func(o *Options) { o.DisableFrameOptions = true }, "X-Frame-Options"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := DefaultOptions()
			tc.mutate(&opts)
			rec := serve(t, opts)
			if got := rec.Header().Get(tc.header); got != "" {
				t.Errorf("%s: should be absent, got %q", tc.header, got)
			}
		})
	}
}

func TestHeaders_OverridePerHeader(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*Options)
		header    string
		wantValue string
	}{
		{
			name:      "hsts custom max-age and no preload",
			mutate:    func(o *Options) { o.HSTS = HSTSOptions{MaxAgeSeconds: 86400} },
			header:    "Strict-Transport-Security",
			wantValue: "max-age=86400",
		},
		{
			name:      "referrer policy override",
			mutate:    func(o *Options) { o.ReferrerPolicy = "no-referrer" },
			header:    "Referrer-Policy",
			wantValue: "no-referrer",
		},
		{
			name:      "frame options sameorigin",
			mutate:    func(o *Options) { o.FrameOptions = "SAMEORIGIN" },
			header:    "X-Frame-Options",
			wantValue: "SAMEORIGIN",
		},
		{
			name:      "corp cross-origin",
			mutate:    func(o *Options) { o.CORP = "cross-origin" },
			header:    "Cross-Origin-Resource-Policy",
			wantValue: "cross-origin",
		},
		{
			name:      "coep credentialless",
			mutate:    func(o *Options) { o.COEP = "credentialless" },
			header:    "Cross-Origin-Embedder-Policy",
			wantValue: "credentialless",
		},
		{
			name:      "permissions policy verbatim",
			mutate:    func(o *Options) { o.PermissionsPolicy = "geolocation=(self)" },
			header:    "Permissions-Policy",
			wantValue: "geolocation=(self)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := DefaultOptions()
			tc.mutate(&opts)
			rec := serve(t, opts)
			if got := rec.Header().Get(tc.header); got != tc.wantValue {
				t.Errorf("%s: got %q, want %q", tc.header, got, tc.wantValue)
			}
		})
	}
}

func TestHeaders_PermissionsAllowOverridesDenyList(t *testing.T) {
	opts := DefaultOptions()
	opts.PermissionsAllow = map[string]string{
		"fullscreen":  "self",
		"geolocation": "self",
		"web-share":   "*",
	}
	rec := serve(t, opts)
	pp := rec.Header().Get("Permissions-Policy")

	wantContains := []string{
		"fullscreen=(self)",
		"geolocation=(self)",
		"web-share=*",
		// Untouched features remain denied.
		"camera=()",
		"microphone=()",
	}
	for _, frag := range wantContains {
		if !strings.Contains(pp, frag) {
			t.Errorf("Permissions-Policy missing %q in: %s", frag, pp)
		}
	}
}

func TestHeaders_PermissionsAllowAlreadyParenthesizedPasses(t *testing.T) {
	// Callers may pass a pre-parenthesized value (e.g. "(self \"https://x.com\")");
	// the builder should not double-wrap.
	opts := DefaultOptions()
	opts.PermissionsAllow = map[string]string{
		"payment": `(self "https://pay.example.com")`,
	}
	rec := serve(t, opts)
	pp := rec.Header().Get("Permissions-Policy")
	want := `payment=(self "https://pay.example.com")`
	if !strings.Contains(pp, want) {
		t.Errorf("Permissions-Policy: missing %q in: %s", want, pp)
	}
}

func TestHeaders_CSPNotEmittedByDefault(t *testing.T) {
	// CSP is intentionally out of scope for this middleware. Confirm no
	// preset accidentally sets it.
	for _, preset := range []struct {
		name string
		opts Options
	}{
		{"default", DefaultOptions()},
		{"public", PublicSite()},
		{"admin", Admin()},
		{"rest", RESTAPI()},
	} {
		t.Run(preset.name, func(t *testing.T) {
			rec := serve(t, preset.opts)
			if got := rec.Header().Get("Content-Security-Policy"); got != "" {
				t.Errorf("CSP should not be set by Headers middleware, got %q", got)
			}
			if got := rec.Header().Get("Content-Security-Policy-Report-Only"); got != "" {
				t.Errorf("CSP-Report-Only should not be set by Headers middleware, got %q", got)
			}
		})
	}
}

func TestHeaders_DownstreamHandlerRuns(t *testing.T) {
	// The middleware must call next.ServeHTTP — confirm the inner handler
	// is reached and can still write a body.
	called := false
	h := Headers(DefaultOptions())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("brew"))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !called {
		t.Fatal("inner handler not invoked")
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("status: got %d, want 418", rec.Code)
	}
	if rec.Body.String() != "brew" {
		t.Errorf("body: got %q, want %q", rec.Body.String(), "brew")
	}
	// Headers must still be present.
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options: got %q, want nosniff", got)
	}
}

func TestHeaders_HSTSZeroMaxAgeOmitsHeader(t *testing.T) {
	// Defense: setting MaxAgeSeconds=0 must NOT produce
	// "Strict-Transport-Security: max-age=0" because browsers interpret
	// that as "clear the pin". Better to omit than to clear by accident.
	opts := DefaultOptions()
	opts.HSTS = HSTSOptions{MaxAgeSeconds: 0, IncludeSubDomains: true, Preload: true}
	rec := serve(t, opts)
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS with zero max-age should be omitted, got %q", got)
	}
}

func TestHSTSOptions_String(t *testing.T) {
	cases := []struct {
		name string
		in   HSTSOptions
		want string
	}{
		{"zero", HSTSOptions{}, ""},
		{"max-age only", HSTSOptions{MaxAgeSeconds: 60}, "max-age=60"},
		{"with includeSubDomains", HSTSOptions{MaxAgeSeconds: 60, IncludeSubDomains: true}, "max-age=60; includeSubDomains"},
		{"full preload", HSTSOptions{MaxAgeSeconds: 63072000, IncludeSubDomains: true, Preload: true}, "max-age=63072000; includeSubDomains; preload"},
		{"preload without subdomains", HSTSOptions{MaxAgeSeconds: 60, Preload: true}, "max-age=60; preload"},
		{"negative max-age omitted", HSTSOptions{MaxAgeSeconds: -1, Preload: true}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.String(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatAllowValue(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "()"},
		{"*", "*"},
		{"self", "(self)"},
		{"  self  ", "(self)"},
		{"(self)", "(self)"},
		{`(self "https://x.com")`, `(self "https://x.com")`},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := formatAllowValue(tc.in); got != tc.want {
				t.Errorf("formatAllowValue(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
