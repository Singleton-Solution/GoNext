package comments

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// updateRequest is the body of PATCH /api/v1/admin/comments/{id}.
// The single field is the target moderation state. Returning a
// dedicated type (rather than a map[string]any) lets the JSON
// decoder reject unknown fields and gives the OpenAPI generator a
// clean schema to target.
type updateRequest struct {
	Status Status `json:"status"`
}

// update handles PATCH /api/v1/admin/comments/{id}. Transitions the
// comment's moderation state to the value in the request body.
//
//	202 on the rare "no row affected because the status was already
//	    the target" — we don't actually use this; the store always
//	    rewrites the row and the response is 200 with the updated
//	    comment. The wire shape is uniform with the bulk endpoint.
//	200 on success, with the updated comment as the body.
//	400 on a missing/unknown status.
//	404 when the comment doesn't exist.
func (h *handlers) update(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "comment id is required")
		return
	}

	var body updateRequest
	dec := json.NewDecoder(r.Body)
	// Reject unknown fields so a typo'd payload doesn't silently
	// no-op the moderation action. Operators expect the API to
	// catch typos at the boundary; a "looks like nothing happened"
	// outcome is a worse failure mode than a 400.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_body",
			"request body must be a JSON object with a status field")
		return
	}

	if body.Status == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_status", "status is required")
		return
	}
	if !IsValidStatus(body.Status) {
		router.WriteError(w, http.StatusBadRequest, "invalid_status",
			"status must be one of pending, approved, spam, trash")
		return
	}

	out, err := h.store.UpdateStatus(r.Context(), id, body.Status)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "comment not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "admin/comments: update failed",
			slog.String("id", id),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to update comment")
		return
	}

	router.WriteJSON(w, http.StatusOK, out)
}
