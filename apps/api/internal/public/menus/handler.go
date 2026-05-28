// Package menus is the public, read-only REST surface for navigation
// menus — issue #509.
//
// Routes (mounted under base, typically /api/v1/menus):
//
//	GET {base}                          — list every configured menu
//	                                      (sitemap generators, rare).
//	GET {base}/by-location/{location}   — items for the menu whose slug
//	                                      matches {location}.
//
// Why a separate package from internal/admin/menus: the admin surface
// is mutating and gated by manage_themes. The public reader is the
// surface the unauthenticated marketing landing hits to paint its nav
// and footer columns — it must answer without a session.
//
// Empty/missing menu returns `{"items": []}` with 200, never 404. The
// rationale is that the public site falls back to a hardcoded default
// list when no items come back; treating "no menu configured" as a
// hard error would force every caller to special-case 404 to avoid
// rendering an empty nav.
package menus

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/menus"
)

// Deps is the dependency bag for [Mount].
type Deps struct {
	// Store is the read side of the menus persistence layer. The
	// public reader only calls ListMenus and GetWithItemsBySlug; any
	// implementation satisfying [menus.Store] works.
	Store menus.Store
	// Logger is optional; defaults to slog.Default().
	Logger *slog.Logger
}

func (d Deps) validate() error {
	if d.Store == nil {
		return errors.New("public/menus: Deps.Store is required")
	}
	return nil
}

type handlers struct {
	store  menus.Store
	logger *slog.Logger
}

// Mount wires the public menu routes onto mux. No policy gate: these
// endpoints serve unauthenticated visitors hitting the marketing
// landing.
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	h := &handlers{store: deps.Store, logger: deps.Logger}
	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base, http.HandlerFunc(h.list))
	mux.Handle("GET "+base+"/by-location/{location}", http.HandlerFunc(h.byLocation))
	return nil
}

// menuSummary is the trimmed shape returned by list — slug + name only.
// The public surface deliberately drops timestamps and attrs blobs to
// keep the payload tight; sitemap generators only need the slug.
type menuSummary struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// publicItem is the projection emitted to the public site. We don't
// surface the internal path ordering token, the menu_id, or the
// CMS-side object_type/object_id linkage — none of those are useful to
// the renderer, and trimming them keeps the surface area small.
type publicItem struct {
	Label    string `json:"label"`
	Href     string `json:"href"`
	External bool   `json:"external"`
}

// list answers GET /api/v1/menus with the configured menus. Empty
// store returns `{"menus": []}` with 200 — never 404, the public
// surface never fails closed.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	all, err := h.store.ListMenus(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "public/menus: list", slog.Any("err", err))
		// A store error is a server problem, not a client one — but
		// we keep the public surface forgiving: an empty list is a
		// safer signal to the renderer than a 500 that breaks the
		// page.
		router.WriteJSON(w, http.StatusOK, map[string]any{"menus": []menuSummary{}})
		return
	}
	out := make([]menuSummary, 0, len(all))
	for _, m := range all {
		out = append(out, menuSummary{Slug: m.Slug, Name: m.Name})
	}
	router.WriteJSON(w, http.StatusOK, map[string]any{"menus": out})
}

// byLocation answers GET /api/v1/menus/by-location/{location} with the
// items belonging to the menu whose slug matches {location}. The
// "location" name reflects how the public site uses these — "primary"
// for top nav, "footer-product" for a footer column, etc. — but
// underneath we just look up by slug.
//
// Missing menu, empty menu, and store error all return `{"items": []}`
// with 200; see the package doc for why.
func (h *handlers) byLocation(w http.ResponseWriter, r *http.Request) {
	location := strings.TrimSpace(r.PathValue("location"))
	if location == "" {
		// Empty path segment is a 200 with empty items, matching the
		// "never 404" contract.
		router.WriteJSON(w, http.StatusOK, map[string]any{"items": []publicItem{}})
		return
	}
	bundle, err := h.store.GetWithItemsBySlug(r.Context(), location)
	if err != nil {
		if !errors.Is(err, menus.ErrNotFound) {
			// Not-found is the expected path on a fresh install; only
			// log unexpected store failures.
			h.logger.ErrorContext(r.Context(), "public/menus: byLocation",
				slog.String("location", location),
				slog.Any("err", err),
			)
		}
		router.WriteJSON(w, http.StatusOK, map[string]any{"items": []publicItem{}})
		return
	}
	out := make([]publicItem, 0, len(bundle.Items))
	for _, mi := range bundle.Items {
		out = append(out, publicItem{
			Label:    mi.Label,
			Href:     mi.URL,
			External: isExternalURL(mi.URL),
		})
	}
	router.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

// isExternalURL classifies a menu item URL as external (points off
// this origin) or internal. The marketing renderer uses this to add
// `target="_blank" rel="noopener"` on external links.
//
// "External" here means "absolute with a scheme or scheme-relative".
// `mailto:` and `tel:` are treated as external because the renderer
// shouldn't add an internal Link wrapper to those.
func isExternalURL(u string) bool {
	u = strings.TrimSpace(u)
	if u == "" {
		return false
	}
	if strings.HasPrefix(u, "//") {
		return true
	}
	if i := strings.Index(u, ":"); i > 0 {
		scheme := u[:i]
		// Only treat as a scheme if it looks like one — lowercase
		// ASCII letters/digits, no path-style characters.
		for _, c := range scheme {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.' {
				continue
			}
			return false
		}
		return true
	}
	return false
}
