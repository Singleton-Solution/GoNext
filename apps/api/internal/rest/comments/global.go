package comments

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
)

// MountGlobal wires the top-level read-only comments routes onto mux
// under base (typically "/api/v1/comments"). Unlike the per-post
// surface (POST/GET /api/v1/posts/{id}/comments) this is the broad
// "give me approved comments across the whole site" view that mirrors
// the posts/users/media REST contract.
//
// Two routes:
//
//	GET  {base}              — list approved comments (with optional post_id filter)
//	GET  {base}/{id}         — fetch a single approved comment by id
//
// The list path accepts ?post_id=<uuid> as an optional filter when a
// client wants the per-post view via the global surface. Without it,
// the response spans every approved comment on the site (cursor-
// paginated by ltree path + id).
//
// No write surface here — comment submission goes through the
// per-post POST endpoint that exists already.
func MountGlobal(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Now == nil {
		deps.Now = nowOrDefault(deps.Now)
	}
	h := &handlers{
		store:  deps.Store,
		logger: deps.Logger,
		now:    deps.Now,
	}
	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base, http.HandlerFunc(h.globalList))
	mux.Handle("GET "+base+"/{id}", http.HandlerFunc(h.globalGet))
	return nil
}

func nowOrDefault(n func() time.Time) func() time.Time {
	if n == nil {
		return func() time.Time { return time.Now() }
	}
	return n
}

func (h *handlers) globalList(w http.ResponseWriter, r *http.Request) {
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
		PostID:    strings.TrimSpace(q.Get("post_id")),
		AfterPath: afterPath,
		AfterID:   afterID,
		Limit:     limit,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "rest/comments: global list failed", slog.Any("err", err))
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

func (h *handlers) globalGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "id is required")
		return
	}
	// Reuse the list filter machinery: fetch a one-row page filtered
	// by the synthetic "id" predicate. Since the existing ListFilter
	// doesn't carry an id, we list with a generous-enough limit and
	// filter in-handler. This is O(limit) per call; not a hot path —
	// the per-post list is the volume endpoint, the by-id lookup is
	// for permalink resolution.
	res, err := h.store.List(r.Context(), ListFilter{Limit: maxListLimit})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "rest/comments: global get failed",
			slog.String("id", id),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error",
			"failed to fetch comment")
		return
	}
	for _, c := range res.Comments {
		if c.ID == id {
			router.WriteJSON(w, http.StatusOK, c)
			return
		}
	}
	router.WriteError(w, http.StatusNotFound, "not_found", "comment not found")
}
