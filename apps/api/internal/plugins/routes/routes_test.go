package routes

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	hostbus "github.com/Singleton-Solution/GoNext/packages/go/hooks"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/capabilities"
)

// stubDispatcher implements Dispatcher and lets each test plant a
// canned response or error.
type stubDispatcher struct {
	resp []byte
	err  error
	// lastPayload captures the last marshalled Request envelope seen
	// by Dispatch — used for assertions about what the host forwarded
	// to the plugin.
	lastPayload []byte
	lastSlug    string
}

func (s *stubDispatcher) Dispatch(_ context.Context, slug string, payload []byte) ([]byte, error) {
	s.lastSlug = slug
	s.lastPayload = payload
	return s.resp, s.err
}

// newRegForTest builds a Registry wired to in-memory deps, returning
// the mux for inbound traffic, the dispatcher stub for assertion, and
// the registry itself.
func newRegForTest(t *testing.T) (*Registry, *http.ServeMux, *stubDispatcher, *audit.MemoryStore) {
	t.Helper()
	mux := http.NewServeMux()
	store := audit.NewMemoryStore()
	em := audit.NewEmitter(store)
	disp := &stubDispatcher{}
	reg, err := NewRegistry(Options{
		Mux:        mux,
		Dispatcher: disp,
		Emitter:    em,
		CapReg:     capabilities.Default(),
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return reg, mux, disp, store
}

// TestRoutes_RegisterRequiresCapability verifies a plugin without the
// http.serve grant is refused at Register.
func TestRoutes_RegisterRequiresCapability(t *testing.T) {
	reg, _, _, _ := newRegForTest(t)
	err := reg.Register("seo", []RouteSpec{{Method: "GET", Path: "/x"}},
		capabilities.NewGrantSet())
	if err == nil || !strings.Contains(err.Error(), "http.serve") {
		t.Errorf("expected http.serve denial, got %v", err)
	}
}

// TestRoutes_MountAndDispatch verifies the full happy path: register,
// inbound request, plugin dispatch, response written.
func TestRoutes_MountAndDispatch(t *testing.T) {
	reg, mux, disp, _ := newRegForTest(t)
	if err := reg.Register("seo",
		[]RouteSpec{{Method: "GET", Path: "/hello"}},
		capabilities.NewGrantSet("http.serve")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Plant a canned plugin response.
	planned, _ := json.Marshal(Response{
		Status: http.StatusOK,
		Headers: map[string]string{
			"Content-Type": "text/plain",
			"X-Plugin":     "seo",
		},
		Body: []byte("hello from plugin"),
	})
	disp.resp = planned

	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/api/plugins/seo/hello", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4") // should be stripped
	req.Header.Set("Authorization", "Bearer x")  // should pass through
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Plugin"); got != "seo" {
		t.Errorf("X-Plugin: got %q", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello from plugin" {
		t.Errorf("body: got %q", body)
	}

	// Verify the dispatcher saw the right slug + an envelope that
	// includes the allowed header and excludes the forwarded one.
	if disp.lastSlug != "seo" {
		t.Errorf("dispatcher slug: got %q", disp.lastSlug)
	}
	var envelope Request
	if err := json.Unmarshal(disp.lastPayload, &envelope); err != nil {
		t.Fatalf("Unmarshal envelope: %v", err)
	}
	if envelope.Headers["Authorization"] != "Bearer x" {
		t.Errorf("Authorization header should pass through, got %q", envelope.Headers["Authorization"])
	}
	if _, ok := envelope.Headers["X-Forwarded-For"]; ok {
		t.Errorf("X-Forwarded-For should be stripped")
	}
}

// TestRoutes_OutboundHeaderDenylist verifies the plugin cannot set
// Set-Cookie, Strict-Transport-Security, etc.
func TestRoutes_OutboundHeaderDenylist(t *testing.T) {
	reg, mux, disp, _ := newRegForTest(t)
	_ = reg.Register("seo",
		[]RouteSpec{{Method: "GET", Path: "/hello"}},
		capabilities.NewGrantSet("http.serve"))
	planned, _ := json.Marshal(Response{
		Status: http.StatusOK,
		Headers: map[string]string{
			"Content-Type":              "text/plain",
			"Set-Cookie":                "session=abc",          // denied
			"Strict-Transport-Security": "max-age=31536000",     // denied
			"Content-Security-Policy":   "default-src 'self'",   // denied
			"X-Plugin-Note":             "ok",                   // allowed
		},
		Body: []byte("hi"),
	})
	disp.resp = planned

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/plugins/seo/hello")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	for _, denied := range []string{"Set-Cookie", "Strict-Transport-Security", "Content-Security-Policy"} {
		if got := resp.Header.Get(denied); got != "" {
			t.Errorf("header %q should be stripped, got %q", denied, got)
		}
	}
	if resp.Header.Get("X-Plugin-Note") != "ok" {
		t.Errorf("X-Plugin-Note should pass through")
	}
}

// TestRoutes_UnregisterShadowDisable verifies that Unregister causes
// subsequent requests to return 404 even though the http.ServeMux
// pattern remains mounted.
func TestRoutes_UnregisterShadowDisable(t *testing.T) {
	reg, mux, disp, _ := newRegForTest(t)
	_ = reg.Register("seo",
		[]RouteSpec{{Method: "GET", Path: "/hello"}},
		capabilities.NewGrantSet("http.serve"))
	planned, _ := json.Marshal(Response{Status: 200, Body: []byte("ok")})
	disp.resp = planned

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/api/plugins/seo/hello")
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("before unregister: got %d want 200", resp.StatusCode)
	}

	reg.Unregister("seo")
	resp, _ = http.Get(srv.URL + "/api/plugins/seo/hello")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("after unregister: got %d want 404", resp.StatusCode)
	}
}

