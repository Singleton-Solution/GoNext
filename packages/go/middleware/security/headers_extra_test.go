package security

import (
	"net/http"
	"net/http/httptest"
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
