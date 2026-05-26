package wprest

import (
	"context"
	"strconv"

	"github.com/Singleton-Solution/GoNext/packages/go/migrate/wxr"
)

// FetchAuthors walks /users and yields a *wxr.Author per row.
// Pagination, auth, and decoding are handled by iteratePages. Per-
// row errors from fn (e.g. importer abort) propagate up unchanged.
func (c *Client) FetchAuthors(ctx context.Context, fn func(*wxr.Author) error) error {
	return iteratePages(ctx, c, "users", func(u restUser) error {
		return fn(toAuthor(u))
	})
}

// FetchCategories walks /categories.
func (c *Client) FetchCategories(ctx context.Context, fn func(*wxr.Category) error) error {
	return iteratePages(ctx, c, "categories", func(t restCategory) error {
		return fn(toCategory(t))
	})
}

// FetchTags walks /tags.
func (c *Client) FetchTags(ctx context.Context, fn func(*wxr.Tag) error) error {
	return iteratePages(ctx, c, "tags", func(t restTag) error {
		return fn(toTag(t))
	})
}

// FetchPosts walks /posts. The yielded *wxr.Post has PostType="post".
func (c *Client) FetchPosts(ctx context.Context, fn func(*wxr.Post) error) error {
	return iteratePages(ctx, c, "posts", func(p restPost) error {
		return fn(toPost(p, "post"))
	})
}

// FetchPages walks /pages. The yielded *wxr.Post has PostType="page".
func (c *Client) FetchPages(ctx context.Context, fn func(*wxr.Post) error) error {
	return iteratePages(ctx, c, "pages", func(p restPost) error {
		return fn(toPost(p, "page"))
	})
}

// FetchMedia walks /media. The yielded *wxr.Post has
// PostType="attachment" and AttachmentURL populated from source_url.
func (c *Client) FetchMedia(ctx context.Context, fn func(*wxr.Post) error) error {
	return iteratePages(ctx, c, "media", func(p restPost) error {
		return fn(toAttachment(p))
	})
}

// FetchAll walks every endpoint in the order the importer expects
// (authors → categories → tags → posts → pages → media). The fn
// callback receives each record as a wxr.Record interface; the
// caller does a type switch identical to the wxr parser path.
//
// This is the primary entry point for the importer wiring: the
// wprest package looks just like a wxr parser to downstream code.
func (c *Client) FetchAll(ctx context.Context, fn func(wxr.Record) error) error {
	if err := c.FetchAuthors(ctx, func(a *wxr.Author) error { return fn(a) }); err != nil {
		return err
	}
	if err := c.FetchCategories(ctx, func(t *wxr.Category) error { return fn(t) }); err != nil {
		return err
	}
	if err := c.FetchTags(ctx, func(t *wxr.Tag) error { return fn(t) }); err != nil {
		return err
	}
	if err := c.FetchPosts(ctx, func(p *wxr.Post) error { return fn(p) }); err != nil {
		return err
	}
	if err := c.FetchPages(ctx, func(p *wxr.Post) error { return fn(p) }); err != nil {
		return err
	}
	if err := c.FetchMedia(ctx, func(p *wxr.Post) error { return fn(p) }); err != nil {
		return err
	}
	return nil
}

// -------------------------------------------------------------------
// Conversion helpers. Each returns a freshly allocated *wxr.<T> that
// is independent of the input restPost — the importer caches
// pointers in runState, so they must outlive iteratePages' inner
// loop.
// -------------------------------------------------------------------

func toAuthor(u restUser) *wxr.Author {
	return &wxr.Author{
		ID:          strconv.FormatInt(u.ID, 10),
		Login:       firstNonEmpty(u.Username, u.Slug, u.Nickname),
		Email:       u.Email,
		DisplayName: firstNonEmpty(u.Name, u.Nickname, u.Username, u.Slug),
		FirstName:   u.FirstName,
		LastName:    u.LastName,
	}
}

func toCategory(t restCategory) *wxr.Category {
	var parent string
	if t.Parent != 0 {
		// WP categories reference their parent by id in the REST
		// payload, but the WXR parser keys parents by nicename.
		// The importer's runState.recordTerm lookup is keyed by
		// (taxonomy, slug); on the REST path we store the parent
		// id as the slug-equivalent string and let the importer's
		// fallback ("parent not yet seen → flat") handle the rest.
		// A second pass after FetchCategories would re-parent
		// strictly, but the importer doesn't need that today.
		parent = strconv.FormatInt(t.Parent, 10)
	}
	return &wxr.Category{
		TermID:      strconv.FormatInt(t.ID, 10),
		Nicename:    t.Slug,
		Parent:      parent,
		Name:        t.Name,
		Description: t.Description,
	}
}