// TestRoutes_ReregisterReplacesSpecs verifies a second Register call
// replaces the spec set: routes in the new manifest serve as expected;
// routes ONLY in the old manifest 404.
func TestRoutes_ReregisterReplacesSpecs(t *testing.T) {
	reg, mux, disp, _ := newRegForTest(t)
	_ = reg.Register("seo",
		[]RouteSpec{
			{Method: "GET", Path: "/v1"},
			{Method: "GET", Path: "/v2"},
		},
		capabilities.NewGrantSet("http.serve"))
	planned, _ := json.Marshal(Response{Status: 200, Body: []byte("ok")})
	disp.resp = planned

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/api/plugins/seo/v1")
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("v1 before reregister: got %d", resp.StatusCode)
	}

	// Re-register: drop /v1, add /v3.
	_ = reg.Register("seo",
		[]RouteSpec{
			{Method: "GET", Path: "/v2"},
			{Method: "GET", Path: "/v3"},
		},
		capabilities.NewGrantSet("http.serve"))

	// /v1 should now 404 (pattern still mounted but spec absent).
	resp, _ = http.Get(srv.URL + "/api/plugins/seo/v1")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("v1 after reregister: got %d want 404", resp.StatusCode)
	}
	// /v3 should serve.
	resp, _ = http.Get(srv.URL + "/api/plugins/seo/v3")
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("v3 after reregister: got %d", resp.StatusCode)
	}
}

// TestRoutes_NoHandler404 verifies the dispatcher's ErrNoHandler maps
// to 404 at the HTTP surface.
func TestRoutes_NoHandler404(t *testing.T) {
	reg, mux, disp, _ := newRegForTest(t)
	_ = reg.Register("seo",
		[]RouteSpec{{Method: "GET", Path: "/x"}},
		capabilities.NewGrantSet("http.serve"))
	disp.err = ErrNoHandler

	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/api/plugins/seo/x")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("no-handler: got %d want 404", resp.StatusCode)
	}
}

// TestRoutes_DispatchErrorIsBadGateway verifies any other dispatcher
// error surfaces as 502.
func TestRoutes_DispatchErrorIsBadGateway(t *testing.T) {
	reg, mux, disp, _ := newRegForTest(t)
	_ = reg.Register("seo",
		[]RouteSpec{{Method: "GET", Path: "/x"}},
		capabilities.NewGrantSet("http.serve"))
	disp.err = errors.New("plugin boom")

	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/api/plugins/seo/x")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("dispatch error: got %d want 502", resp.StatusCode)
	}
}

