package comments

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// bulkRequest is the body of POST /api/v1/admin/comments/bulk. The
// IDs slice is the selection from the list UI; the action is one of
// the canonical verbs. We model the body as a typed struct (not a
// generic map) so the JSON decoder rejects extra fields and the
// shape is OpenAPI-friendly.
type bulkRequest struct {
	IDs    []string   `json:"ids"`
	Action BulkAction `json:"action"`
}

// bulkResponse is the body of a successful bulk action. We return
// the updated comments so the UI can drop them into its local cache
// without a second round-trip.
type bulkResponse struct {
	Updated []Comment `json:"updated"`
	// Count is the number of rows affected. Equals len(Updated) in
	// the all-or-nothing model; included so a client can branch on
	// the count without re-walking the array.
	Count int `json:"count"`
}

// bulk handles POST /api/v1/admin/comments/bulk. Applies the action
// to every ID in a single transaction. If any ID is unknown the
// whole request is rejected with a 422 and the database is
// untouched.
//
//	200 on success, with the updated rows.
//	400 on malformed body / bad verb / empty IDs.
//	413 on too many IDs (cap at maxBulkIDs).
//	422 when at least one ID is unknown — selection out of sync with
//	    the list view; the client should reload its selection.
func (h *handlers) bulk(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	var body bulkRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_body",
			"request body must be a JSON object with ids and action")
		return
	}

	if len(body.IDs) == 0 {
		router.WriteError(w, http.StatusBadRequest, "missing_ids", "ids must be non-empty")
		return
	}
	if len(body.IDs) > maxBulkIDs {
		router.WriteError(w, http.StatusRequestEntityTooLarge, "too_many_ids",
			"too many ids in a single bulk request")
		return
	}
	for _, id := range body.IDs {
		if id == "" {
			router.WriteError(w, http.StatusBadRequest, "invalid_id", "ids must be non-empty strings")
			return
		}
	}

	if !IsValidBulkAction(body.Action) {
		router.WriteError(w, http.StatusBadRequest, "invalid_action",
			"action must be one of approve, spam, trash")
		return
	}

	status := StatusForBulkAction(body.Action)
	updated, err := h.store.Bulk(r.Context(), body.IDs, status)
	if err != nil {
		if errors.Is(err, ErrBulkPartial) {
			// 422 Unprocessable Entity: the request was well-formed
			// but referred to non-existent rows. RFC 4918 is the
			// canonical "syntactically OK, semantically broken"
			// signal and aligns with how the rest of the admin
			// surface treats out-of-sync selections.
			router.WriteError(w, http.StatusUnprocessableEntity, "ids_not_found",
				"one or more ids were not found; the selection may be stale")
			return
		}
		h.logger.ErrorContext(r.Context(), "admin/comments: bulk failed",
			slog.Int("count", len(body.IDs)),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to apply bulk action")
		return
	}

	router.WriteJSON(w, http.StatusOK, bulkResponse{
		Updated: updated,
		Count:   len(updated),
	})
}
