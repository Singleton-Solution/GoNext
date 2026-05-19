package comments

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
)

// listResponse is the envelope returned by the list endpoint. We
// reuse router.Page so the shape matches the rest of the REST
// surface. Cursor encoding: an opaque base64url of "path:id" so a
// future tie-break on (path,id) can swap in without a contract break.
type listResponse = router.Page[Comment]

// list handles GET /api/v1/posts/{id}/comments.
//
// Query params:
//
//	limit  — optional; 1..100. Default 50.
//	after  — optional cursor (opaque). Returned by the previous page.
//
// Returns approved comments only, in ltree path ascending order so a
// thread renders naturally (parent before child, siblings in
// insertion order because the path embeds a v7 UUID label).
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	postID := r.PathValue("id")
	if postID == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "post id is required")
		return
	}

	q := r.URL.Query()

	limit := defaultListLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			router.WriteError(w, http.StatusBadRequest, "invalid_limit",
				"limit must be a positive integer")
			return
		}
		if n > maxListLimit {
			n = maxListLimit
		}
		limit = n
	}

	var afterPath, afterID string
	if raw := q.Get("after"); raw != "" {
		decoded, err := router.ParseCursor(raw)
		if err != nil {
			router.WriteError(w, http.StatusBadRequest, "invalid_cursor",
				"after must be a valid cursor")
			return
		}
		if i := strings.IndexByte(decoded, ':'); i >= 0 {
			afterPath = decoded[:i]
			afterID = decoded[i+1:]
		} else {
			afterPath = decoded
		}
	}

	res, err := h.store.List(r.Context(), ListFilter{
		PostID:    postID,
		AfterPath: afterPath,
		AfterID:   afterID,
		Limit:     limit,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "rest/comments: list failed",
			slog.String("post_id", postID),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error",
			"failed to list comments")
		return
	}

	var nextCursor string
	if res.HasNext && len(res.Comments) > 0 {
		last := res.Comments[len(res.Comments)-1]
		nextCursor = router.EncodeCursor(last.Path + ":" + last.ID)
	}

	out := res.Comments
	if out == nil {
		out = []Comment{}
	}

	router.WriteJSON(w, http.StatusOK, listResponse{
		Data: out,
		Pagination: router.PageInfo{
			NextCursor: nextCursor,
		},
	})
}
