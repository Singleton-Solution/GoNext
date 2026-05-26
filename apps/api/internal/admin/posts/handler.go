package posts

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/revisions"
)

// Deps is the dependency bag for Mount. Every required field is
// non-nil; Logger and Now fall back to safe defaults for convenience.
type Deps struct {
	// Revisions is the revisions Store the list + restore handlers
	// read from. Required.
	Revisions revisions.Store

	// Posts is the post Store the restore handler writes the
	// rolled-back content into. Required.
	Posts PostUpdater

	// Policy gates the edit_posts capability check.
	Policy policy.Policy

	// Logger receives structured log lines. nil falls back to
	// slog.Default.
	Logger *slog.Logger

	// Now lets tests pin the clock for the "Restored from revision X"
	// audit comment timestamp. nil falls back to time.Now.
	Now func() time.Time
}

func (d Deps) validate() error {
	if d.Revisions == nil {
		return errors.New("admin/posts: Revisions is required")
	}
	if d.Posts == nil {
		return errors.New("admin/posts: Posts is required")
	}
	if d.Policy == nil {
		return errors.New("admin/posts: Policy is required")
	}
	return nil
}

// PostUpdater is the minimal surface the restore handler needs from
// the posts package. Carved out so the admin/posts package doesn't
// depend on the full rest/posts.Store and so tests can pass a stub
// without rebuilding a memory store.
//
// SetContentBlocks replaces the row's content_blocks with raw — used
// by the restore endpoint to roll the post back to a revision's
// materialized JSON. Implementations are expected to bump the
// row's version and re-render the content hash; the optimistic
// concurrency guard isn't surfaced here because the admin restore
// flow is a deliberate one-shot operation, not a multi-tab edit.
type PostUpdater interface {
	SetContentBlocks(ctx context.Context, postID string, raw json.RawMessage) error
}

// PostUpdaterFunc is a convenience adapter that lets the binary boot
// wire any closure as a PostUpdater. The wiring site composes the
// "load current version + update content_blocks" two-step against the
// concrete rest/posts.Store without forcing this package to import the
// public posts package.
type PostUpdaterFunc func(ctx context.Context, postID string, raw json.RawMessage) error

// SetContentBlocks satisfies PostUpdater.
func (f PostUpdaterFunc) SetContentBlocks(ctx context.Context, postID string, raw json.RawMessage) error {
	return f(ctx, postID, raw)
}

// handlers is the resolved-Deps form passed around inside the package.
type handlers struct {
	revs   revisions.Store
	posts  PostUpdater
	policy policy.Policy
	logger *slog.Logger
	now    func() time.Time
}

// Mount wires the admin posts routes onto mux under base (typically
// "/api/v1/admin/posts"). Returns an error rather than panicking if
// Deps is malformed so the server boot can surface it cleanly.
//
// Route tree:
//
//	GET   {base}/{id}/revisions                  — list, most recent first
//	POST  {base}/{id}/revisions/{rev}/restore    — roll post back to rev
//
// Every route is gated by edit_posts. The list endpoint is gated
// because revision payloads include the full editable JSON, which
// can carry draft content the post's owner hasn't shared yet.
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	h := &handlers{
		revs:   deps.Revisions,
		posts:  deps.Posts,
		policy: deps.Policy,
		logger: deps.Logger,
		now:    deps.Now,
	}
	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base+"/{id}/revisions", h.gate(h.listRevisions))
	mux.Handle("POST "+base+"/{id}/revisions/{rev}/restore", h.gate(h.restoreRevision))
	return nil
}

// gate wraps a handler with the auth + edit_posts capability check.
// Returns 401 if no principal is on the context, 403 if the principal
// lacks the capability. The richer edit_others_posts split lives in
// the public posts mount; this admin surface is operator-only by
// design (the UI is behind the admin shell).
func (h *handlers) gate(next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if d := h.policy.Can(pr, policy.CapEditPosts, nil); !d.Allowed {
			router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
			return
		}
		next(w, r, pr)
	})
}

