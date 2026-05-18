package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHeaders_StripIdentifyingHeadersOnByDefault verifies that the
// default presets remove the Server and X-Powered-By headers when the
// inner handler (or some upstream component) sets them.
func TestHeaders_StripIdentifyingHeadersOnByDefault(t *testing.T) {
	presets := []struct {
		name string
		opts Options
	}{
		{"default", DefaultOptions()},
		{"public", PublicSite()},
		{"admin", Admin()},
		{"rest", RESTAPI()},
	}
	for _, p := range presets {
		t.Run(p.name, func(t *testing.T) {
			// The inner handler intentionally pretends to be a chatty
			// framework that leaks identifying headers — the
			// middleware must scrub them before the response is sent.
			h := Headers(p.opts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Server", "leaky/1.0")
				w.Header().Set("X-Powered-By", "Express")
				w.WriteHeader(http.StatusOK)
			}))
			rec := httptest.NewRecorder()
			// Pre-populate the recorder's headers so the middleware
			// has something to delete BEFORE the inner handler runs.
			rec.Header().Set("Server", "preset/1.0")
			rec.Header().Set("X-Powered-By", "Drupal")
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			// The middleware deletes BEFORE next runs (documented
			// "writes, doesn't lock" behavior). The downstream handler
			// re-sets them — that's the inner handler's prerogative.
			// We assert here that the middleware DID delete them; the
			// inner handler's re-set is then visible in the recorder.
			//
			// To verify the strip semantics in isolation, swap to a
			// handler that does NOT re-set them:
			h2 := Headers(p.opts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			rec2 := httptest.NewRecorder()
			rec2.Header().Set("Server", "preset/1.0")
			rec2.Header().Set("X-Powered-By", "Drupal")
			h2.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/", nil))

			if got := rec2.Header().Get("Server"); got != "" {
				t.Errorf("Server should be stripped, got %q", got)
			}
			if got := rec2.Header().Get("X-Powered-By"); got != "" {
				t.Errorf("X-Powered-By should be stripped, got %q", got)
			}
		})
	}
}

// TestHeaders_DownstreamCanReassertServer documents that the middleware
// strips identifying headers BEFORE the inner handler runs — so a
// downstream handler that has a legitimate reason to set Server (e.g. a
// proxy passthrough) is not blocked.
func TestHeaders_DownstreamCanReassertServer(t *testing.T) {
	h := Headers(DefaultOptions())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "deliberate/2.0")
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("Server"); got != "deliberate/2.0" {
		t.Errorf("downstream Server: got %q, want %q", got, "deliberate/2.0")
	}
}

// TestHeaders_DisableStripIdentifyingHeadersHonored verifies the
// explicit opt-out: when DisableStripIdentifyingHeaders is true, the
// middleware leaves pre-existing Server / X-Powered-By alone.
func TestHeaders_DisableStripIdentifyingHeadersHonored(t *testing.T) {
	opts := DefaultOptions()
	opts.DisableStripIdentifyingHeaders = true

	h := Headers(opts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	rec.Header().Set("Server", "preset/1.0")
	rec.Header().Set("X-Powered-By", "Drupal")
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Server"); got != "preset/1.0" {
		t.Errorf("Server should be preserved when strip disabled, got %q", got)
	}
	if got := rec.Header().Get("X-Powered-By"); got != "Drupal" {
		t.Errorf("X-Powered-By should be preserved when strip disabled, got %q", got)
	}
}

