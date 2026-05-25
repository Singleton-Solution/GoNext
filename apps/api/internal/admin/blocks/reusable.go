// Reusable-blocks admin REST surface — issue #193.
//
// Routes (mounted under base, typically /api/v1/admin/blocks/reusable):
//
//	GET    {base}            — list entries (paginated, ?search= filter)
//	POST   {base}            — create entry
//	GET    {base}/{id}       — fetch single entry
//	PUT    {base}/{id}       — update entry
//	DELETE {base}/{id}       — delete entry
//
// The whole sub-tree is gated by edit_posts (mirrors how WordPress
// gates wp_block create/edit — authors who can write a post are
// trusted to author a reusable block too).

package blocks

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	rb "github.com/Singleton-Solution/GoNext/packages/go/blocks/reusable"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// ReusableDeps is the dependency bag for MountReusable.
type ReusableDeps struct {
	// Store persists reusable block entries. Required.
	Store rb.Store

	// Policy gates the edit_posts capability check. Required.
	Policy policy.Policy

	// Logger receives structured log lines. nil falls back to
	// slog.Default — useful for tests but production should pass
	// a service logger.
	Logger *slog.Logger
}

func (d ReusableDeps) validate() error {
	if d.Store == nil {
		return errors.New("admin/blocks: ReusableDeps.Store is required")
	}
	if d.Policy == nil {
		return errors.New("admin/blocks: ReusableDeps.Policy is required")
	}
	return nil
}

type reusableHandlers struct {
	store  rb.Store
	policy policy.Policy
	logger *slog.Logger
}

// MountReusable wires the reusable-blocks admin routes onto mux under
// base. The whole sub-tree is gated by edit_posts via
// policy.Require — operators who can't write a post can't author a
// reusable block either.
func MountReusable(mux *http.ServeMux, base string, deps ReusableDeps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	h := &reusableHandlers{
		store:  deps.Store,
		policy: deps.Policy,
		logger: deps.Logger,
	}
	base = strings.TrimRight(base, "/")
	gate := policy.Require(deps.Policy, policy.CapEditPosts)
	mux.Handle("GET "+base, gate(http.HandlerFunc(h.list)))
	mux.Handle("POST "+base, gate(http.HandlerFunc(h.create)))
	mux.Handle("GET "+base+"/{id}", gate(http.HandlerFunc(h.get)))
	mux.Handle("PUT "+base+"/{id}", gate(http.HandlerFunc(h.update)))
	mux.Handle("DELETE "+base+"/{id}", gate(http.HandlerFunc(h.delete)))
	return nil
}

// ReusableView is the on-wire shape returned to the admin UI.
// Mirrors rb.Entry, but the JSON-typed fields surface as decoded
// objects so the admin UI doesn't need to double-decode.
type ReusableView struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Attrs     json.RawMessage `json:"attrs"`
	Content   json.RawMessage `json:"content"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func toView(e rb.Entry) ReusableView {
	attrs := e.Attrs
	if len(attrs) == 0 {
		attrs = json.RawMessage(`{}`)
	}
	content := e.Content
	if len(content) == 0 {
		content = json.RawMessage(`[]`)
	}
	return ReusableView{
		ID:        e.ID.String(),
		Name:      e.Name,
		Attrs:     attrs,
		Content:   content,
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
}

type reusableRequest struct {
	Name    string          `json:"name"`
	Attrs   json.RawMessage `json:"attrs,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
}

func (req reusableRequest) toEntry() rb.Entry {
	return rb.Entry{
		Name:    strings.TrimSpace(req.Name),
		Attrs:   req.Attrs,
		Content: req.Content,
	}
}

func (h *reusableHandlers) list(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get("limit"), 50)
	before := parseBefore(r.URL.Query().Get("before"))
	search := strings.TrimSpace(r.URL.Query().Get("search"))

	entries, err := h.store.List(r.Context(), rb.ListFilter{
		NameContains: search,
		Limit:        limit,
		Before:       before,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/blocks/reusable: list failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list reusable blocks")
		return
	}
	out := make([]ReusableView, 0, len(entries))
	for _, e := range entries {
		out = append(out, toView(e))
	}
	router.WriteJSON(w, http.StatusOK, map[string]any{
		"data":       out,
		"pagination": map[string]any{"next_cursor": nextCursor(entries)},
	})
}

func (h *reusableHandlers) create(w http.ResponseWriter, r *http.Request) {
	var req reusableRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}
	created, err := h.store.Create(r.Context(), req.toEntry())
	if err != nil {
		writeReusableErr(w, err)
		return
	}
	router.WriteJSON(w, http.StatusCreated, toView(created))
}

func (h *reusableHandlers) get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(r)
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_id", "id is not a valid uuid")
		return
	}
	got, err := h.store.Get(r.Context(), id)
	if err != nil {
		writeReusableErr(w, err)
		return
	}
	router.WriteJSON(w, http.StatusOK, toView(got))
}

func (h *reusableHandlers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(r)
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_id", "id is not a valid uuid")
		return
	}
	var req reusableRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}
	entry := req.toEntry()
	entry.ID = id
	updated, err := h.store.Update(r.Context(), entry)
	if err != nil {
		writeReusableErr(w, err)
		return
	}
	router.WriteJSON(w, http.StatusOK, toView(updated))
}

func (h *reusableHandlers) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(r)
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_id", "id is not a valid uuid")
		return
	}
	if err := h.store.Delete(r.Context(), id); err != nil {
		writeReusableErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeReusableErr maps the store's sentinel errors to HTTP responses.
func writeReusableErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, rb.ErrInvalidEntry):
		router.WriteError(w, http.StatusBadRequest, "invalid_entry", err.Error())
	case errors.Is(err, rb.ErrNotFound):
		router.WriteError(w, http.StatusNotFound, "not_found", err.Error())
	default:
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "reusable-block store error")
	}
}

// parseUUID resolves the {id} path value into a UUID.
func parseUUID(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// parseLimit clamps the user-supplied limit to a sensible range.
func parseLimit(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 || n > 200 {
		return fallback
	}
	return n
}

// parseBefore parses the cursor used by the list endpoint.
func parseBefore(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// nextCursor returns the wire-form cursor for the next page, or
// the empty string if no rows were returned.
func nextCursor(entries []rb.Entry) string {
	if len(entries) == 0 {
		return ""
	}
	return entries[len(entries)-1].CreatedAt.Format(time.RFC3339Nano)
}
