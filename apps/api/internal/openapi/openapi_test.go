package openapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSpec_IsOpenAPI31 guards the version pin — issue #29's house style is
// 3.1, not 3.0. A typo here would silently produce a spec that older tools
// accept but that's missing the 3.1 features we'll rely on later.
func TestSpec_IsOpenAPI31(t *testing.T) {
	t.Parallel()

	var doc map[string]any
	if err := json.Unmarshal(SpecBytes(), &doc); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
	v, ok := doc["openapi"].(string)
	if !ok {
		t.Fatalf("openapi field missing or not a string: %#v", doc["openapi"])
	}
	if !strings.HasPrefix(v, "3.1") {
		t.Fatalf("openapi version must be 3.1.x, got %q", v)
	}
}

// TestSpec_HasRequiredSections ensures the scaffold isn't accidentally
// gutted. Each section is referenced by name in docs/05-admin-api.md and
// by downstream tooling, so a missing key is a regression worth flagging
// loud at test time rather than at runtime.
func TestSpec_HasRequiredSections(t *testing.T) {
	t.Parallel()

	var doc map[string]any
	if err := json.Unmarshal(SpecBytes(), &doc); err != nil {
		t.Fatalf("spec parse: %v", err)
	}

	for _, key := range []string{"info", "servers", "paths", "components"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("spec missing top-level %q", key)
		}
	}

	info, _ := doc["info"].(map[string]any)
	if got, _ := info["title"].(string); got != "GoNext API" {
		t.Errorf("info.title = %q, want %q", got, "GoNext API")
	}
	if got, _ := info["version"].(string); got != "0.0.0" {
		t.Errorf("info.version = %q, want %q", got, "0.0.0")
	}

	paths, _ := doc["paths"].(map[string]any)
	if _, ok := paths["/"]; !ok {
		t.Error("paths missing GET / identity endpoint")
	}

	comps, _ := doc["components"].(map[string]any)
	schemes, _ := comps["securitySchemes"].(map[string]any)
	for _, want := range []string{"CookieSession", "BearerJWT", "ApplicationPassword"} {
		if _, ok := schemes[want]; !ok {
			t.Errorf("components.securitySchemes missing %q", want)
		}
	}

	responses, _ := comps["responses"].(map[string]any)
	for _, want := range []string{"BadRequest", "Unauthorized", "Forbidden", "NotFound", "TooManyRequests", "InternalError"} {
		if _, ok := responses[want]; !ok {
			t.Errorf("components.responses missing %q", want)
		}
	}
}

// TestSpec_MirrorMatchesCanonical proves the copy embedded into the Go
// binary is byte-identical to the canonical copy at
// apps/api/openapi/gonext.openapi.json. Without this check the two files
// would drift silently on any edit — see openapi/README.md for the editing
// workflow.
//
// Skips if the canonical file isn't reachable (e.g. running from a stripped
// binary tree); that keeps the test useful in-repo without making the
// package depend on a particular working directory.
func TestSpec_MirrorMatchesCanonical(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("runtime.Caller unavailable")
	}
	// thisFile is apps/api/internal/openapi/openapi_test.go.
	// Canonical lives at  apps/api/openapi/gonext.openapi.json.
	canonical := filepath.Join(filepath.Dir(thisFile), "..", "..", "openapi", "gonext.openapi.json")
	got, err := os.ReadFile(canonical)
	if err != nil {
		t.Skipf("canonical spec not reachable from test: %v", err)
	}
	if string(got) != string(SpecBytes()) {
		t.Fatalf("embedded spec drifted from canonical %s — re-run the copy step (see openapi/README.md)", canonical)
	}
}

// TestHandler_ServesSpec exercises the Handler() round-trip: 200, correct
// content-type, body parses as JSON, and conditional-request ETag works.
func TestHandler_ServesSpec(t *testing.T) {
	t.Parallel()

	h := Handler()

	t.Run("GET returns spec", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("Content-Type = %q, want application/json prefix", ct)
		}
		var doc map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("body is not JSON: %v", err)
		}
		if v, _ := doc["openapi"].(string); !strings.HasPrefix(v, "3.1") {
			t.Errorf("openapi = %q, want 3.1.x", v)
		}
	})

	t.Run("HEAD has no body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodHead, "/openapi.json", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("HEAD status = %d, want 200", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("HEAD body len = %d, want 0", rec.Body.Len())
		}
	})

	t.Run("POST rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/openapi.json", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST status = %d, want 405", rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
			t.Errorf("Allow header = %q, want %q", got, "GET, HEAD")
		}
	})

	t.Run("If-None-Match short-circuits", func(t *testing.T) {
		// First request: capture ETag.
		req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		etag := rec.Header().Get("ETag")
		if etag == "" {
			t.Fatal("ETag header missing on initial response")
		}

		// Second request: send the ETag back, expect 304.
		req2 := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
		req2.Header.Set("If-None-Match", etag)
		rec2 := httptest.NewRecorder()
		h.ServeHTTP(rec2, req2)
		if rec2.Code != http.StatusNotModified {
			t.Errorf("If-None-Match status = %d, want 304", rec2.Code)
		}
	})
}

// TestSwaggerUIHandler asserts the dev-mode UI page is served with HTML
// content-type and points at /openapi.json. Keeping this check loose: the
// page is intentionally a tiny static blob, so we mostly want to confirm
// the wiring rather than the exact markup.
func TestSwaggerUIHandler(t *testing.T) {
	t.Parallel()

	h := SwaggerUIHandler()

	req := httptest.NewRequest(http.MethodGet, "/docs/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html prefix", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "/openapi.json") {
		t.Errorf("body should reference /openapi.json; got: %s", body)
	}
	if !strings.Contains(body, "swagger-ui") {
		t.Errorf("body should reference swagger-ui; got: %s", body)
	}

	t.Run("POST rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/docs/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST status = %d, want 405", rec.Code)
		}
	})
}

// TestSpecBytes_ReturnsCopy ensures callers can't mutate the embedded spec
// through SpecBytes(). A shared backing array would let a misbehaving test
// poison every subsequent request.
func TestSpecBytes_ReturnsCopy(t *testing.T) {
	t.Parallel()

	a := SpecBytes()
	b := SpecBytes()
	if len(a) == 0 {
		t.Fatal("SpecBytes is empty")
	}
	if &a[0] == &b[0] {
		t.Fatal("SpecBytes returned the same backing array twice; callers can corrupt each other")
	}
}
