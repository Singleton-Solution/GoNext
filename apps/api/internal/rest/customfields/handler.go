// Package customfields wires packages/go/customfields onto the REST
// router. Two mount points:
//
//	customfields.MountGroups(mux, "/api/v1/custom-fields/groups", deps)
//	customfields.MountMeta(mux,   "/api/v1/posts",                deps)
//
// Groups CRUD is admin-only (writes need edit_field_groups; reads
// surface anonymously per the public-API posture).
// Meta-value CRUD inherits the post's policy — reading post meta
// requires read_post; writing requires edit_post and the value is
// schema-validated against the field group before persistence.
package customfields

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	cf "github.com/Singleton-Solution/GoNext/packages/go/customfields"
)

// maxBodyBytes caps the request body size for group + meta writes.
// 256 KiB covers the largest realistic schema + the deepest realistic
// meta blob; anything larger is almost always a fuzzer probe.
const maxBodyBytes = 256 * 1024

// Deps is the dependency bag for both Mount entry points.
type Deps struct {
	Store  cf.Store
	Logger *slog.Logger
}

func (d Deps) validate() error {
	if d.Store == nil {
		return errors.New("rest/customfields: Store is required")
	}
	return nil
}

type handlers struct {
	store  cf.Store
	logger *slog.Logger
}

func newHandlers(deps Deps) (*handlers, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &handlers{store: deps.Store, logger: deps.Logger}, nil
}

// MountGroups wires the field-group CRUD routes onto mux.
func MountGroups(mux *http.ServeMux, base string, deps Deps) error {
	h, err := newHandlers(deps)
	if err != nil {
		return err
	}
	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base, http.HandlerFunc(h.listGroups))
	mux.Handle("POST "+base, http.HandlerFunc(h.createGroup))
	mux.Handle("GET "+base+"/{id}", http.HandlerFunc(h.getGroup))
	mux.Handle("PATCH "+base+"/{id}", http.HandlerFunc(h.updateGroup))
	mux.Handle("DELETE "+base+"/{id}", http.HandlerFunc(h.deleteGroup))
	return nil
}

// MountMeta wires the per-post meta routes onto mux. base is
// typically "/api/v1/posts" so the resulting routes are
// "/api/v1/posts/{post_id}/meta[/...]".
func MountMeta(mux *http.ServeMux, base string, deps Deps) error {
	h, err := newHandlers(deps)
	if err != nil {
		return err
	}
	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base+"/{post_id}/meta", http.HandlerFunc(h.listMeta))
	mux.Handle("GET "+base+"/{post_id}/meta/{group_id}", http.HandlerFunc(h.getMeta))
	mux.Handle("PUT "+base+"/{post_id}/meta/{group_id}", http.HandlerFunc(h.putMeta))
	return nil
}

// ---- groups ----------------------------------------------------------------

func (h *handlers) listGroups(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.ListGroups(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "rest/customfields: list groups", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list groups")
		return
	}
	router.WriteJSON(w, http.StatusOK, router.Page[cf.FieldGroup]{Data: rows})
}

type createGroupInput struct {
	Slug      string          `json:"slug"`
	Title     string          `json:"title"`
	PostTypes []string        `json:"post_types,omitempty"`
	Schema    json.RawMessage `json:"schema"`
}

func (h *handlers) createGroup(w http.ResponseWriter, r *http.Request) {
	var in createGroupInput
	if err := decodeBody(r, &in); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if strings.TrimSpace(in.Slug) == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_slug", "slug is required")
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_title", "title is required")
		return
	}
	if len(in.Schema) == 0 {
		router.WriteError(w, http.StatusBadRequest, "missing_schema", "schema is required")
		return
	}
	// Validate the schema parses; we don't compile here (the validator
	// in customfields.Validate handles compilation lazily) but a
	// payload that isn't valid JSON should fail at creation time.
	var probe map[string]any
	if err := json.Unmarshal(in.Schema, &probe); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_schema",
			"schema must be a JSON object: "+err.Error())
		return
	}

	g, err := h.store.InsertGroup(r.Context(), cf.FieldGroupCreate{
		Slug:      in.Slug,
		Title:     in.Title,
		PostTypes: in.PostTypes,
		Schema:    in.Schema,
	})
	if err != nil {
		if errors.Is(err, cf.ErrDuplicateSlug) {
			router.WriteError(w, http.StatusConflict, "duplicate_slug", "a group with this slug already exists")
			return
		}
		h.logger.ErrorContext(r.Context(), "rest/customfields: insert group", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to create group")
		return
	}
	w.Header().Set("X-Version", strconv.Itoa(g.Version))
	router.WriteJSON(w, http.StatusCreated, g)
}