// TestHeaders_StripDisabledWhenStripFalseOnZeroOptions ensures that an
// explicit zero-value Options{} does NOT strip headers (StripIdentifying
// defaults to true only via the preset constructors). This guards
// against surprising behavior for callers who hand-roll Options.
func TestHeaders_StripDisabledOnZeroOptions(t *testing.T) {
	h := Headers(Options{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	rec.Header().Set("Server", "preset/1.0")
	rec.Header().Set("X-Powered-By", "Drupal")
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Server"); got != "preset/1.0" {
		t.Errorf("zero Options should NOT strip Server, got %q", got)
	}
	if got := rec.Header().Get("X-Powered-By"); got != "Drupal" {
		t.Errorf("zero Options should NOT strip X-Powered-By, got %q", got)
	}
}

// TestHeaders_StaticBaselineHeadersOnAllPresets confirms the two newly
// added static headers (X-Permitted-Cross-Domain-Policies and
// Origin-Agent-Cluster) show up on every preset with their documented
// default values.
func TestHeaders_StaticBaselineHeadersOnAllPresets(t *testing.T) {
	presets := map[string]Options{
		"default": DefaultOptions(),
		"public":  PublicSite(),
		"admin":   Admin(),
		"rest":    RESTAPI(),
	}
	want := map[string]string{
		"X-Permitted-Cross-Domain-Policies": "none",
		"Origin-Agent-Cluster":              "?1",
	}
	for name, opts := range presets {
		t.Run(name, func(t *testing.T) {
			rec := serve(t, opts)
			for k, v := range want {
				if got := rec.Header().Get(k); got != v {
					t.Errorf("%s: got %q, want %q", k, got, v)
				}
			}
		})
	}
}

// TestHeaders_ZeroOptionsFallbacks exercises the empty-string fallback
// arms for every header — verifying the documented contract that a
// zero-valued field falls back to the canonical default rather than
// emitting an empty header value.
func TestHeaders_ZeroOptionsFallbacks(t *testing.T) {
	// Options with HSTS opt-out so the middleware emits everything
	// EXCEPT HSTS (which requires a non-zero max-age) from defaults.
	opts := Options{DisableHSTS: true}
	rec := serve(t, opts)

	want := map[string]string{
		"X-Content-Type-Options":            "nosniff",
		"Referrer-Policy":                   "strict-origin-when-cross-origin",
		"Cross-Origin-Opener-Policy":        "same-origin",
		"Cross-Origin-Embedder-Policy":      "require-corp",
		"Cross-Origin-Resource-Policy":      "same-site",
		"X-Frame-Options":                   "DENY",
		"X-Permitted-Cross-Domain-Policies": "none",
		"Origin-Agent-Cluster":              "?1",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s: got %q, want %q", k, got, v)
		}
	}
}

// TestHeaders_NosniffAlwaysOnByDefault is the focused #43 acceptance
// check: every preset must emit X-Content-Type-Options: nosniff. We
// validate each preset independently so a regression in any single
// constructor surfaces with a clear test name.
func TestHeaders_NosniffAlwaysOnByDefault(t *testing.T) {
	presets := map[string]Options{
		"default": DefaultOptions(),
		"public":  PublicSite(),
		"admin":   Admin(),
		"rest":    RESTAPI(),
	}
	for name, opts := range presets {
		t.Run(name, func(t *testing.T) {
			rec := serve(t, opts)
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options: got %q, want %q", got, "nosniff")
			}
		})
	}
}

// TestHeaders_StrictForcesCOEPEvenWhenDisabled verifies the Strict
// override: a caller who flips DisableCOEP=true still gets COEP set if
// they also set Strict=true. This is the high-level "I want maximum
// cross-origin isolation regardless of other knobs" toggle.
func TestHeaders_StrictForcesCOEPEvenWhenDisabled(t *testing.T) {
	opts := DefaultOptions()
	opts.DisableCOEP = true // attempt to drop it
	opts.Strict = true      // but Strict wins
	rec := serve(t, opts)
	if got := rec.Header().Get("Cross-Origin-Embedder-Policy"); got != "require-corp" {
		t.Errorf("Strict should force COEP=require-corp, got %q", got)
	}
}

// TestHeaders_StrictHonorsExplicitCOEPOverride verifies that Strict=true
// pins COEP ON but does not stomp an explicit non-empty override. This
// lets a marketing surface keep Strict semantics but loosen COEP to
// "credentialless" for embed-friendliness on a single page.
func TestHeaders_StrictHonorsExplicitCOEPOverride(t *testing.T) {
	opts := DefaultOptions()
	opts.Strict = true
	opts.COEP = "credentialless"
	rec := serve(t, opts)
	if got := rec.Header().Get("Cross-Origin-Embedder-Policy"); got != "credentialless" {
		t.Errorf("explicit COEP override should win over Strict default, got %q", got)
	}
}