// RevisionView is the on-wire shape returned by the list endpoint.
// Mirrors revisions.Revision but flattens the binary hash to hex and
// omits the (potentially large) Snapshot / Delta payloads — the list
// UI doesn't need the materialized JSON, only the row metadata. A
// future "preview" endpoint will materialize on demand.
type RevisionView struct {
	ID        string    `json:"id"`
	PostID    string    `json:"post_id"`
	AuthorID  string    `json:"author_id,omitempty"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
	Title     string    `json:"title,omitempty"`
	Excerpt   string    `json:"excerpt,omitempty"`
	Comment   string    `json:"comment,omitempty"`
	// IsSnapshot is convenient for the admin UI's "full vs delta"
	// chip — saves callers from having to inspect the storage shape.
	IsSnapshot bool `json:"is_snapshot"`
	// IsPermanent surfaces the legal-hold pin so the restore button
	// can be styled differently on rows the pruner won't touch.
	IsPermanent bool `json:"is_permanent,omitempty"`
}

func toView(r revisions.Revision) RevisionView {
	v := RevisionView{
		ID:          r.ID.String(),
		PostID:      r.PostID.String(),
		Kind:        string(r.Kind),
		CreatedAt:   r.CreatedAt,
		Title:       r.Title,
		Excerpt:     r.Excerpt,
		Comment:     r.Comment,
		IsSnapshot:  len(r.Snapshot) > 0,
		IsPermanent: r.IsPermanent,
	}
	if r.AuthorID != uuid.Nil {
		v.AuthorID = r.AuthorID.String()
	}
	return v
}

// -----------------------------------------------------------------------------
// LIST
// -----------------------------------------------------------------------------

func (h *handlers) listRevisions(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	postID, ok := parseUUID(r.PathValue("id"))
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_id", "post id is not a valid uuid")
		return
	}

	limit := parseLimit(r.URL.Query().Get("limit"), 50)
	rows, err := h.revs.List(r.Context(), postID, revisions.Filter{Limit: limit})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/posts: list revisions failed",
			slog.String("post_id", postID.String()), slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error",
			"failed to list revisions")
		return
	}

	out := make([]RevisionView, 0, len(rows))
	for _, row := range rows {
		out = append(out, toView(row))
	}
	router.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}

// -----------------------------------------------------------------------------
// RESTORE
// -----------------------------------------------------------------------------

func (h *handlers) restoreRevision(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	postID, ok := parseUUID(r.PathValue("id"))
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_id", "post id is not a valid uuid")
		return
	}
	revID, ok := parseUUID(r.PathValue("rev"))
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_id", "revision id is not a valid uuid")
		return
	}

	rev, err := h.revs.Get(r.Context(), revID)
	if err != nil {
		if errors.Is(err, revisions.ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "revision not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "admin/posts: revision Get failed",
			slog.String("rev_id", revID.String()), slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error",
			"failed to load revision")
		return
	}
	// Guard against cross-post restore. A caller-supplied (post, rev)
	// mismatch is a 404 not a 400 — the rev exists, just not on this
	// post, and we don't want to leak the existence of other posts'
	// revisions to a curious admin.
	if rev.PostID != postID {
		router.WriteError(w, http.StatusNotFound, "not_found", "revision not found on this post")
		return
	}

	materialized, err := h.revs.Materialize(r.Context(), revID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/posts: materialize failed",
			slog.String("rev_id", revID.String()), slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error",
			"failed to materialize revision")
		return
	}

	if err := h.posts.SetContentBlocks(r.Context(), postID.String(), materialized); err != nil {
		h.logger.ErrorContext(r.Context(), "admin/posts: post update failed",
			slog.String("post_id", postID.String()), slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error",
			"failed to restore post")
		return
	}

	// Write a fresh manual revision recording the restore, per
	// docs/01-core-cms.md §4.4. Best-effort: a failure here doesn't
	// roll back the post update — the operator's intent (the restore)
	// has already landed, and the audit row is a nice-to-have that
	// can be re-emitted later by the post layer's own save trigger.
	authorID, _ := uuid.Parse(pr.UserID)
	auditRev := revisions.Revision{
		PostID:        postID,
		AuthorID:      authorID,
		Kind:          revisions.Manual,
		CreatedAt:     h.now().UTC(),
		Title:         rev.Title,
		Excerpt:       rev.Excerpt,
		ContentBlocks: materialized,
		Comment:       "Restored from revision " + revID.String(),
	}
	if _, err := h.revs.Save(r.Context(), auditRev, revisions.WithForceSnapshot()); err != nil {
		h.logger.WarnContext(r.Context(), "admin/posts: restore audit revision save failed",
			slog.String("post_id", postID.String()),
			slog.String("rev_id", revID.String()),
			slog.Any("err", err))
	}

	router.WriteJSON(w, http.StatusOK, map[string]any{
		"restored_from": revID.String(),
		"post_id":       postID.String(),
	})
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func parseUUID(s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// parseLimit clamps the limit to [1, 100], defaulting to fallback when
// the query string is empty or malformed. The cap is conservative —
// the UI lazy-loads beyond the first page, so a single response
// doesn't need to carry the entire history.
func parseLimit(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return fallback
	}
	if n > 100 {
		return 100
	}
	return n
}
