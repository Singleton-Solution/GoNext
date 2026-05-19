package site_editor

import (
	"log/slog"
	"net/http"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// delete handles DELETE /api/v1/admin/site_editor/parts/{name}.
//
// Removes the operator-saved override so the next read falls back to
// the on-disk part. The operation is idempotent: deleting a part that
// has no override returns 204 just the same as deleting one that did.
//
// Capability: theme.edit_parts.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	name := r.PathValue("name")
	if err := validatePartName(name); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_name", err.Error())
		return
	}

	theme, err := h.source.ActiveTheme(r.Context())
	if err != nil {
		h.writeSourceError(w, r, err, "admin/site_editor.delete")
		return
	}

	// Verify the part is declared. We could just delete the row
	// blindly and let the renderer fall back to disk, but if the
	// operator typed a wrong name (or the UI sent a stale path after
	// a theme switch), a 404 is more diagnostic than a silent 204.
	meta, err := h.source.List(r.Context())
	if err != nil {
		h.writeSourceError(w, r, err, "admin/site_editor.delete")
		return
	}
	if !hasMeta(meta, name) {
		router.WriteError(w, http.StatusNotFound, "part_not_found",
			"the requested part is not declared by the active theme")
		return
	}

	if err := h.overrides.Delete(r.Context(), theme, name); err != nil {
		h.logger.ErrorContext(r.Context(), "admin/site_editor.delete: store delete failed",
			slog.String("theme", theme),
			slog.String("part", name),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error",
			"failed to remove override")
		return
	}

	h.resolver.InvalidateDisk(theme, name)
	w.WriteHeader(http.StatusNoContent)
}
