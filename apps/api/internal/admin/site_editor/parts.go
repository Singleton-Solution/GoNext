package site_editor

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// listResponse is the GET /parts payload. Wrapping the slice in an
// object (rather than returning a bare array) leaves room for future
// fields — total count, theme metadata, capability echo — without an
// API break.
type listResponse struct {
	Theme string `json:"theme"`
	Parts []Part `json:"parts"`
}

// list handles GET /api/v1/admin/site_editor/parts.
//
// For every declared part the handler resolves the BlockTree via the
// Resolver: override-first, on-disk fallback. The Overridden flag in
// the response tells the UI whether to render the "Modified" badge.
//
// Capability: theme.edit_parts (already enforced by gate; this
// handler does not re-check).
func (h *Handler) list(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	theme, err := h.source.ActiveTheme(r.Context())
	if err != nil {
		h.writeSourceError(w, r, err, "admin/site_editor.list")
		return
	}

	meta, err := h.source.List(r.Context())
	if err != nil {
		h.writeSourceError(w, r, err, "admin/site_editor.list")
		return
	}

	out := make([]Part, 0, len(meta))
	for _, m := range meta {
		tree, overridden, err := h.resolver.Resolve(r.Context(), theme, m.Name)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "admin/site_editor.list: resolve failed",
				slog.String("theme", theme),
				slog.String("part", m.Name),
				slog.Any("err", err),
			)
			// One bad part shouldn't blank out the rest of the sidebar.
			// Surface an empty tree + overridden=false so the editor
			// opens on a blank canvas the operator can re-author.
			tree = BlockTree{}
			overridden = false
		}
		title := m.Title
		if title == "" {
			title = m.Name
		}
		out = append(out, Part{
			Name:       m.Name,
			Title:      title,
			Area:       m.Area,
			Blocks:     tree,
			Overridden: overridden,
		})
	}

	router.WriteJSON(w, http.StatusOK, listResponse{Theme: theme, Parts: out})
}

// writeSourceError translates the small set of PartsSource sentinel
// errors into HTTP responses. Anything we don't recognise is treated
// as internal and the original is logged for the operator to chase.
func (h *Handler) writeSourceError(w http.ResponseWriter, r *http.Request, err error, tag string) {
	switch {
	case errors.Is(err, ErrNoActiveTheme):
		router.WriteError(w, http.StatusServiceUnavailable, "no_active_theme",
			"no theme is currently active; run the installer or set core.active_theme")
	case errors.Is(err, ErrPartNotFound):
		router.WriteError(w, http.StatusNotFound, "part_not_found",
			"the requested part is not declared by the active theme")
	default:
		h.logger.ErrorContext(r.Context(), tag+": source error", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error",
			"failed to load template parts")
	}
}
