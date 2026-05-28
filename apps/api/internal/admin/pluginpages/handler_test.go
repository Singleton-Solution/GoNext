package pluginpages

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/lifecycle"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

type fakeLister struct {
	plugins []lifecycle.Plugin
}

func (f *fakeLister) List(_ context.Context) ([]lifecycle.Plugin, error) {
	return f.plugins, nil
}

func newHarness(t *testing.T, plugins []lifecycle.Plugin) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1/admin/plugin-pages", Deps{
		Manager: &fakeLister{plugins: plugins},
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return mux
}

func makeManifest(slug string, pages []map[string]string) json.RawMessage {
	body := map[string]any{
		"apiVersion": "gonext.io/v1",
		"name":       slug,
		"version":    "0.0.1",
		"entry":      "plugin.wasm",
	}
	if len(pages) > 0 {
		ps := make([]map[string]string, len(pages))
		copy(ps, pages)
		body["admin_pages"] = ps
	}
	b, _ := json.Marshal(body)
	return b
}

func TestList_OnlyActivePlugins(t *testing.T) {
	plugins := []lifecycle.Plugin{
		{
			Slug:     "active-plugin",
			State:    lifecycle.StateActive,
			Manifest: makeManifest("active-plugin", []map[string]string{{"slug": "dash", "label": "Dashboard"}}),
		},
		{
			Slug:     "inactive-plugin",
			State:    lifecycle.StateInactive,
			Manifest: makeManifest("inactive-plugin", []map[string]string{{"slug": "dash", "label": "Should not appear"}}),
		},
	}
	mux := newHarness(t, plugins)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/plugin-pages", nil)
	req = req.WithContext(policy.WithPrincipal(req.Context(), policy.Principal{
		UserID: "u:1", Roles: []policy.Role{policy.RoleAdmin},
	}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp listResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Pages) != 1 {
		t.Fatalf("expected 1 page, got %d: %+v", len(resp.Pages), resp.Pages)
	}
	if resp.Pages[0].Plugin != "active-plugin" || resp.Pages[0].Slug != "dash" {
		t.Fatalf("unexpected page: %+v", resp.Pages[0])
	}
}

func TestList_FlattensAcrossPlugins(t *testing.T) {
	plugins := []lifecycle.Plugin{
		{
			Slug:     "a-plugin",
			State:    lifecycle.StateActive,
			Manifest: makeManifest("a-plugin", []map[string]string{{"slug": "x", "label": "X"}, {"slug": "y", "label": "Y"}}),
		},
		{
			Slug:     "b-plugin",
			State:    lifecycle.StateActive,
			Manifest: makeManifest("b-plugin", []map[string]string{{"slug": "x", "label": "BX"}}),
		},
	}
	mux := newHarness(t, plugins)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/plugin-pages", nil)
	req = req.WithContext(policy.WithPrincipal(req.Context(), policy.Principal{UserID: "u:1"}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var resp listResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Pages) != 3 {
		t.Fatalf("expected 3 pages, got %d: %+v", len(resp.Pages), resp.Pages)
	}
	// Sort guarantee: a-plugin/x, a-plugin/y, b-plugin/x.
	if resp.Pages[0].Plugin != "a-plugin" || resp.Pages[2].Plugin != "b-plugin" {
		t.Fatalf("unexpected ordering: %+v", resp.Pages)
	}
}

func TestList_AnonymousDenied(t *testing.T) {
	mux := newHarness(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/plugin-pages", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// Clean install: no plugins are installed yet, so the underlying
// lifecycle.List call returns an empty slice. The sidebar polls this
// endpoint on every authenticated layout render; it must respond 200
// with {"pages":[]} rather than failing the layout. Regression for
// issue #503.
func TestList_EmptyOnCleanInstall(t *testing.T) {
	mux := newHarness(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/plugin-pages", nil)
	req = req.WithContext(policy.WithPrincipal(req.Context(), policy.Principal{UserID: "u:1"}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp listResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Pages == nil {
		t.Fatal("Pages should be a non-nil empty slice so JSON encodes as [], not null")
	}
	if len(resp.Pages) != 0 {
		t.Fatalf("expected zero pages, got %d: %+v", len(resp.Pages), resp.Pages)
	}
}
