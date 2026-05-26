package customfields

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// FieldGroup is one cohesive bundle of custom fields a post type can
// gain. The shape mirrors ACF's "field group" but the storage and
// validation are GoNext-native — no PHP, no serialised arrays.
//
// The Schema field is a JSON Schema (draft 2020-12) object that
// describes the meta blob's keys + value types + required fields.
// jsonschemautil.Compile parses it at registration time; the compiled
// schema is reused for every Validate call.
type FieldGroup struct {
	// ID is the persistent identifier. The store assigns it; clients
	// reference it from /api/v1/posts/{post_id}/meta/{group_id}.
	ID string `json:"id"`

	// Slug is the human-readable key (e.g. "product_details"). Used
	// in URLs, in templates' meta-access shorthand, and in audit
	// log entries. Stable across renames — the title can be edited
	// without breaking template references.
	Slug string `json:"slug"`

	// Title is the operator-facing label for the group ("Product
	// Details"). Surfaces in the admin's field-group picker.
	Title string `json:"title"`

	// PostTypes is the list of post-type slugs the group attaches
	// to. Empty == every post type.
	PostTypes []string `json:"post_types,omitempty"`

	// Schema is the JSON Schema document that constrains the meta
	// blob. Stored as a raw JSON message so the store doesn't need
	// to know about the schema-compiler types.
	Schema json.RawMessage `json:"schema"`

	// CreatedAt/UpdatedAt are housekeeping. Returned to clients so
	// the admin can show "last edited 2 days ago" without a follow-
	// up query.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Version is the optimistic-concurrency stamp. The PATCH
	// endpoint requires an If-Match header carrying this value;
	// stale writes are rejected with a 412.
	Version int `json:"version"`
}

// MetaValue is one group's persisted value for one post. The shape
// is intentionally simple — one blob per (post_id, group_id) — so
// the storage layer doesn't have to know about individual fields.
// The schema-driven validation happens at the boundary; the row
// only stores the validated blob.
type MetaValue struct {
	PostID    string          `json:"post_id"`
	GroupID   string          `json:"group_id"`
	Values    json.RawMessage `json:"values"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// FieldGroupCreate is the input shape for Store.InsertGroup. The
// store fills ID/CreatedAt/UpdatedAt/Version.
type FieldGroupCreate struct {
	Slug      string
	Title     string
	PostTypes []string
	Schema    json.RawMessage
}

// FieldGroupUpdate is the input shape for Store.UpdateGroup. Each
// field is a pointer so the handler can distinguish "leave alone"
// from "set to empty".
type FieldGroupUpdate struct {
	Title     *string
	PostTypes *[]string
	Schema    *json.RawMessage
}

// Store is the persistence boundary for field groups + meta values.
// Two backends: in-memory for tests, Postgres for production.
type Store interface {
	// ListGroups returns every field group ordered by slug.
	ListGroups(ctx context.Context) ([]FieldGroup, error)

	// GetGroup fetches by id. Returns ErrNotFound when missing.
	GetGroup(ctx context.Context, id string) (FieldGroup, error)

	// GetGroupBySlug is the convenience lookup for theme templates
	// that reference groups by their slug.
	GetGroupBySlug(ctx context.Context, slug string) (FieldGroup, error)

	// InsertGroup persists a new group. Returns the populated row
	// (ID + timestamps assigned by the store).
	InsertGroup(ctx context.Context, in FieldGroupCreate) (FieldGroup, error)

	// UpdateGroup applies the non-nil update fields. version is the
	// expected current version; mismatch returns ErrVersionConflict.
	UpdateGroup(ctx context.Context, id string, version int, u FieldGroupUpdate) (FieldGroup, error)

	// DeleteGroup soft-deletes the group. Returns ErrNotFound if
	// already gone.
	DeleteGroup(ctx context.Context, id string) error

	// ListMeta returns every meta value attached to postID, one per
	// group. Empty slice when no groups have values.
	ListMeta(ctx context.Context, postID string) ([]MetaValue, error)

	// GetMeta returns the values for one (post, group) pair.
	GetMeta(ctx context.Context, postID, groupID string) (MetaValue, error)

	// PutMeta replaces the values for one (post, group). The blob
	// has already been validated against the group's schema before
	// it reaches the store.
	PutMeta(ctx context.Context, postID, groupID string, values json.RawMessage) (MetaValue, error)
}

// Errors.

var (
	ErrNotFound        = errors.New("customfields: not found")
	ErrVersionConflict = errors.New("customfields: version conflict")
	ErrDuplicateSlug   = errors.New("customfields: duplicate slug")
)
