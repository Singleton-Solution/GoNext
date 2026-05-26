// Package menus is the admin REST surface for navigation menus and
// their items — issue #54.
//
// Routes (mounted under base, typically /api/v1/admin/menus):
//
//	GET    {base}                          — list menus
//	POST   {base}                          — create menu
//	GET    {base}/{id}                     — get menu (with items)
//	PUT    {base}/{id}                     — update menu (name/attrs only)
//	DELETE {base}/{id}                     — delete menu (cascade items)
//	POST   {base}/{id}/items               — append item
//	PUT    {base}/{id}/items/{itemID}      — update item
//	DELETE {base}/{id}/items/{itemID}      — delete item
//	POST   {base}/{id}/items/reorder       — drag-drop reorder bulk
//
// The whole sub-tree is gated by manage_themes — operators that can
// change the theme are trusted to change the navigation that the theme
// renders.
package menus

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/menus"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// Deps is the dependency bag for [Mount].
type Deps struct {
	Store  menus.Store
	Policy policy.Policy
	Logger *slog.Logger
}

func (d Deps) validate() error {
	if d.Store == nil {
		return errors.New("admin/menus: Deps.Store is required")
	}
	if d.Policy == nil {
		return errors.New("admin/menus: Deps.Policy is required")
	}
	return nil
}

type handlers struct {
	store  menus.Store
	policy policy.Policy
	logger *slog.Logger
}

// Mount wires the menus admin routes onto mux. The whole sub-tree is
// gated by edit_theme_options.
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	h := &handlers{store: deps.Store, policy: deps.Policy, logger: deps.Logger}
	base = strings.TrimRight(base, "/")
	gate := policy.Require(deps.Policy, policy.CapManageThemes)
	mux.Handle("GET "+base, gate(http.HandlerFunc(h.list)))
	mux.Handle("POST "+base, gate(http.HandlerFunc(h.create)))
	mux.Handle("GET "+base+"/{id}", gate(http.HandlerFunc(h.get)))
	mux.Handle("PUT "+base+"/{id}", gate(http.HandlerFunc(h.update)))
	mux.Handle("DELETE "+base+"/{id}", gate(http.HandlerFunc(h.deleteMenu)))
	mux.Handle("POST "+base+"/{id}/items", gate(http.HandlerFunc(h.createItem)))
	mux.Handle("PUT "+base+"/{id}/items/{itemID}", gate(http.HandlerFunc(h.updateItem)))
	mux.Handle("DELETE "+base+"/{id}/items/{itemID}", gate(http.HandlerFunc(h.deleteItem)))
	mux.Handle("POST "+base+"/{id}/items/reorder", gate(http.HandlerFunc(h.reorder)))
	return nil
}

type menuRequest struct {
	Slug  string          `json:"slug"`
	Name  string          `json:"name"`
	Attrs json.RawMessage `json:"attrs,omitempty"`
}

type itemRequest struct {
	Path       string          `json:"path"`
	Label      string          `json:"label"`
	URL        string          `json:"url"`
	ObjectType string          `json:"object_type,omitempty"`
	ObjectID   *uuid.UUID      `json:"object_id,omitempty"`
	Attrs      json.RawMessage `json:"attrs,omitempty"`
}

type reorderRequest struct {
	Items []struct {
		ID   uuid.UUID `json:"id"`
		Path string    `json:"path"`
	} `json:"items"`
}

func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	out, err := h.store.ListMenus(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/menus: list", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list menus")
		return
	}
	if out == nil {
		out = []menus.Menu{}
	}
	router.WriteJSON(w, http.StatusOK, map[string]any{"menus": out})
}

func (h *handlers) create(w http.ResponseWriter, r *http.Request) {
	var req menuRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}
	m, err := h.store.CreateMenu(r.Context(), menus.Menu{
		Slug:  strings.TrimSpace(req.Slug),
		Name:  strings.TrimSpace(req.Name),
		Attrs: req.Attrs,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	router.WriteJSON(w, http.StatusCreated, m)
}

func (h *handlers) get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(r, "id")
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_id", "id is not a valid uuid")
		return
	}
	bundle, err := h.store.GetWithItems(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if bundle.Items == nil {
		bundle.Items = []menus.MenuItem{}
	}
	router.WriteJSON(w, http.StatusOK, bundle)
}

