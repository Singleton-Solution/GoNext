// Package pluginpages exposes the active plugins' admin_pages
// declarations to the admin shell. Issue #228.
//
// Route (mounted under base, typically /api/v1/admin/plugin-pages):
//
//	GET {base} — flat list of {plugin, slug, label, icon, capability}
//
// The admin sidebar fetches this once per page load, filters by the
// current viewer's capabilities, and renders one Sidebar entry under
// the "Plugins" section per declared page. The plugin's frontend
// host (under /plugins/{plugin}/{slug}) is responsible for the page
// itself; this surface only exposes the manifest declarations.
package pluginpages

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/lifecycle"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/manifest"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// PluginLister is the narrow [lifecycle.Manager] surface this package
// uses. Implementations typically wrap the production Manager; tests
// pass an inline closure.
type PluginLister interface {
	List(ctx context.Context) ([]lifecycle.Plugin, error)
}

// Deps is the dependency bag for [Mount].
type Deps struct {
	Manager PluginLister
	Logger  *slog.Logger
}

func (d Deps) validate() error {
	if d.Manager == nil {
		return errors.New("pluginpages: Deps.Manager is required")
	}
	return nil
}

// PageView is the per-page wire shape returned by the list endpoint.
// One PageView per AdminPage declaration in every active plugin's
// manifest.
type PageView struct {
	Plugin     string `json:"plugin"`
	Slug       string `json:"slug"`
	Label      string `json:"label"`
	Icon       string `json:"icon,omitempty"`
	Capability string `json:"capability,omitempty"`
}

type listResponse struct {
	Pages []PageView `json:"pages"`
}

type handlers struct {
	mgr PluginLister
	log *slog.Logger
}

// Mount wires the route onto mux. base is typically
// "/api/v1/admin/plugin-pages".
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	h := &handlers{mgr: deps.Manager, log: deps.Logger}
	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base, http.HandlerFunc(h.list))
	return nil
}

// list walks every active plugin's manifest, pulls out the admin_pages
// list, and returns the flattened set. Pages are sorted alphabetically
// by (plugin, slug) for a stable wire ordering.
//
// Capability filtering: the response includes the declared
// `capability` per page; the admin shell filters client-side using
// the viewer's principal. This keeps the server surface principal-
// neutral (other clients — e.g. an OpenAPI explorer — see the full
// declared set).
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	p, ok := policy.FromContext(r.Context())
	if !ok || p.UserID == "" {
		router.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	plugins, err := h.mgr.List(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "pluginpages: list", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list plugins")
		return
	}
	out := make([]PageView, 0)
	for _, pl := range plugins {
		if pl.State != lifecycle.StateActive {
			continue
		}
		var m manifest.Manifest
		if err := json.Unmarshal(pl.Manifest, &m); err != nil {
			h.log.WarnContext(r.Context(), "pluginpages: skip plugin with unparseable manifest",
				slog.String("plugin", pl.Slug),
				slog.Any("err", err))
			continue
		}
		for _, page := range m.AdminPages {
			out = append(out, PageView{
				Plugin:     pl.Slug,
				Slug:       page.Slug,
				Label:      page.Label,
				Icon:       page.Icon,
				Capability: page.Capability,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Plugin != out[j].Plugin {
			return out[i].Plugin < out[j].Plugin
		}
		return out[i].Slug < out[j].Slug
	})
	router.WriteJSON(w, http.StatusOK, listResponse{Pages: out})
}
