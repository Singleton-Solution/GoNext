package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/config"
)

// testLogger discards log output. Tests don't need to assert on log
// lines; the router behaviour is what we're verifying.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestBuildRouter_DevPluginEndpointNotRegisteredWhenDevModeOff verifies
// the registration-time gate on the dev install endpoint. The route
// MUST NOT be present in the resulting mux when Plugins.DevMode is
// false — this is the strongest guarantee prod can have, since the
// route literally doesn't exist on the http.ServeMux to respond to.
//
// We pass nil for pool and rdb because buildRouter doesn't dereference
// them for the routes we exercise here (the readiness probe is the
// only consumer and it isn't on the path we hit).
func TestBuildRouter_DevPluginEndpointNotRegisteredWhenDevModeOff(t *testing.T) {
	cfg := &config.Config{
		Plugins: config.PluginsConfig{DevMode: false},
	}
	router := buildRouter(cfg, nil, nil, testLogger())

	req := httptest.NewRequest(http.MethodPost, "/_/plugins/dev/install", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 when DevMode is off, got %d (body: %s)",
			rr.Code, rr.Body.String())
	}
}

// TestBuildRouter_DevPluginEndpointRegisteredWhenDevModeOn checks the
// other side of the conditional: with DevMode on, the route exists
// (even if it rejects the request for missing auth or shape — we just
// need to confirm it's no longer 404).
func TestBuildRouter_DevPluginEndpointRegisteredWhenDevModeOn(t *testing.T) {
	cfg := &config.Config{
		Plugins: config.PluginsConfig{DevMode: true, DevToken: "test-token-123"},
	}
	router := buildRouter(cfg, nil, nil, testLogger())

	req := httptest.NewRequest(http.MethodPost, "/_/plugins/dev/install", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code == http.StatusNotFound {
		t.Errorf("expected route to be registered when DevMode is on, got 404")
	}
}