func (h *handlers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(r, "id")
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_id", "id is not a valid uuid")
		return
	}
	var req menuRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}
	// Re-fetch to get the slug — UpdateMenu pins it from the existing
	// row, but the validator runs on the input so we pass the right
	// slug through to satisfy the regex check.
	existing, err := h.store.GetMenu(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.store.UpdateMenu(r.Context(), menus.Menu{
		ID:    id,
		Slug:  existing.Slug,
		Name:  strings.TrimSpace(req.Name),
		Attrs: req.Attrs,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	router.WriteJSON(w, http.StatusOK, out)
}

func (h *handlers) deleteMenu(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(r, "id")
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_id", "id is not a valid uuid")
		return
	}
	if err := h.store.DeleteMenu(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) createItem(w http.ResponseWriter, r *http.Request) {
	menuID, ok := parseUUID(r, "id")
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_id", "id is not a valid uuid")
		return
	}
	var req itemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}
	out, err := h.store.CreateItem(r.Context(), menus.MenuItem{
		MenuID:     menuID,
		Path:       strings.TrimSpace(req.Path),
		Label:      strings.TrimSpace(req.Label),
		URL:        strings.TrimSpace(req.URL),
		ObjectType: strings.TrimSpace(req.ObjectType),
		ObjectID:   req.ObjectID,
		Attrs:      req.Attrs,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	router.WriteJSON(w, http.StatusCreated, out)
}

func (h *handlers) updateItem(w http.ResponseWriter, r *http.Request) {
	menuID, ok := parseUUID(r, "id")
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_id", "id is not a valid uuid")
		return
	}
	itemID, ok := parseUUID(r, "itemID")
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_id", "itemID is not a valid uuid")
		return
	}
	var req itemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}
	out, err := h.store.UpdateItem(r.Context(), menus.MenuItem{
		ID:         itemID,
		MenuID:     menuID,
		Path:       strings.TrimSpace(req.Path),
		Label:      strings.TrimSpace(req.Label),
		URL:        strings.TrimSpace(req.URL),
		ObjectType: strings.TrimSpace(req.ObjectType),
		ObjectID:   req.ObjectID,
		Attrs:      req.Attrs,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	router.WriteJSON(w, http.StatusOK, out)
}

func (h *handlers) deleteItem(w http.ResponseWriter, r *http.Request) {
	itemID, ok := parseUUID(r, "itemID")
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_id", "itemID is not a valid uuid")
		return
	}
	if err := h.store.DeleteItem(r.Context(), itemID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) reorder(w http.ResponseWriter, r *http.Request) {
	menuID, ok := parseUUID(r, "id")
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_id", "id is not a valid uuid")
		return
	}
	var req reorderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}
	// Pull current items so we can carry their non-mutated columns
	// (label/url/attrs/etc) into the validator.
	bundle, err := h.store.GetWithItems(r.Context(), menuID)
	if err != nil {
		writeErr(w, err)
		return
	}
	indexed := make(map[uuid.UUID]menus.MenuItem, len(bundle.Items))
	for _, mi := range bundle.Items {
		indexed[mi.ID] = mi
	}
	out := make([]menus.MenuItem, 0, len(req.Items))
	for _, in := range req.Items {
		existing, ok := indexed[in.ID]
		if !ok {
			router.WriteError(w, http.StatusBadRequest, "unknown_item",
				fmt.Sprintf("item %s does not belong to this menu", in.ID))
			return
		}
		existing.Path = strings.TrimSpace(in.Path)
		out = append(out, existing)
	}
	if err := h.store.ReorderItems(r.Context(), menuID, out); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseUUID(r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, menus.ErrInvalidMenu), errors.Is(err, menus.ErrInvalidItem):
		router.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
	case errors.Is(err, menus.ErrNotFound):
		router.WriteError(w, http.StatusNotFound, "not_found", err.Error())
	default:
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "menus store error")
	}
}
