package settings

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	pkgsettings "github.com/Singleton-Solution/GoNext/packages/go/settings"
)

// testBase is the route prefix used in every test. Mirrors the
// production wiring in main.go.
const testBase = "/api/v1/settings"

// adminPrincipal is the Principal a request carries when an admin is
// signed in. The admin role holds manage_options in
// DefaultRoleCapabilities, so writes succeed.
func adminPrincipal() policy.Principal {
	return policy.Principal{UserID: "user:admin", Roles: []policy.Role{policy.RoleAdmin}}
}

// subscriberPrincipal lacks manage_options but is authenticated, so
// reads succeed and writes 403.
func subscriberPrincipal() policy.Principal {
	return policy.Principal{UserID: "user:subscriber", Roles: []policy.Role{policy.RoleSubscriber}}
}

// newTestDeps builds a Deps with a fresh registry seeded with core +
// privacy settings and a MemoryStore. The returned Registry pointer is
// the same one in Deps, so tests that want to register additional
// settings can do so after construction.
func newTestDeps(t *testing.T) Deps {
	t.Helper()
	reg := pkgsettings.NewRegistry()
	if err := pkgsettings.RegisterCore(reg); err != nil {
		t.Fatalf("RegisterCore: %v", err)
	}
	if err := pkgsettings.RegisterPrivacy(reg); err != nil {
		t.Fatalf("RegisterPrivacy: %v", err)
	}
	return Deps{
		Store:    pkgsettings.NewMemoryStore(reg),
		Registry: reg,
		Policy:   policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
	}
}

// mountTest builds a fresh mux + handler from deps. Returns the mux so
// tests can drive ServeHTTP directly.
func mountTest(t *testing.T, deps Deps) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	if err := Mount(mux, testBase, deps); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return mux
}

