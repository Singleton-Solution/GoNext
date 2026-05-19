package site_editor

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// maxOverrideBytes caps the request body for an override write. The
// limit is generous (1 MiB) — a part with a few dozen blocks
// serialises to well under 100 KiB, even with image data URIs inline.
// The cap exists to bound a hostile or buggy client; it is NOT a
// "what's reasonable" line.
const maxOverrideBytes = 1 << 20

// overrideRequest is the PUT body. We accept a bare object with a
// "blocks" field rather than a top-level array so adding sibling
// fields later (a hash for optimistic concurrency, an editor
// version stamp) doesn't break the wire shape.
type overrideRequest struct {
	Blocks BlockTree `json:"blocks"`
}

// overrideResponse echoes the saved tree back so the editor's
// autosave hook can fold "what landed on the server" into its
// last-saved snapshot without an extra round trip.
type overrideResponse struct {
	Name       string    `json:"name"`
	Theme      string    `json:"theme"`
	Blocks     BlockTree `json:"blocks"`
	Overridden bool      `json:"overridden"`
}

// put handles PUT /api/v1/admin/site_editor/parts/{name}.
//
// The handler:
//
//  1. Reads + size-limits the request body.
//  2. Decodes the BlockTree.
//  3. Validates that every block name resolves to a registered block
//     — a tree the renderer can't render would lock the operator out
//     of fixing the part via the very UI they used to break it.
//  4. Persists via the OverrideStore.
//
// On success returns 200 with the echoed tree + theme + name.
func (h *Handler) put(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	name := r.PathValue("name")
	if name == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_name", "part name is required")
		return
	}

	theme, err := h.source.ActiveTheme(r.Context())
	if err != nil {
		h.writeSourceError(w, r, err, "admin/site_editor.put")
		return
	}

	// Verify the part is part of the theme's declared inventory.
	// Saving an override for an undeclared part would create a row the
	// renderer never looks up — a write-only graveyard.
	meta, err := h.source.List(r.Context())
	if err != nil {
		h.writeSourceError(w, r, err, "admin/site_editor.put")
		return
	}
	if !hasMeta(meta, name) {
		router.WriteError(w, http.StatusNotFound, "part_not_found",
			"the requested part is not declared by the active theme")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxOverrideBytes+1))
	if err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_body", "failed to read request body")
		return
	}
	if int64(len(body)) > maxOverrideBytes {
		router.WriteError(w, http.StatusRequestEntityTooLarge, "body_too_large",
			"override payload exceeds 1 MiB limit")
		return
	}

	var req overrideRequest
	if err := json.Unmarshal(body, &req); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}
	if req.Blocks == nil {
		// Distinguish "missing field" from "explicit empty array" by
		// re-parsing into a generic map. An explicit `{"blocks": []}`
		// is legitimate ("clear the part to empty"); a `{}` is not.
		var generic map[string]json.RawMessage
		if jerr := json.Unmarshal(body, &generic); jerr == nil {
			if _, present := generic["blocks"]; !present {
				router.WriteError(w, http.StatusBadRequest, "missing_blocks",
					"request body must include a `blocks` field")
				return
			}
		}
		req.Blocks = BlockTree{}
	}

	if err := h.validator.Validate(req.Blocks); err != nil {
		// Validation errors are operator-visible: the editor surfaces
		// the offending block name + path so the user sees what went
		// wrong. Don't log noisily — this is a 4xx, not a 5xx.
		router.WriteError(w, http.StatusUnprocessableEntity, "invalid_block_tree", err.Error())
		return
	}

	if err := h.overrides.Put(r.Context(), theme, name, req.Blocks); err != nil {
		h.logger.ErrorContext(r.Context(), "admin/site_editor.put: store write failed",
			slog.String("theme", theme),
			slog.String("part", name),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error",
			"failed to persist override")
		return
	}

	// Override changed — drop the cached on-disk parse so a follow-up
	// read of the disk path (e.g. after a Reset) sees a fresh parse.
	// The override read path doesn't go through the cache, so this is
	// belt-and-braces only.
	h.resolver.InvalidateDisk(theme, name)

	router.WriteJSON(w, http.StatusOK, overrideResponse{
		Name:       name,
		Theme:      theme,
		Blocks:     req.Blocks,
		Overridden: true,
	})
}

// nameValidation surfaces a 400 for the small set of "this can't be a
// part name" cases. We do NOT consult the theme.json declaration here
// — that's the per-handler responsibility (put + delete each scope
// the check to "is this part declared by the active theme?").
func validatePartName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	for _, ch := range name {
		switch {
		case ch >= 'a' && ch <= 'z':
		case ch >= '0' && ch <= '9':
		case ch == '-' || ch == '_':
		default:
			return errors.New("name must be lower-case alphanumeric with '-' or '_'")
		}
	}
	return nil
}
