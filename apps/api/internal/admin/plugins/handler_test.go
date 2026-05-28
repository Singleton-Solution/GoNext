package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/lifecycle"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// fakeManager is the test stand-in for *lifecycle.Manager. It stores
// plugins in a map keyed by slug, transitions states in-process, and
// records every call so tests can assert which lifecycle methods the
// handler invoked.
type fakeManager struct {
	mu      sync.Mutex
	plugins map[string]lifecycle.Plugin
	calls   []string

	// hooks let tests inject failure modes (ErrNotFound, an arbitrary
	// install error, etc.) without re-implementing the lifecycle.
	installErr error
}

func newFakeManager() *fakeManager {
	return &fakeManager{plugins: map[string]lifecycle.Plugin{}}
}

func (f *fakeManager) seed(p lifecycle.Plugin) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.plugins[p.Slug] = p
}

func (f *fakeManager) List(_ context.Context) ([]lifecycle.Plugin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "List")
	out := make([]lifecycle.Plugin, 0, len(f.plugins))
	for _, p := range f.plugins {
		out = append(out, p)
	}
	return out, nil
}

func (f *fakeManager) Get(_ context.Context, slug string) (lifecycle.Plugin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "Get:"+slug)
	p, ok := f.plugins[slug]
	if !ok {
		return lifecycle.Plugin{}, lifecycle.ErrNotFound
	}
	return p, nil
}

func (f *fakeManager) Install(_ context.Context, _ io.Reader) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "Install")
	if f.installErr != nil {
		return "", f.installErr
	}
	p := lifecycle.Plugin{
		Slug:        "fake-installed",
		Version:     "1.0.0",
		ABIVersion:  1,
		State:       lifecycle.StateInstalled,
		InstalledAt: time.Now().UTC(),
		Capabilities: []string{"kv.read"},
	}
	f.plugins[p.Slug] = p
	return p.Slug, nil
}

func (f *fakeManager) Activate(_ context.Context, slug string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "Activate:"+slug)
	p, ok := f.plugins[slug]
	if !ok {
		return lifecycle.ErrNotFound
	}
	if p.State != lifecycle.StateInstalled && p.State != lifecycle.StateInactive {
		return fmt.Errorf("%w: cannot activate from %s", lifecycle.ErrInvalidTransition, p.State)
	}
	p.State = lifecycle.StateActive
	p.ActivatedAt = time.Now().UTC()
	f.plugins[slug] = p
	return nil
}

func (f *fakeManager) Deactivate(_ context.Context, slug string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "Deactivate:"+slug)
	p, ok := f.plugins[slug]
	if !ok {
		return lifecycle.ErrNotFound
	}
	if p.State != lifecycle.StateActive {
		return fmt.Errorf("%w: cannot deactivate from %s", lifecycle.ErrInvalidTransition, p.State)
	}
	p.State = lifecycle.StateInactive
	f.plugins[slug] = p
	return nil
}

func (f *fakeManager) Uninstall(_ context.Context, slug string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "Uninstall:"+slug)
	p, ok := f.plugins[slug]
	if !ok {
		return lifecycle.ErrNotFound
	}
	if p.State != lifecycle.StateInactive && p.State != lifecycle.StateErrored {
		return fmt.Errorf("%w: cannot uninstall from %s", lifecycle.ErrInvalidTransition, p.State)
	}
	delete(f.plugins, slug)
	return nil
}

// recordedCalls returns a snapshot of the call log so tests can
// assert specific lifecycle methods fired.
func (f *fakeManager) recordedCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// harness bundles the handler mux + fake manager so each test has its
// own isolated state. The discard logger keeps test output clean.
type harness struct {
	mux *http.ServeMux
	mgr *fakeManager
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	mgr := newFakeManager()
	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1/plugins", Deps{
		Manager: mgr,
		Policy:  policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return &harness{mux: mux, mgr: mgr}
}