func (h *handlers) getGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g, err := h.store.GetGroup(r.Context(), id)
	if err != nil {
		if errors.Is(err, cf.ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "group not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "rest/customfields: get group", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to fetch group")
		return
	}
	w.Header().Set("X-Version", strconv.Itoa(g.Version))
	router.WriteJSON(w, http.StatusOK, g)
}

type updateGroupInput struct {
	Title     *string          `json:"title,omitempty"`
	PostTypes *[]string        `json:"post_types,omitempty"`
	Schema    *json.RawMessage `json:"schema,omitempty"`
}

func (h *handlers) updateGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	version, present, err := router.ParseIfMatchVersion(r)
	if err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_if_match", "If-Match header is malformed")
		return
	}
	if !present {
		router.WriteError(w, http.StatusPreconditionRequired, "if_match_required", "If-Match header is required")
		return
	}
	var in updateGroupInput
	if err := decodeBody(r, &in); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	g, err := h.store.UpdateGroup(r.Context(), id, version, cf.FieldGroupUpdate{
		Title:     in.Title,
		PostTypes: in.PostTypes,
		Schema:    in.Schema,
	})
	if err != nil {
		switch {
		case errors.Is(err, cf.ErrNotFound):
			router.WriteError(w, http.StatusNotFound, "not_found", "group not found")
		case errors.Is(err, cf.ErrVersionConflict):
			router.WriteError(w, http.StatusPreconditionFailed, "version_mismatch", "If-Match version does not match")
		default:
			h.logger.ErrorContext(r.Context(), "rest/customfields: update group", slog.Any("err", err))
			router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to update group")
		}
		return
	}
	w.Header().Set("X-Version", strconv.Itoa(g.Version))
	router.WriteJSON(w, http.StatusOK, g)
}

func (h *handlers) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.DeleteGroup(r.Context(), id); err != nil {
		if errors.Is(err, cf.ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "group not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "rest/customfields: delete group", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to delete group")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- meta -----------------------------------------------------------------

func (h *handlers) listMeta(w http.ResponseWriter, r *http.Request) {
	postID := r.PathValue("post_id")
	rows, err := h.store.ListMeta(r.Context(), postID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "rest/customfields: list meta", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list meta")
		return
	}
	if rows == nil {
		rows = []cf.MetaValue{}
	}
	router.WriteJSON(w, http.StatusOK, router.Page[cf.MetaValue]{Data: rows})
}

func (h *handlers) getMeta(w http.ResponseWriter, r *http.Request) {
	postID := r.PathValue("post_id")
	groupID := r.PathValue("group_id")
	v, err := h.store.GetMeta(r.Context(), postID, groupID)
	if err != nil {
		if errors.Is(err, cf.ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "meta not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "rest/customfields: get meta", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to fetch meta")
		return
	}
	router.WriteJSON(w, http.StatusOK, v)
}

func (h *handlers) putMeta(w http.ResponseWriter, r *http.Request) {
	postID := r.PathValue("post_id")
	groupID := r.PathValue("group_id")

	// Resolve the group + validate the incoming payload against its
	// schema. ErrNotFound on the group is a 404; schema-violation is
	// a 422 (the payload was well-formed JSON but didn't match the
	// shape the group requires).
	group, err := h.store.GetGroup(r.Context(), groupID)
	if err != nil {
		if errors.Is(err, cf.ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "group not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "rest/customfields: get group for put", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to fetch group")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxBodyBytes))
	if err != nil {
		router.WriteError(w, http.StatusRequestEntityTooLarge,
			"body_too_large", "request body exceeds the size limit")
		return
	}
	if err := cf.Validate(group, body); err != nil {
		router.WriteError(w, http.StatusUnprocessableEntity,
			"schema_violation", err.Error())
		return
	}

	v, err := h.store.PutMeta(r.Context(), postID, groupID, body)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "rest/customfields: put meta", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to persist meta")
		return
	}
	router.WriteJSON(w, http.StatusOK, v)
}

// decodeBody is the shared decode helper. Limits the body to
// maxBodyBytes and rejects unknown fields so client typos surface.
func decodeBody(r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return errors.New("request body could not be parsed: " + err.Error())
	}
	if dec.More() {
		return errors.New("request body must contain a single JSON value")
	}
	return nil
}
