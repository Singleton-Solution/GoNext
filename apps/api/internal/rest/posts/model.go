package posts

import (
	"encoding/json"
	"time"
)

// Post is the on-the-wire and in-store shape for a row of the posts
// table, projected for the REST API. It is deliberately a thin mirror
// of the SQL columns we expose — keeping a single Post struct means the
// row scan, the response body, and the create/update bodies all share
// the same JSON tag set so clients see consistent field names.
//
// We do NOT embed content_rendered / content_rendered_at in this shape:
// rendered HTML is a derived artifact owned by the renderer (see
// docs/04-block-editor.md). REST callers receive the canonical
// content_blocks and may render client-side, or hit a future
// /api/v1/posts/{id}/rendered endpoint for the cached HTML.
type Post struct {
	ID            string          `json:"id"`
	PostType      string          `json:"post_type"`
	ParentID      *string         `json:"parent_id"`
	AuthorID      string          `json:"author_id"`
	Status        string          `json:"status"`
	Title         string          `json:"title"`
	Slug          string          `json:"slug"`
	Excerpt       *string         `json:"excerpt,omitempty"`
	ContentBlocks json.RawMessage `json:"content_blocks"`
	CommentStatus string          `json:"comment_status"`
	PingStatus    string          `json:"ping_status"`
	MenuOrder     int             `json:"menu_order"`
	Meta          json.RawMessage `json:"meta"`
	PublishedAt   *time.Time      `json:"published_at,omitempty"`
	ScheduledFor  *time.Time      `json:"scheduled_for,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	Version       int             `json:"version"`

	// Protected mirrors "this row has a non-NULL password set." We do
	// NOT serialize the password column itself — clients prove access
	// via the X-Post-Password header, never read the hash. The boolean
	// lets UIs surface the lock icon without a separate round-trip.
	Protected bool `json:"protected"`

	// hash is the row's content_blocks_hash (binary). Unexported so it
	// doesn't leak into JSON; the handler maps it to the ETag header.
	hash []byte

	// password is the row's stored password value, unexported and never
	// surfaced. Used inside the handler to gate access to the rendered
	// content of password-protected posts.
	password *string
}

// Hash returns the content_blocks_hash bytes, for the ETag header.
// Returns nil for rows that haven't yet been rendered.
func (p *Post) Hash() []byte { return p.hash }

// CreateInput is the JSON body for POST. Pointer fields distinguish
// "client omitted" from "client sent zero value" — useful for
// content_blocks, meta, and status which all carry defaults at the
// DB level.
type CreateInput struct {
	// PostType, when non-nil and non-empty, OVERRIDES the mount's
	// hardcoded post type discriminator for this single create. Empty
	// or omitted means "use the mount default".
	//
	// This exists so the admin Pages screens (issue #506) can POST to
	// /api/v1/posts with body {post_type: "page", ...} rather than
	// depending on a separate /api/v1/pages mount. The handler
	// validates the value against the closed {"post","page"} set
	// before forwarding it to the store, so a malicious request can't
	// land a row as a CPT it shouldn't.
	PostType      *string         `json:"post_type,omitempty"`
	ParentID      *string         `json:"parent_id"`
	Status        *string         `json:"status"`
	Title         *string         `json:"title"`
	Slug          *string         `json:"slug"`
	Excerpt       *string         `json:"excerpt"`
	ContentBlocks json.RawMessage `json:"content_blocks"`
	Password      *string         `json:"password"`
	CommentStatus *string         `json:"comment_status"`
	PingStatus    *string         `json:"ping_status"`
	MenuOrder     *int            `json:"menu_order"`
	Meta          json.RawMessage `json:"meta"`
	PublishedAt   *time.Time      `json:"published_at"`
	ScheduledFor  *time.Time      `json:"scheduled_for"`
}

// UpdateInput is the JSON body for PATCH. The shape is identical to
// [CreateInput] except every field is optional — nil = "do not touch
// this column". The handler builds a sparse SET clause from the
// non-nil fields.
type UpdateInput struct {
	ParentID      *string         `json:"parent_id"`
	Status        *string         `json:"status"`
	Title         *string         `json:"title"`
	Slug          *string         `json:"slug"`
	Excerpt       *string         `json:"excerpt"`
	ContentBlocks json.RawMessage `json:"content_blocks"`
	Password      *string         `json:"password"`
	CommentStatus *string         `json:"comment_status"`
	PingStatus    *string         `json:"ping_status"`
	MenuOrder     *int            `json:"menu_order"`
	Meta          json.RawMessage `json:"meta"`
	PublishedAt   *time.Time      `json:"published_at"`
	ScheduledFor  *time.Time      `json:"scheduled_for"`
}

// ListFilter narrows a List query. All fields are optional.
type ListFilter struct {
	Status   string // exact match on posts.status
	AuthorID string // exact match on posts.author_id
	Search   string // free-text against title (ILIKE)
	After    string // cursor: posts.id strictly greater than this UUID
	Limit    int    // page size; clamped to [1, MaxListLimit] by the handler

	// PostType, when non-empty, OVERRIDES the mount's hardcoded post
	// type discriminator for this single request. Empty means "use the
	// mount default" (the typical case).
	//
	// This exists so a single mount (today, /api/v1/posts) can serve
	// both posts and pages — admin Pages screens query
	// /api/v1/posts?post_type=page rather than depending on a separate
	// /api/v1/pages mount that isn't wired yet. The handler validates
	// the value against the closed {"post","page"} set before stuffing
	// it here, so a malicious request can't read a CPT it shouldn't.
	PostType string
}

// MaxListLimit is the hard upper bound on a single page of results.
// Higher requests are silently clamped; the response cursor still
// works so a client can page through the full set with smaller pages.
const MaxListLimit = 100

// DefaultListLimit is the page size when the client omits ?limit.
const DefaultListLimit = 20