func (h *harness) do(req *http.Request, pr *policy.Principal) *httptest.ResponseRecorder {
	if pr != nil {
		req = req.WithContext(policy.WithPrincipal(req.Context(), *pr))
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func adminPrincipal() policy.Principal {
	return policy.Principal{
		UserID: "11111111-1111-1111-1111-111111111111",
		Roles:  []policy.Role{policy.RoleAdmin},
	}
}

func subscriberPrincipal() policy.Principal {
	return policy.Principal{
		UserID: "22222222-2222-2222-2222-222222222222",
		Roles:  []policy.Role{policy.RoleSubscriber},
	}
}

// TestList_EmptyReturnsPagedShape covers the unblock scenario for the
// admin Plugins page: a clean install (no plugins) must return 200 with
// the {data: [], pagination: {...}} envelope, not 404. The Page[T]
// MarshalJSON guarantees Data is `[]` rather than `null` (issue #505).
func TestList_EmptyReturnsPagedShape(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/plugins", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	// Parse the raw body so we can assert "data":[] rather than the
	// Go zero-value (nil) decoding ambiguity.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode body: %v (body=%s)", err, rec.Body.String())
	}
	data, ok := raw["data"]
	if !ok {
		t.Fatalf("response missing data key: %s", rec.Body.String())
	}
	if string(data) != "[]" {
		t.Fatalf("expected data=[], got %s", string(data))
	}
	if _, ok := raw["pagination"]; !ok {
		t.Fatalf("response missing pagination key: %s", rec.Body.String())
	}
}

// TestList_ReturnsSeededPlugins confirms the projection from
// lifecycle.Plugin onto the wire shape (name<-slug, capabilities
// nil-coerce, etc.).
func TestList_ReturnsSeededPlugins(t *testing.T) {
	h := newHarness(t)
	now := time.Now().UTC().Truncate(time.Second)
	h.mgr.seed(lifecycle.Plugin{
		Slug:         "demo-plugin",
		Version:      "0.1.0",
		ABIVersion:   1,
		State:        lifecycle.StateActive,
		Capabilities: []string{"kv.read", "audit.emit"},
		InstalledAt:  now,
		ActivatedAt:  now,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/plugins", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var page router.Page[PluginRecord]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if len(page.Data) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(page.Data))
	}
	got := page.Data[0]
	if got.Name != "demo-plugin" {
		t.Errorf("Name: got %q, want demo-plugin", got.Name)
	}
	if got.State != "active" {
		t.Errorf("State: got %q, want active", got.State)
	}
	if got.Version != "0.1.0" {
		t.Errorf("Version: got %q, want 0.1.0", got.Version)
	}
	if len(got.Capabilities) != 2 {
		t.Errorf("Capabilities: got %v, want 2 entries", got.Capabilities)
	}
}

// TestList_Unauthenticated returns 401 when no Principal is on the
// context. Mirrors how RequireSession would refuse a logged-out caller.
func TestList_Unauthenticated(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/plugins", nil)
	rec := h.do(req, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestGet_UnknownReturns404 covers the typed ErrNotFound path.
func TestGet_UnknownReturns404(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/plugins/nope", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestActivate_Unauthenticated returns 401 for an anonymous caller.
func TestActivate_Unauthenticated(t *testing.T) {
	h := newHarness(t)
	h.mgr.seed(lifecycle.Plugin{Slug: "demo-plugin", State: lifecycle.StateInstalled})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/demo-plugin/activate", nil)
	rec := h.do(req, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestActivate_Forbidden returns 403 for a logged-in caller without
// the CapActivatePlugins capability.
func TestActivate_Forbidden(t *testing.T) {
	h := newHarness(t)
	h.mgr.seed(lifecycle.Plugin{Slug: "demo-plugin", State: lifecycle.StateInstalled})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/demo-plugin/activate", nil)
	pr := subscriberPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestActivate_Succeeds drives a seeded Installed plugin to Active and
// verifies the response carries the post-transition state. The fake
// manager records each lifecycle call so we can assert the right
// method fired.
func TestActivate_Succeeds(t *testing.T) {
	h := newHarness(t)
	h.mgr.seed(lifecycle.Plugin{
		Slug:    "demo-plugin",
		Version: "0.1.0",
		State:   lifecycle.StateInstalled,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/demo-plugin/activate", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var rec1 PluginRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &rec1); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if rec1.State != string(lifecycle.StateActive) {
		t.Errorf("State after activate: got %q, want active", rec1.State)
	}

	calls := h.mgr.recordedCalls()
	var sawActivate bool
	for _, c := range calls {
		if c == "Activate:demo-plugin" {
			sawActivate = true
			break
		}
	}
	if !sawActivate {
		t.Fatalf("Activate was not called; calls=%v", calls)
	}
}

// TestActivate_InvalidTransitionReturns409 covers the case where the
// caller asks to activate a plugin that's already active. The
// lifecycle Manager surfaces ErrInvalidTransition; the handler maps
// that to a 409 so the admin can show a clear "already active" hint.
func TestActivate_InvalidTransitionReturns409(t *testing.T) {
	h := newHarness(t)
	h.mgr.seed(lifecycle.Plugin{
		Slug:  "demo-plugin",
		State: lifecycle.StateActive,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/demo-plugin/activate", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestDeactivate_Succeeds drives Active → Inactive.
func TestDeactivate_Succeeds(t *testing.T) {
	h := newHarness(t)
	h.mgr.seed(lifecycle.Plugin{
		Slug:  "demo-plugin",
		State: lifecycle.StateActive,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/demo-plugin/deactivate", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var rec1 PluginRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &rec1); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rec1.State != string(lifecycle.StateInactive) {
		t.Errorf("State after deactivate: got %q, want inactive", rec1.State)
	}
}

// TestUninstall_Succeeds drives Inactive → deleted.
func TestUninstall_Succeeds(t *testing.T) {
	h := newHarness(t)
	h.mgr.seed(lifecycle.Plugin{
		Slug:  "demo-plugin",
		State: lifecycle.StateInactive,
	})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/plugins/demo-plugin", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	// Subsequent GET should now 404.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/plugins/demo-plugin", nil)
	getRec := h.do(getReq, &pr)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("post-uninstall GET expected 404, got %d", getRec.Code)
	}
}

// TestUninstall_Forbidden covers the cap gate: a subscriber cannot
// remove a plugin.
func TestUninstall_Forbidden(t *testing.T) {
	h := newHarness(t)
	h.mgr.seed(lifecycle.Plugin{Slug: "demo-plugin", State: lifecycle.StateInactive})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/plugins/demo-plugin", nil)
	pr := subscriberPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestInstall_RejectsNonMultipart confirms the content-type gate. A
// JSON body or empty body should produce 400 rather than reaching the
// lifecycle Manager.
func TestInstall_RejectsNonMultipart(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/install", nil)
	req.Header.Set("Content-Type", "application/json")
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestInstall_Forbidden covers the cap gate on the install endpoint.
func TestInstall_Forbidden(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/install", nil)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=zzz")
	pr := subscriberPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// Verifies the manager's error propagation: the install handler
// surfaces an unwrapped lifecycle error as a 400 with the message in
// the detail field so the admin UI can render it.
func TestInstall_ManagerErrorReturns400(t *testing.T) {
	h := newHarness(t)
	h.mgr.installErr = errors.New("manifest version is required")

	// Build a minimal multipart body with a bundle part.
	body, contentType := makeMultipart(t, map[string][]byte{"bundle": []byte("not-a-zip")})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/install", body)
	req.Header.Set("Content-Type", contentType)
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// makeMultipart builds a tiny multipart/form-data body for the install
// tests. Each entry becomes one part with FormName = key, body = value.
func makeMultipart(t *testing.T, parts map[string][]byte) (io.Reader, string) {
	t.Helper()
	pr, pw := io.Pipe()
	mw := newMultipartWriter(pw)
	go func() {
		defer func() { _ = pw.Close() }()
		for name, body := range parts {
			if err := mw.writePart(name, body); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
		}
		_ = mw.close()
	}()
	return pr, "multipart/form-data; boundary=" + mw.boundary
}

// Tiny in-test multipart writer. We don't pull in mime/multipart's
// FormDataWriter because we want a stable boundary string for the
// boundary check above and because the standard helper writes a
// trailing CRLF the test doesn't need.
type multipartWriter struct {
	w        io.Writer
	boundary string
}

func newMultipartWriter(w io.Writer) *multipartWriter {
	return &multipartWriter{w: w, boundary: "test-boundary-zzz"}
}

func (m *multipartWriter) writePart(name string, body []byte) error {
	header := "--" + m.boundary + "\r\n" +
		`Content-Disposition: form-data; name="` + name + `"; filename="` + name + `.bin"` + "\r\n" +
		"Content-Type: application/octet-stream\r\n\r\n"
	if _, err := io.WriteString(m.w, header); err != nil {
		return err
	}
	if _, err := m.w.Write(body); err != nil {
		return err
	}
	_, err := io.WriteString(m.w, "\r\n")
	return err
}

func (m *multipartWriter) close() error {
	_, err := io.WriteString(m.w, "--"+m.boundary+"--\r\n")
	return err
}