// do executes req against mux and returns the recorded response. A
// helper because every test does this five-line sequence.
func do(t *testing.T, mux *http.ServeMux, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// withPrincipal returns a copy of req with the given Principal stashed
// on its context — mirrors what the auth middleware does in production.
func withPrincipal(req *http.Request, pr policy.Principal) *http.Request {
	return req.WithContext(policy.WithPrincipal(req.Context(), pr))
}

// TestMount_RequiresAuth verifies that an anonymous GET returns 401
// rather than serving the response or panicking on a nil Principal.
func TestMount_RequiresAuth(t *testing.T) {
	deps := newTestDeps(t)
	mux := mountTest(t, deps)

	req := httptest.NewRequest(http.MethodGet, testBase, nil)
	rec := do(t, mux, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: want 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestGet_EmptyGroup_ReturnsEmptyMap is the acceptance-test from issue
// #499: a GET with a registered group whose keys are not yet stored
// returns 200 with the registered defaults (or an empty map if no keys
// match). Critically, it does NOT 404 or 500 — the admin form has a
// shape to render.
func TestGet_EmptyGroup_ReturnsEmptyMap(t *testing.T) {
	// Use a registry with NO settings registered so the response is
	// guaranteed empty. This is the strictest form of the acceptance
	// criterion: empty group means {} body, not 500.
	reg := pkgsettings.NewRegistry()
	deps := Deps{
		Store:    pkgsettings.NewMemoryStore(reg),
		Registry: reg,
		Policy:   policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
	}
	mux := mountTest(t, deps)

	req := httptest.NewRequest(http.MethodGet, testBase+"?group=core.site", nil)
	req = withPrincipal(req, adminPrincipal())
	rec := do(t, mux, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v body=%s", err, rec.Body.String())
	}
	if len(body) != 0 {
		t.Fatalf("response body: want {}, got %v", body)
	}
}

// TestGet_CoreSite_ReturnsDefaults verifies the happy path for the
// admin /settings/general page: GET ?group=core.site returns the
// registered defaults for core.site.* keys.
func TestGet_CoreSite_ReturnsDefaults(t *testing.T) {
	deps := newTestDeps(t)
	mux := mountTest(t, deps)

	req := httptest.NewRequest(http.MethodGet, testBase+"?group=core.site", nil)
	req = withPrincipal(req, adminPrincipal())
	rec := do(t, mux, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	// Expect at least core.site.name to be present with its default.
	if got, ok := body["core.site.name"].(string); !ok || got != "My GoNext Site" {
		t.Fatalf("core.site.name: want default %q, got %v", "My GoNext Site", body["core.site.name"])
	}
	// core.permalinks.format is in a different group prefix and must
	// NOT leak into the response.
	if _, present := body["core.permalinks.format"]; present {
		t.Fatalf("response leaked permalinks key into core.site group: %v", body)
	}
}

// TestGet_PrivacyGroup_RewritesPrefix verifies the special-case "privacy"
// → "core.privacy." prefix rewrite documented on groupPrefix.
func TestGet_PrivacyGroup_RewritesPrefix(t *testing.T) {
	deps := newTestDeps(t)
	mux := mountTest(t, deps)

	req := httptest.NewRequest(http.MethodGet, testBase+"?group=privacy", nil)
	req = withPrincipal(req, adminPrincipal())
	rec := do(t, mux, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if _, present := body[pkgsettings.PrivacyAllowGDPRSelfService]; !present {
		t.Fatalf("response missing privacy key %q: body=%v",
			pkgsettings.PrivacyAllowGDPRSelfService, body)
	}
}

// TestGet_NoGroup_ReturnsAll verifies that an unfiltered GET returns
// every registered key — the shape the OpenAPI spec documents
// (`additionalProperties: true`).
func TestGet_NoGroup_ReturnsAll(t *testing.T) {
	deps := newTestDeps(t)
	mux := mountTest(t, deps)

	req := httptest.NewRequest(http.MethodGet, testBase, nil)
	req = withPrincipal(req, adminPrincipal())
	rec := do(t, mux, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	// Core + privacy registries together ship >10 settings. A strict
	// count here would rot every time a key is added; assert the floor.
	if len(body) < 10 {
		t.Fatalf("unfiltered response: want >=10 keys, got %d (%v)", len(body), body)
	}
}

// TestPatch_HappyPath verifies that a valid PATCH persists the value
// and the response echoes it.
func TestPatch_HappyPath(t *testing.T) {
	deps := newTestDeps(t)
	mux := mountTest(t, deps)

	payload := map[string]any{"core.site.name": "Hello"}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPatch, testBase, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, adminPrincipal())
	rec := do(t, mux, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got["core.site.name"] != "Hello" {
		t.Fatalf("response: want Hello, got %v", got["core.site.name"])
	}

	// Round-trip via GET to confirm persistence.
	getReq := httptest.NewRequest(http.MethodGet, testBase+"?group=core.site", nil)
	getReq = withPrincipal(getReq, adminPrincipal())
	getRec := do(t, mux, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status: want 200, got %d", getRec.Code)
	}
	var afterGet map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &afterGet); err != nil {
		t.Fatalf("get unmarshal: %v", err)
	}
	if afterGet["core.site.name"] != "Hello" {
		t.Fatalf("post-write GET: want Hello, got %v", afterGet["core.site.name"])
	}
}

// TestPatch_RequiresManageOptions verifies that a request from a user
// without manage_options is rejected 403.
func TestPatch_RequiresManageOptions(t *testing.T) {
	deps := newTestDeps(t)
	mux := mountTest(t, deps)

	body, _ := json.Marshal(map[string]any{"core.site.name": "Nope"})
	req := httptest.NewRequest(http.MethodPatch, testBase, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, subscriberPrincipal())
	rec := do(t, mux, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestPatch_UnknownKey_Returns400 verifies that PATCH with an
// unregistered key returns a structured 400 rather than committing or
// 500'ing.
func TestPatch_UnknownKey_Returns400(t *testing.T) {
	deps := newTestDeps(t)
	mux := mountTest(t, deps)

	body, _ := json.Marshal(map[string]any{"this.does.not.exist": "x"})
	req := httptest.NewRequest(http.MethodPatch, testBase, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, adminPrincipal())
	rec := do(t, mux, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unknown") {
		t.Fatalf("response should mention 'unknown', got %s", rec.Body.String())
	}
}

// TestPatch_ValidationError verifies that a value violating the
// schema is rejected 400.
func TestPatch_ValidationError(t *testing.T) {
	deps := newTestDeps(t)
	mux := mountTest(t, deps)

	// core.site.name has minLength:1, maxLength:80. An empty string
	// fails minLength.
	body, _ := json.Marshal(map[string]any{"core.site.name": ""})
	req := httptest.NewRequest(http.MethodPatch, testBase, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, adminPrincipal())
	rec := do(t, mux, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestPatch_BadJSON returns 400 on a malformed body.
func TestPatch_BadJSON(t *testing.T) {
	deps := newTestDeps(t)
	mux := mountTest(t, deps)

	req := httptest.NewRequest(http.MethodPatch, testBase, strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, adminPrincipal())
	rec := do(t, mux, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
}

// TestMount_NilDeps_Errors verifies that Mount returns an error
// rather than panicking when Deps is malformed.
func TestMount_NilDeps_Errors(t *testing.T) {
	mux := http.NewServeMux()
	if err := Mount(mux, testBase, Deps{}); err == nil {
		t.Fatal("Mount: want error for empty Deps, got nil")
	}
}
