package media

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/media/collections"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// CollectionsDeps is the dependency bag the collections sub-mount
// needs. Store is required; Logger / Policy are required by the
// gate. We keep the bag separate from media.Deps so the wiring
// signal is clear ("collections sub-feature uses its own store, can
// be disabled independently") and so a future Postgres swap of one
// can land without touching the other.
type CollectionsDeps struct {
	Store    collections.Store
	MediaSt  Store
	Policy   policy.Policy
	Logger   *slog.Logger
}

// MountCollections wires the collection endpoints onto mux under
// base (typically "/api/v1/admin/media"). Five new routes:
//
//	GET    {base}/collections        — list every collection (tree)
//	POST   {base}/collections        — create a collection
//	GET    {base}/collections/{id}   — fetch one
//	PUT    {base}/collections/{id}   — rename / move (slug/name/parent)
//	DELETE {base}/collections/{id}   — delete (cascade)
//	POST   {base}/move               — bulk re-file media into a collection
//
// All routes share the gate convention from handler.go — capability
// checks via h.policy with the same media.* capabilities.
func MountCollections(mux *http.ServeMux, base string, deps CollectionsDeps) error {
	if deps.Store == nil {
		return errors.New("admin/media: collections Store is required")
	}
	if deps.MediaSt == nil {
		return errors.New("admin/media: media Store is required")
	}
	if deps.Policy == nil {
		return errors.New("admin/media: Policy is required")
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	h := &collectionsHandlers{
		store:   deps.Store,
		mediaSt: deps.MediaSt,
		policy:  deps.Policy,
		logger:  deps.Logger,
	}
	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base+"/collections", h.gate(policy.CapMediaRead, h.list))
	mux.Handle("POST "+base+"/collections", h.gate(policy.CapMediaUpload, h.create))
	mux.Handle("GET "+base+"/collections/{id}", h.gate(policy.CapMediaRead, h.get))
	mux.Handle("PUT "+base+"/collections/{id}", h.gate(policy.CapMediaUpload, h.update))
	mux.Handle("DELETE "+base+"/collections/{id}", h.gate(policy.CapMediaDelete, h.delete))
	mux.Handle("POST "+base+"/move", h.gate(policy.CapMediaUpload, h.moveMedia))
	return nil
}

type collectionsHandlers struct {
	store   collections.Store
	mediaSt Store
	policy  policy.Policy
	logger  *slog.Logger
}

func (h *collectionsHandlers) gate(cap policy.Capability, next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if d := h.policy.Can(pr, cap, nil); !d.Allowed {
			router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
			return
		}
		next(w, r, pr)
	})
}

// listResponse is the envelope returned by GET /collections. We wrap
// the slice in a struct so we can append metadata (e.g. total count)
// without a breaking JSON shape change.
type listResponse struct {
	Data []collections.Collection `json:"data"`
}

// list returns every collection. The admin tree sidebar reconstructs
// the hierarchy locally from the flat list.
func (h *collectionsHandlers) list(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	out, err := h.store.List(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/media: collections list failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list collections")
		return
	}
	router.WriteJSON(w, http.StatusOK, listResponse{Data: out})
}

// createBody is the POST body shape. ParentID is optional.
type createBody struct {
	Slug     string  `json:"slug"`
	Name     string  `json:"name"`
	ParentID *string `json:"parent_id,omitempty"`
}

func (h *collectionsHandlers) create(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	var body createBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		router.WriteError(w, http.StatusBadRequest, "invalid_body", "could not parse json: "+err.Error())
		return
	}
	c, err := h.store.Create(r.Context(), collections.CreateInput{
		Slug:     body.Slug,
		Name:     body.Name,
		ParentID: body.ParentID,
	})
	if err != nil {
		writeCollectionError(w, r, h.logger, err, "create")
		return
	}
	router.WriteJSON(w, http.StatusCreated, c)
}

func (h *collectionsHandlers) get(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "collection id is required")
		return
	}
	c, err := h.store.GetByID(r.Context(), id)
	if err != nil {
		writeCollectionError(w, r, h.logger, err, "get")
		return
	}
	router.WriteJSON(w, http.StatusOK, c)
}

// updateBody is the PUT body shape. All three fields are optional.
// A slug or name change is a rename (cheap). A parent_id change is
// a move (rewrites paths). A request with both is rejected — the
// caller must do them as two requests so a partial failure mode is
// crisp.
type updateBody struct {
	Slug     *string `json:"slug,omitempty"`
	Name     *string `json:"name,omitempty"`
	ParentID *string `json:"parent_id,omitempty"`
	// MoveParent signals "set parent_id to ParentID even if
	// ParentID is null". Without this flag we cannot distinguish
	// "client did not specify parent_id" from "client wants to
	// move to root (null parent_id)". JSON cannot encode "null vs
	// undefined" without a sidecar field.
	MoveParent bool `json:"move_parent,omitempty"`
}

