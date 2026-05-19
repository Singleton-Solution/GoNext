package comments

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// maxReplyContentBytes caps the size of a moderator reply. Chosen
// to match the public comment surface; longer drafts are typically
// the wrong tool ("write a post instead") and a hard cap also
// guards against an accidental paste of a multi-MB document.
const maxReplyContentBytes = 16 * 1024

// replyRequest is the body of POST /api/v1/admin/comments/{id}/reply.
type replyRequest struct {
	Content string `json:"content"`
}

// reply handles POST /api/v1/admin/comments/{id}/reply. Creates a
// child comment under the path-segment parent.
//
//   - The parent's post_id is inherited automatically (the store
//     copies it across).
//   - path = parent.path || self.label. The Postgres backend gets
//     this from the comments_set_path trigger; the in-memory store
//     mirrors the trigger explicitly so tests catch a regression
//     before it reaches the DB.
//   - The author is the request's principal — moderator replies are
//     always linked to a real user. The display name comes from
//     CurrentDisplayName (or empty if not wired, in which case the
//     store substitutes "Moderator").
//   - The new row lands in 'approved' state: the operator wouldn't
//     reply if they weren't endorsing the response.
//
// Status codes:
//
//	201 Created on success, with the new comment as the body.
//	400 on missing/empty content.
//	404 when the parent doesn't exist.
//	413 when content exceeds maxReplyContentBytes.
func (h *handlers) reply(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	parentID := r.PathValue("id")
	if parentID == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "parent comment id is required")
		return
	}

	// Cap the body to avoid an unbounded read on a malicious client.
	// http.MaxBytesReader returns the friendlier "request body too
	// large" error rather than blowing past the cap.
	r.Body = http.MaxBytesReader(w, r.Body, maxReplyContentBytes*2)

	var body replyRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		// MaxBytesReader's "http: request body too large" surfaces
		// as a decode error; we rebrand it as 413 so the UI can
		// branch on the code without parsing the message.
		if strings.Contains(err.Error(), "request body too large") {
			router.WriteError(w, http.StatusRequestEntityTooLarge, "content_too_large",
				"reply content exceeds the size limit")
			return
		}
		router.WriteError(w, http.StatusBadRequest, "invalid_body",
			"request body must be a JSON object with a content field")
		return
	}

	trimmed := strings.TrimSpace(body.Content)
	if trimmed == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_content", "content is required")
		return
	}
	if len(trimmed) > maxReplyContentBytes {
		router.WriteError(w, http.StatusRequestEntityTooLarge, "content_too_large",
			"reply content exceeds the size limit")
		return
	}

	uid := h.currentUID(r)
	display := h.currentDisplay(r)

	out, err := h.store.Reply(r.Context(), parentID, uid, display, trimmed)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "parent comment not found")
			return
		}
		if errors.Is(err, ErrEmptyContent) {
			// Belt-and-braces: the handler already trimmed and
			// checked the content, but a custom store might
			// double-validate. Surface a 400 consistent with the
			// other empty-content path.
			router.WriteError(w, http.StatusBadRequest, "missing_content", "content is required")
			return
		}
		h.logger.ErrorContext(r.Context(), "admin/comments: reply failed",
			slog.String("parent_id", parentID),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to create reply")
		return
	}

	router.WriteJSON(w, http.StatusCreated, out)
}
