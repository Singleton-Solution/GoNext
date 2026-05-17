package security

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// resetClassifier restores the default classifier state between tests.
// Tests that mutate package-level configuration must defer this to keep
// the suite hermetic.
func resetClassifier() {
	classifierMu.Lock()
	classifierFn = nil
	prefixTable = newPrefixMap(defaultPrefixes, RouteClassPublic)
	classifierMu.Unlock()
}

func TestRouteClass_String(t *testing.T) {
	cases := []struct {
		in   RouteClass
		want string
	}{
		{RouteClassPublic, "public"},
		{RouteClassAdmin, "admin"},
		{RouteClassRESTAPI, "rest-api"},
		{RouteClassPluginFrontend, "plugin-frontend"},
		{RouteClassMedia, "media"},
		{RouteClass(999), "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := tc.in.String(); got != tc.want {
				t.Errorf("RouteClass(%d).String() = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestClassify_DefaultPrefixes(t *testing.T) {
	defer resetClassifier()
	resetClassifier()

	cases := []struct {
		path string
		want RouteClass
	}{
		{"/", RouteClassPublic},
		{"/about", RouteClassPublic},
		{"/admin/", RouteClassAdmin},
		{"/admin/users", RouteClassAdmin},
		{"/admin/api/users", RouteClassRESTAPI},
		{"/api/v1/widgets", RouteClassRESTAPI},
		{"/media/photo.jpg", RouteClassMedia},
		{"/static/app.css", RouteClassMedia},
		{"/assets/font.woff2", RouteClassMedia},
		{"/plugins/hello/index.js", RouteClassPluginFrontend},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if got := Classify(r); got != tc.want {
				t.Errorf("Classify(%s) = %s, want %s", tc.path, got, tc.want)
			}
		})
	}
}

// TestClassify_LongestPrefixWins is the key correctness property: an
// /admin/api/* request must classify as REST API, not Admin, because
// the longer prefix is more specific.
func TestClassify_LongestPrefixWins(t *testing.T) {
	defer resetClassifier()
	resetClassifier()

	r := httptest.NewRequest(http.MethodGet, "/admin/api/users/42", nil)
	if got := Classify(r); got != RouteClassRESTAPI {
		t.Errorf("Classify(/admin/api/...) = %s, want rest-api", got)
	}
}

func TestClassify_NilRequest(t *testing.T) {
	defer resetClassifier()
	resetClassifier()

	if got := Classify(nil); got != RouteClassPublic {
		t.Errorf("Classify(nil) = %s, want public", got)
	}
}

func TestClassify_NilURL(t *testing.T) {
	defer resetClassifier()
	resetClassifier()

	r := &http.Request{} // URL is nil
	if got := Classify(r); got != RouteClassPublic {
		t.Errorf("Classify(req with nil URL) = %s, want public", got)
	}
}

func TestSetClassifierPrefixes_OverrideAndFallback(t *testing.T) {
	defer resetClassifier()

	SetClassifierPrefixes(map[string]RouteClass{
		"/v2/admin/": RouteClassAdmin,
		"/v2/api/":   RouteClassRESTAPI,
		"/cdn/":      RouteClassMedia,
	}, RouteClassPluginFrontend)

	cases := []struct {
		path string
		want RouteClass
	}{
		{"/v2/admin/x", RouteClassAdmin},
		{"/v2/api/x", RouteClassRESTAPI},
		{"/cdn/x.png", RouteClassMedia},
		{"/", RouteClassPluginFrontend}, // fallback
		{"/admin/", RouteClassPluginFrontend},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if got := Classify(r); got != tc.want {
				t.Errorf("Classify(%s) = %s, want %s", tc.path, got, tc.want)
			}
		})
	}
}

func TestSetClassifier_CustomFunction(t *testing.T) {
	defer resetClassifier()

	// A custom classifier that flips between admin/public based on a
	// header, demonstrating request-aware logic that prefix matching
	// can't express.
	SetClassifier(func(r *http.Request) RouteClass {
		if r.Header.Get("X-Role") == "admin" {
			return RouteClassAdmin
		}
		return RouteClassPublic
	})

	r1 := httptest.NewRequest(http.MethodGet, "/anything", nil)
	if got := Classify(r1); got != RouteClassPublic {
		t.Errorf("no header: got %s, want public", got)
	}

	r2 := httptest.NewRequest(http.MethodGet, "/anything", nil)
	r2.Header.Set("X-Role", "admin")
	if got := Classify(r2); got != RouteClassAdmin {
		t.Errorf("admin header: got %s, want admin", got)
	}

	// Reset and verify the default path-prefix classifier is back.
	SetClassifier(nil)
	r3 := httptest.NewRequest(http.MethodGet, "/admin/x", nil)
	if got := Classify(r3); got != RouteClassAdmin {
		t.Errorf("after reset: got %s, want admin", got)
	}
}

