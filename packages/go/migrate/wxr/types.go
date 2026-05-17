package wxr

// Site captures the channel-level metadata at the top of a WXR file.
// It is returned exactly once, by Header, before any item records are
// streamed. WXR files always carry these fields; if any are missing in
// the source export the corresponding string is empty.
type Site struct {
	// Title is the human-readable site title (<title>).
	Title string
	// Link is the site's public URL (<link>).
	Link string
	// Description is the site tagline (<description>).
	Description string
	// PubDate is the export's generation time, as written by WP. Empty
	// if absent or unparseable — the spec doesn't pin a single format.
	PubDate string
	// Language is the BCP47 / WP locale code (<language>), e.g. "en-US".
	Language string
	// BaseSiteURL and BaseBlogURL are the wp: prefixed canonical URLs.
	// On single-site WP they're typically identical; on multisite they
	// differ.
	BaseSiteURL string
	BaseBlogURL string
	// WXRVersion is the declared <wp:wxr_version>, e.g. "1.2".
	WXRVersion string
	// GeneratorVersion is the value of <generator>, usually a URL like
	// "https://wordpress.org/?v=6.4.2".
	GeneratorVersion string
}

// Author is a registered WP user as serialised in the channel preamble.
// Authors are emitted before posts so importers can pre-create users
// and resolve the post.Creator login at insert time.
type Author struct {
	ID          string // wp:author_id
	Login       string // wp:author_login
	Email       string // wp:author_email
	DisplayName string // wp:author_display_name
	FirstName   string // wp:author_first_name
	LastName    string // wp:author_last_name
}

// Category is a taxonomy term in the "category" taxonomy. Categories
// are emitted in the preamble; posts reference them by Nicename.
type Category struct {
	TermID      string // wp:term_id
	Nicename    string // wp:category_nicename (slug)
	Parent      string // wp:category_parent (nicename of parent, "" if top-level)
	Name        string // wp:cat_name
	Description string // wp:category_description
}

// Tag is a taxonomy term in the "post_tag" taxonomy.
type Tag struct {
	TermID      string // wp:term_id
	Slug        string // wp:tag_slug
	Name        string // wp:tag_name
	Description string // wp:tag_description
}

// TermRef is one of the <category> elements nested under an <item>. WP
// uses the same element name for both categories and tags and
// disambiguates with the domain attribute ("category" or "post_tag").
// The Nicename attribute maps back to Category.Nicename / Tag.Slug.
type TermRef struct {
	Domain   string // "category", "post_tag", or any custom taxonomy
	Nicename string
	Name     string // CDATA content
}

// Comment is a single comment on a post. WXR carries enough fields to
// distinguish approved/pending/spam/trash and threading via Parent.
type Comment struct {
	ID          string
	Author      string
	AuthorEmail string
	AuthorURL   string
	AuthorIP    string
	Date        string // wp:comment_date (server-local) — preserved as string
	DateGMT     string // wp:comment_date_gmt
	Content     string // raw HTML, CDATA preserved
	Approved    string // "1", "0", "spam", "trash"
	Type        string // "" for regular, "pingback", "trackback"
	Parent      string // ID of parent comment, "0" if top-level
	UserID      string
}

// Post is the catch-all WXR <item>. It covers regular posts, pages,
// attachments, revisions, and custom post types — discriminated by
// PostType. Importers typically branch on PostType:
//
//   - "post"        → blog post
//   - "page"        → static page
//   - "attachment"  → media file (AttachmentURL is set)
//   - "revision"    → post history (usually skipped on import)
//   - "nav_menu_item" / custom → handled by the target's plugin layer
type Post struct {
	// Title, Link, PubDate are RSS-standard.
	Title   string
	Link    string
	PubDate string

	// Creator is the wp:author_login of the user that owns the post.
	// Cross-references Author.Login emitted in the preamble.
	Creator string

	// Description is the RSS <description>, generally empty in WXR.
	Description string

	// Content is content:encoded — the raw post HTML, exactly as
	// stored in wp_posts.post_content. CDATA wrappers are stripped by
	// the XML decoder but the inner bytes are preserved verbatim.
	Content string

	// Excerpt is excerpt:encoded.
	Excerpt string

	// PostID is wp:post_id, the integer primary key in wp_posts.
	PostID string

	// PostDate / PostDateGMT are wp:post_date / wp:post_date_gmt. We
	// keep them as strings since WP's format ("2024-03-14 12:34:56")
	// isn't RFC 3339 and the parsing convention is importer-specific.
	PostDate    string
	PostDateGMT string

	// Status is wp:status: "publish", "draft", "pending", "private",
	// "future", "trash", "inherit" (for attachments/revisions), etc.
	Status string

	// PostType is wp:post_type: see the docstring above for values.
	PostType string

	// Name is wp:post_name (URL slug).
	Name string

	// Parent is wp:post_parent (PostID of parent, "0" if none).
	Parent string

	// MenuOrder is wp:menu_order (integer as string).
	MenuOrder string

	// Password is wp:post_password (rarely set).
	Password string

	// IsSticky is wp:is_sticky as "0" or "1".
	IsSticky string

	// AttachmentURL is wp:attachment_url. Only present when
	// PostType == "attachment".
	AttachmentURL string

	// CommentStatus / PingStatus are wp:comment_status / wp:ping_status:
	// typically "open" or "closed".
	CommentStatus string
	PingStatus    string

	// Categories and Tags are the resolved term references on this
	// post. Custom taxonomies appear in Terms keyed by domain.
	Categories []TermRef
	Tags       []TermRef
	Terms      []TermRef // every <category> element including categories/tags, in source order

	// Meta is the flattened postmeta key/value pairs. Duplicate keys
	// (rare in practice) win last; callers that need every value can
	// iterate Terms-like over the raw stream by extending the parser.
	Meta map[string]string

	// Comments are emitted inline with the post — the WXR format
	// nests <wp:comment> inside <item>, so we don't stream them
	// separately.
	Comments []Comment
}