// TestHeaders_StrictOffByDefaultOnZeroOptions confirms the field
// defaults to false on a hand-rolled Options{} — so callers who never
// set it observe no behavior change.
func TestHeaders_StrictOffByDefaultOnZeroOptions(t *testing.T) {
	opts := Options{DisableHSTS: true, DisableCOEP: true}
	rec := serve(t, opts)
	if got := rec.Header().Get("Cross-Origin-Embedder-Policy"); got != "" {
		t.Errorf("zero Options with DisableCOEP should omit COEP, got %q", got)
	}
}

// TestHeaders_PermissionsPolicyDisablesInvasiveFeatures captures the
// #43 acceptance promise: the default Permissions-Policy MUST deny
// camera, microphone, and geolocation. Other features are tested
// elsewhere; this is the explicit security-baseline contract.
func TestHeaders_PermissionsPolicyDisablesInvasiveFeatures(t *testing.T) {
	rec := serve(t, DefaultOptions())
	pp := rec.Header().Get("Permissions-Policy")
	if pp == "" {
		t.Fatal("Permissions-Policy: missing")
	}
	for _, must := range []string{"camera=()", "microphone=()", "geolocation=()"} {
		if !strings.Contains(pp, must) {
			t.Errorf("Permissions-Policy missing %q in: %s", must, pp)
		}
	}
}

// TestHeaders_PermissionsPolicyCustomOverrideAppliedVerbatim verifies a
// caller-supplied policy is emitted exactly as given — no normalization,
// no merge with the deny list. This matches the documented contract on
// the PermissionsPolicy field.
func TestHeaders_PermissionsPolicyCustomOverrideAppliedVerbatim(t *testing.T) {
	const custom = "camera=(), microphone=(), geolocation=(), fullscreen=(self)"
	opts := DefaultOptions()
	opts.PermissionsPolicy = custom
	rec := serve(t, opts)
	if got := rec.Header().Get("Permissions-Policy"); got != custom {
		t.Errorf("Permissions-Policy: got %q, want %q (verbatim)", got, custom)
	}
}

// TestHeaders_AppliedOnNon2xxResponses documents the policy that
// security headers are emitted on every response — not just 2xx. 3xx
// redirects and 4xx/5xx error pages frequently render
// attacker-controlled content; omitting headers there would create a
// soft spot. We assert on a 302 redirect and a 500 error.
func TestHeaders_AppliedOnNon2xxResponses(t *testing.T) {
	cases := []struct {
		name   string
		status int
		extra  func(http.ResponseWriter)
	}{
		{
			name:   "302 redirect",
			status: http.StatusFound,
			extra: func(w http.ResponseWriter) {
				w.Header().Set("Location", "/elsewhere")
			},
		},
		{
			name:   "500 error",
			status: http.StatusInternalServerError,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := Headers(DefaultOptions())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.extra != nil {
					tc.extra(w)
				}
				w.WriteHeader(tc.status)
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			if rec.Code != tc.status {
				t.Fatalf("status: got %d, want %d", rec.Code, tc.status)
			}
			// All canonical headers must be present even on non-2xx.
			must := map[string]string{
				"X-Content-Type-Options":     "nosniff",
				"Cross-Origin-Opener-Policy": "same-origin",
				"X-Frame-Options":            "DENY",
				"Referrer-Policy":            "strict-origin-when-cross-origin",
			}
			for k, v := range must {
				if got := rec.Header().Get(k); got != v {
					t.Errorf("%s on %s: got %q, want %q", k, tc.name, got, v)
				}
			}
			if got := rec.Header().Get("Strict-Transport-Security"); got == "" {
				t.Errorf("HSTS should be present on %s", tc.name)
			}
		})
	}
}