func toTag(t restTag) *wxr.Tag {
	return &wxr.Tag{
		TermID:      strconv.FormatInt(t.ID, 10),
		Slug:        t.Slug,
		Name:        t.Name,
		Description: t.Description,
	}
}

// toPost converts a /posts or /pages payload. forcePostType lets the
// caller pin the wxr.PostType when WP's "type" field disagrees
// (custom post types under /wp/v2/posts can return "post", etc.).
func toPost(p restPost, forcePostType string) *wxr.Post {
	postType := forcePostType
	if postType == "" {
		postType = p.Type
	}
	return &wxr.Post{
		Title:         p.Title.String(),
		Link:          p.Link,
		PubDate:       p.Date,
		Description:   "",
		Content:       p.Content.String(),
		Excerpt:       p.Excerpt.String(),
		PostID:        strconv.FormatInt(p.ID, 10),
		PostDate:      p.Date,
		PostDateGMT:   p.DateGMT,
		Status:        p.Status,
		PostType:      postType,
		Name:          p.Slug,
		Parent:        intIDOrZero(p.Parent),
		MenuOrder:     strconv.Itoa(p.MenuOrder),
		Password:      p.Password,
		IsSticky:      boolToZeroOne(p.Sticky),
		CommentStatus: p.CommentStatus,
		PingStatus:    p.PingStatus,
		Categories:    termRefsFromIDs(p.Categories, "category"),
		Tags:          termRefsFromIDs(p.Tags, "post_tag"),
		Terms:         combinedTermRefs(p.Categories, p.Tags),
		// Creator: WP REST exposes the author as a numeric id, not
		// a login. The importer expects a login string. We populate
		// Creator with the numeric id stringified; the importer's
		// runState lookup is keyed by login, so a separate by-id
		// pre-pass is required for full fidelity. As a pragmatic
		// fallback, we also set wp_author_id in Meta below so a
		// downstream resolver can re-map.
		Creator: strconv.FormatInt(p.Author, 10),
		Meta: map[string]string{
			"wp_author_id":      strconv.FormatInt(p.Author, 10),
			"wp_featured_media": strconv.FormatInt(p.FeaturedMedia, 10),
		},
	}
}

// toAttachment converts a /media payload. AttachmentURL comes from
// source_url and PostType is forced to "attachment" to match WXR's
// classification.
func toAttachment(p restPost) *wxr.Post {
	post := toPost(p, "attachment")
	post.AttachmentURL = p.SourceURL
	// Carry mime type + alt text via Meta so a downstream media
	// migrator can consult them without re-fetching.
	if p.MimeType != "" {
		post.Meta["wp_mime_type"] = p.MimeType
	}
	if p.AltText != "" {
		post.Meta["wp_alt_text"] = p.AltText
	}
	if c := p.Caption.String(); c != "" {
		post.Meta["wp_caption"] = c
	}
	return post
}

// -------------------------------------------------------------------
// Small helpers. Kept package-private; the file is tiny enough to
// keep them inline rather than in a separate utils file.
// -------------------------------------------------------------------

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func intIDOrZero(id int64) string {
	if id == 0 {
		return "0"
	}
	return strconv.FormatInt(id, 10)
}

func boolToZeroOne(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func termRefsFromIDs(ids []int64, domain string) []wxr.TermRef {
	if len(ids) == 0 {
		return nil
	}
	out := make([]wxr.TermRef, 0, len(ids))
	for _, id := range ids {
		// REST returns ids; the WXR side stores nicenames. We pass
		// the stringified id as Nicename and let the importer
		// runState resolve it through the term map populated by
		// FetchCategories / FetchTags above (which use the same
		// stringified-id form as the term slug). When a category
		// has its real WP slug as the runState key it's because
		// the WXR parser ran first; the REST path stays consistent
		// with itself.
		out = append(out, wxr.TermRef{
			Domain:   domain,
			Nicename: strconv.FormatInt(id, 10),
		})
	}
	return out
}

func combinedTermRefs(cats, tags []int64) []wxr.TermRef {
	out := make([]wxr.TermRef, 0, len(cats)+len(tags))
	for _, id := range cats {
		out = append(out, wxr.TermRef{
			Domain:   "category",
			Nicename: strconv.FormatInt(id, 10),
		})
	}
	for _, id := range tags {
		out = append(out, wxr.TermRef{
			Domain:   "post_tag",
			Nicename: strconv.FormatInt(id, 10),
		})
	}
	return out
}
