package wprest

import "encoding/json"

// renderedString is the {"rendered": "...", "raw": "..."} shape the
// WP REST API returns for most content fields. We decode both halves
// — "raw" is preferred for migration (un-shortcode'd source) but is
// only populated when the requesting user has the edit context.
// "rendered" is the public-facing HTML and always present.
type renderedString struct {
	Rendered string `json:"rendered"`
	Raw      string `json:"raw"`
}

// String returns the most faithful representation: prefer raw, fall
// back to rendered. WP escapes characters in "rendered" that "raw"
// keeps literal — for migration we want the literal source so we
// can re-process it ourselves.
func (rs renderedString) String() string {
	if rs.Raw != "" {
		return rs.Raw
	}
	return rs.Rendered
}

// restPost is the unmarshal target for /posts, /pages, and /media.
// Field set is a deliberate subset of the v2 API; unknown fields are
// dropped by encoding/json so the struct doesn't need updating each
// WP release.
type restPost struct {
	ID            int64          `json:"id"`
	Date          string         `json:"date"`
	DateGMT       string         `json:"date_gmt"`
	GUID          renderedString `json:"guid"`
	Modified      string         `json:"modified"`
	ModifiedGMT   string         `json:"modified_gmt"`
	Slug          string         `json:"slug"`
	Status        string         `json:"status"`
	Type          string         `json:"type"`
	Link          string         `json:"link"`
	Title         renderedString `json:"title"`
	Content       renderedString `json:"content"`
	Excerpt       renderedString `json:"excerpt"`
	Author        int64          `json:"author"`
	FeaturedMedia int64          `json:"featured_media"`
	CommentStatus string         `json:"comment_status"`
	PingStatus    string         `json:"ping_status"`
	Sticky        bool           `json:"sticky"`
	MenuOrder     int            `json:"menu_order"`
	Parent        int64          `json:"parent"`
	Password      string         `json:"password"`

	// Taxonomy ids (returned by WP as []int64 on /posts and /pages).
	Categories []int64 `json:"categories"`
	Tags       []int64 `json:"tags"`

	// Attachment-only fields.
	SourceURL    string          `json:"source_url"`
	MimeType     string          `json:"mime_type"`
	MediaType    string          `json:"media_type"`
	AltText      string          `json:"alt_text"`
	Caption      renderedString  `json:"caption"`
	Description  renderedString  `json:"description"`
	MediaDetails json.RawMessage `json:"media_details"`

	// Meta is the flattened, public-meta JSON. Plugins can choose
	// to expose private meta here; we keep it as-is.
	Meta map[string]json.RawMessage `json:"meta"`
}

// restCategory and restTag share the same payload shape; we keep
// them distinct in case WP diverges later.
type restCategory struct {
	ID          int64  `json:"id"`
	Count       int    `json:"count"`
	Description string `json:"description"`
	Link        string `json:"link"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Parent      int64  `json:"parent"`
	Taxonomy    string `json:"taxonomy"`
}

type restTag struct {
	ID          int64  `json:"id"`
	Count       int    `json:"count"`
	Description string `json:"description"`
	Link        string `json:"link"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Taxonomy    string `json:"taxonomy"`
}

// restUser is the /users endpoint payload. WP returns roles as a
// []string in the "edit" context only — under "view" the field is
// omitted entirely.
type restUser struct {
	ID          int64    `json:"id"`
	Username    string   `json:"username"`
	Name        string   `json:"name"`
	FirstName   string   `json:"first_name"`
	LastName    string   `json:"last_name"`
	Email       string   `json:"email"`
	URL         string   `json:"url"`
	Description string   `json:"description"`
	Link        string   `json:"link"`
	Locale      string   `json:"locale"`
	Nickname    string   `json:"nickname"`
	Slug        string   `json:"slug"`
	Roles       []string `json:"roles"`
}