func (h *collectionsHandlers) update(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "collection id is required")
		return
	}
	var body updateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		router.WriteError(w, http.StatusBadRequest, "invalid_body", "could not parse json: "+err.Error())
		return
	}
	if body.Slug == nil && body.Name == nil && !body.MoveParent {
		router.WriteError(w, http.StatusBadRequest, "empty_update", "supply slug, name, or move_parent")
		return
	}
	// Move is its own operation; if the caller asked for a move,
	// we apply that first, then any rename on top.
	if body.MoveParent {
		if _, err := h.store.Move(r.Context(), id, collections.MoveInput{NewParentID: body.ParentID}); err != nil {
			writeCollectionError(w, r, h.logger, err, "move")
			return
		}
	}
	if body.Slug != nil || body.Name != nil {
		c, err := h.store.Rename(r.Context(), id, collections.UpdateInput{Slug: body.Slug, Name: body.Name})
		if err != nil {
			writeCollectionError(w, r, h.logger, err, "rename")
			return
		}
		router.WriteJSON(w, http.StatusOK, c)
		return
	}
	c, err := h.store.GetByID(r.Context(), id)
	if err != nil {
		writeCollectionError(w, r, h.logger, err, "get")
		return
	}
	router.WriteJSON(w, http.StatusOK, c)
}

func (h *collectionsHandlers) delete(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "collection id is required")
		return
	}
	if err := h.store.Delete(r.Context(), id); err != nil {
		writeCollectionError(w, r, h.logger, err, "delete")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// moveMediaBody is the POST /move body. IDs is a list of media UUIDs;
// CollectionID is the target folder (nil/missing = move to root).
type moveMediaBody struct {
	IDs          []string `json:"ids"`
	CollectionID *string  `json:"collection_id,omitempty"`
}

// moveResult counts the successful + failed moves so the caller can
// surface a precise summary in the admin UI. Failed ids carry an
// error code per id for diagnosis.
type moveResult struct {
	Moved  int               `json:"moved"`
	Failed map[string]string `json:"failed,omitempty"`
}

func (h *collectionsHandlers) moveMedia(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	var body moveMediaBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		router.WriteError(w, http.StatusBadRequest, "invalid_body", "could not parse json: "+err.Error())
		return
	}
	if len(body.IDs) == 0 {
		router.WriteError(w, http.StatusBadRequest, "empty_ids", "ids must be non-empty")
		return
	}
	if len(body.IDs) > MaxBulkSize {
		router.WriteError(w, http.StatusBadRequest, "too_many_ids", "ids exceeds bulk size limit")
		return
	}
	// If the target collection is provided, verify it exists up
	// front so we don't half-move and then 404 mid-loop.
	if body.CollectionID != nil {
		if _, err := h.store.GetByID(r.Context(), *body.CollectionID); err != nil {
			if errors.Is(err, collections.ErrNotFound) {
				router.WriteError(w, http.StatusNotFound, "collection_not_found", "target collection not found")
				return
			}
			h.logger.ErrorContext(r.Context(), "admin/media: target collection lookup failed", slog.Any("err", err))
			router.WriteError(w, http.StatusInternalServerError, "internal_error", "could not verify target collection")
			return
		}
	}
	result := moveResult{Failed: map[string]string{}}
	for _, id := range body.IDs {
		if err := h.mediaSt.SetCollection(r.Context(), id, body.CollectionID); err != nil {
			if errors.Is(err, ErrNotFound) {
				result.Failed[id] = "not_found"
				continue
			}
			h.logger.ErrorContext(r.Context(), "admin/media: SetCollection failed",
				slog.String("asset_id", id),
				slog.Any("err", err),
			)
			result.Failed[id] = "internal_error"
			continue
		}
		result.Moved++
	}
	if len(result.Failed) == 0 {
		result.Failed = nil
	}
	router.WriteJSON(w, http.StatusOK, result)
}

// writeCollectionError translates collections errors into HTTP
// responses. Centralised so the five endpoints don't drift in how
// they surface the same error type.
func writeCollectionError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error, op string) {
	switch {
	case errors.Is(err, collections.ErrNotFound):
		router.WriteError(w, http.StatusNotFound, "not_found", "collection not found")
	case errors.Is(err, collections.ErrSlugConflict):
		router.WriteError(w, http.StatusConflict, "slug_conflict", "slug conflicts with a sibling")
	case errors.Is(err, collections.ErrInvalidSlug):
		router.WriteError(w, http.StatusBadRequest, "invalid_slug", "slug is invalid")
	case errors.Is(err, collections.ErrInvalidName):
		router.WriteError(w, http.StatusBadRequest, "invalid_name", "name is invalid")
	case errors.Is(err, collections.ErrCycle):
		router.WriteError(w, http.StatusBadRequest, "cycle", "move would create a cycle")
	case errors.Is(err, collections.ErrTooDeep):
		router.WriteError(w, http.StatusBadRequest, "too_deep", "collection depth exceeds maximum")
	default:
		logger.ErrorContext(r.Context(), "admin/media: collections "+op+" failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "operation failed")
	}
}