func TestOptionsFor_PerClassPresets(t *testing.T) {
	// Spot-check that each class maps to a distinguishable preset so
	// callers wiring a class to Headers get the right header matrix.
	cases := []struct {
		class   RouteClass
		header  string
		want    string
		comment string
	}{
		{RouteClassPublic, "Cross-Origin-Embedder-Policy", "credentialless", "public uses public-site preset"},
		{RouteClassAdmin, "Cross-Origin-Embedder-Policy", "require-corp", "admin tightens COEP"},
		{RouteClassAdmin, "Referrer-Policy", "same-origin", "admin tightens referrer"},
		{RouteClassRESTAPI, "Cross-Origin-Resource-Policy", "cross-origin", "REST is cross-origin"},
		{RouteClassPluginFrontend, "Cross-Origin-Embedder-Policy", "credentialless", "plugin frontends are public-style"},
		{RouteClassMedia, "Cross-Origin-Resource-Policy", "cross-origin", "media must be embeddable"},
	}
	for _, tc := range cases {
		t.Run(tc.class.String()+"/"+tc.header, func(t *testing.T) {
			opts := OptionsFor(tc.class)
			rec := serve(t, opts)
			if got := rec.Header().Get(tc.header); got != tc.want {
				t.Errorf("%s: got %q, want %q (%s)", tc.header, got, tc.want, tc.comment)
			}
		})
	}
}

func TestOptionsFor_MediaDropsDocumentOnlyHeaders(t *testing.T) {
	rec := serve(t, OptionsFor(RouteClassMedia))
	if got := rec.Header().Get("Cross-Origin-Opener-Policy"); got != "" {
		t.Errorf("COOP should be omitted for media, got %q", got)
	}
	if got := rec.Header().Get("Cross-Origin-Embedder-Policy"); got != "" {
		t.Errorf("COEP should be omitted for media, got %q", got)
	}
}

func TestOptionsFor_UnknownClassFallsBackToPublic(t *testing.T) {
	opts := OptionsFor(RouteClass(999))
	rec := serve(t, opts)
	// public preset uses credentialless COEP
	if got := rec.Header().Get("Cross-Origin-Embedder-Policy"); got != "credentialless" {
		t.Errorf("unknown class should fall back to public, got COEP=%q", got)
	}
}

func TestClassifiedHeaders_AppliesPerClassPreset(t *testing.T) {
	defer resetClassifier()
	resetClassifier()

	mw := ClassifiedHeaders()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(inner)

	cases := []struct {
		path        string
		headerKey   string
		headerValue string
	}{
		{"/admin/users", "Referrer-Policy", "same-origin"},               // Admin preset
		{"/api/widgets", "Cross-Origin-Resource-Policy", "cross-origin"}, // REST preset
		{"/media/x.jpg", "Cross-Origin-Resource-Policy", "cross-origin"}, // Media preset
		{"/about", "Cross-Origin-Embedder-Policy", "credentialless"},     // Public preset
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if got := rec.Header().Get(tc.headerKey); got != tc.headerValue {
				t.Errorf("%s on %s: got %q, want %q", tc.headerKey, tc.path, got, tc.headerValue)
			}
		})
	}
}

// TestClassifiedHeaders_StripsServerHeader sanity-checks that the
// per-class wrapping still benefits from the identifying-header strip
// inherited from each class's preset.
func TestClassifiedHeaders_StripsServerHeader(t *testing.T) {
	defer resetClassifier()
	resetClassifier()

	mw := ClassifiedHeaders()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(inner)

	rec := httptest.NewRecorder()
	rec.Header().Set("Server", "leaky/1.0")
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/x", nil))
	if got := rec.Header().Get("Server"); got != "" {
		t.Errorf("Server should be stripped on classified routes, got %q", got)
	}
}