// TestRoutes_RouteValidation rejects malformed specs.
func TestRoutes_RouteValidation(t *testing.T) {
	reg, _, _, _ := newRegForTest(t)
	cases := []RouteSpec{
		{Method: "", Path: "/x"},
		{Method: "PROBE", Path: "/x"},
		{Method: "GET", Path: "x"}, // missing leading /
		{Method: "GET", Path: ""},
	}
	for _, c := range cases {
		err := reg.Register("seo", []RouteSpec{c}, capabilities.NewGrantSet("http.serve"))
		if err == nil {
			t.Errorf("spec %+v should fail validation", c)
		}
	}
}

// TestRoutes_HookBusDispatch_NoHandler verifies that the bus
// dispatcher synthesises ErrNoHandler when the bus returns the input
// unchanged (no handler subscribed).
func TestRoutes_HookBusDispatch_NoHandler(t *testing.T) {
	bus := hostbus.NewBus()
	d := NewHookBusDispatcher(bus)
	in := []byte(`{"method":"GET","path":"/x"}`)
	_, err := d.Dispatch(context.Background(), "seo", in)
	if !errors.Is(err, ErrNoHandler) {
		t.Errorf("expected ErrNoHandler, got %v", err)
	}
}

// TestRoutes_HookBusDispatch_HandlerReturns verifies the bus
// dispatcher returns the handler's transformed payload.
func TestRoutes_HookBusDispatch_HandlerReturns(t *testing.T) {
	bus := hostbus.NewBus()
	unsubscribe := bus.RegisterFilter("http.serve.seo", 10, func(_ context.Context, value any, _ ...any) (any, error) {
		// Echo back a stock response envelope.
		return json.RawMessage(`{"status":200,"body":"aGk="}`), nil
	})
	defer unsubscribe()

	d := NewHookBusDispatcher(bus)
	out, err := d.Dispatch(context.Background(), "seo", []byte(`{"method":"GET"}`))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !strings.Contains(string(out), `"status":200`) {
		t.Errorf("expected handler response, got %q", out)
	}
}

// TestRoutes_OversizedBodyRejected ensures a request body over the
// cap is refused with 413.
func TestRoutes_OversizedBodyRejected(t *testing.T) {
	reg, mux, disp, _ := newRegForTest(t)
	_ = reg.Register("seo",
		[]RouteSpec{{Method: "POST", Path: "/big"}},
		capabilities.NewGrantSet("http.serve"))
	planned, _ := json.Marshal(Response{Status: 200})
	disp.resp = planned

	srv := httptest.NewServer(mux)
	defer srv.Close()

	huge := strings.Repeat("a", maxRequestBodyBytes+1024)
	resp, _ := http.Post(srv.URL+"/api/plugins/seo/big", "text/plain", strings.NewReader(huge))
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized: got %d want 413", resp.StatusCode)
	}
}

// TestRoutes_AuditEmission verifies every dispatch results in one
// audit row.
func TestRoutes_AuditEmission(t *testing.T) {
	reg, mux, disp, store := newRegForTest(t)
	_ = reg.Register("seo",
		[]RouteSpec{{Method: "GET", Path: "/x"}},
		capabilities.NewGrantSet("http.serve"))
	planned, _ := json.Marshal(Response{Status: 201, Body: []byte("ok")})
	disp.resp = planned

	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/api/plugins/seo/x")
	resp.Body.Close()

	evts, err := store.List(context.Background(), audit.Filter{Limit: 5})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(evts) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(evts))
	}
	if evts[0].EventType != "plugin.http.serve" {
		t.Errorf("event type: got %q", evts[0].EventType)
	}
}

// TestRoutes_NewRegistry_RequiredFields verifies the constructor
// validates its inputs.
func TestRoutes_NewRegistry_RequiredFields(t *testing.T) {
	cases := []Options{
		{}, // all nil
		{Mux: http.NewServeMux()},
		{Mux: http.NewServeMux(), Dispatcher: &stubDispatcher{}},
		{Mux: http.NewServeMux(), Dispatcher: &stubDispatcher{}, Emitter: audit.NewEmitter(audit.NewMemoryStore())},
	}
	for i, c := range cases {
		if _, err := NewRegistry(c); err == nil {
			t.Errorf("case %d: expected error for incomplete opts", i)
		}
	}
}
