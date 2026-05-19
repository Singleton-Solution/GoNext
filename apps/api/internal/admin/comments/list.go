package comments

import (
	"log/slog"
	"net/http"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// listResponse is the envelope returned by GET /api/v1/admin/comments.
// We reuse router.Page so the shape matches the rest of the admin
// surface (posts, jobs, etc.). Cursor encoding: a plain page number
// in decimal, base64url-wrapped, so the UI doesn't accumulate a
// dependency on the "page" detail and we can swap to a (created_at,
// id) tuple later without a contract break.
type listResponse = router.Page[Comment]

// list handles GET /api/v1/admin/comments. Query params:
//
//	status   — optional; one of "pending", "approved", "spam", "trash".
//	post_id  — optional; restricts to one post.
//	user_id  — optional; restricts to one author. Excludes anonymous.
//	page     — optional; 1-based page number. Default 1.
//	limit    — optional; 1..100. Default 30.
//
// Results are sorted by created_at DESC. The response envelope is
// {"data":[...], "pagination":{"next_cursor":"..."}}. The cursor is
// non-empty only when more pages exist beyond the current one.
func (h *handlers) list(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	q := r.URL.Query()

	var filter ListFilter

	// Status filter. Empty string means "no filter"; any other
	// value must be in AllStatuses or we 400 so the client doesn't
	// accidentally typo "approve" (the bulk verb) instead of
	// "approved" (the state).
	if s := q.Get("status"); s != "" {
		st := Status(s)
		if !IsValidStatus(st) {
			router.WriteError(w, http.StatusBadRequest, "invalid_status",
				"status must be one of pending, approved, spam, trash")
			return
		}
		filter.Status = st
	}

	if pid := q.Get("post_id"); pid != "" {
		filter.PostID = pid
	}
	if uid := q.Get("user_id"); uid != "" {
		filter.UserID = uid
	}

	page, err := parsePage(q.Get("page"))
	if err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_page", err.Error())
		return
	}
	filter.Page = page

	limit, err := parseLimit(q.Get("limit"))
	if err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	filter.Limit = limit

	res, err := h.store.List(r.Context(), filter)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/comments: list failed",
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list comments")
		return
	}

	var nextCursor string
	if res.HasNext {
		// Cursor is the next page number, base64url-encoded so the
		// on-wire string is opaque to the client. The list handler
		// is the only consumer; we don't decode it.
		nextCursor = router.EncodeCursor(itoa(page + 1))
	}

	// Always-allocated slice so the JSON encoder emits "[]" rather
	// than "null" on an empty page. The admin UI treats null as a
	// surprise error case.
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

// itoa is a small int→string helper to avoid pulling strconv into
// this file just for one use. We already imported strconv in
// handler.go for limit/page parsing; this would shadow that import
// in some IDEs. Inline integer formatting keeps the package's
// imports tidy.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	const digits = "0123456789"
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = digits[i%10]
		i /= 10
	}
	if neg {
		n--
		buf[n] = '-'
	}
	return string(buf[n:])
}
