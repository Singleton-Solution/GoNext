package wprest

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// wpPostEnvelope is the on-the-wire shape for a single post/page. Field
// names and types mirror live WP exactly — adding/removing fields here
// is part of the compatibility contract.
//
// The struct is annotated for json so the field order in the marshal
// output matches what live WP emits. Some WP clients are field-order
// tolerant; others (older curl-based scrapers) are not, hence the
// explicit ordering.
type wpPostEnvelope struct {
	ID            int                    `json:"id"`
	Date          string                 `json:"date"`
	DateGMT       string                 `json:"date_gmt"`
	GUID          wpRendered             `json:"guid"`
	Modified      string                 `json:"modified"`
	ModifiedGMT   string                 `json:"modified_gmt"`
	Slug          string                 `json:"slug"`
	Status        string                 `json:"status"`
	Type          string                 `json:"type"`
	Link          string                 `json:"link"`
	Title         wpRendered             `json:"title"`
	Content       wpProtectedRendered    `json:"content"`
	Excerpt       wpProtectedRendered    `json:"excerpt"`
	Author        int                    `json:"author"`
	FeaturedMedia int                    `json:"featured_media"`
	CommentStatus string                 `json:"comment_status"`
	PingStatus    string                 `json:"ping_status"`
	Sticky        bool                   `json:"sticky"`
	Template      string                 `json:"template"`
	Format        string                 `json:"format"`
	Meta          map[string]any         `json:"meta"`
	Categories    []int                  `json:"categories"`
	Tags          []int                  `json:"tags"`
	Links         map[string][]hrefMap   `json:"_links"`
	Embedded      map[string]any         `json:"_embedded,omitempty"`
	Extra         map[string]interface{} `json:"-"` // reserved for plugin fields, unused in #89
}

// wpRendered is the WP envelope for a "rendered" string field — title,
// excerpt without protection, guid. Live WP emits a single `rendered`
// key; `raw` is omitted on unauthenticated reads.
type wpRendered struct {
	Rendered string `json:"rendered"`
}

// wpProtectedRendered is the rendered envelope plus a `protected`
// boolean (content, excerpt). Live WP includes the boolean on every
// post regardless of protection state, so we do too.
type wpProtectedRendered struct {
	Rendered  string `json:"rendered"`
	Protected bool   `json:"protected"`
}

// listPosts implements GET /wp-json/wp/v2/posts.
func (h *handlers) listPosts(w http.ResponseWriter, r *http.Request) {
	h.listGeneric(w, r, h.deps.Posts, "post")
}

// listPages implements GET /wp-json/wp/v2/pages.
func (h *handlers) listPages(w http.ResponseWriter, r *http.Request) {
	h.listGeneric(w, r, h.deps.Pages, "page")
}

// getPost implements GET /wp-json/wp/v2/posts/{id}.
func (h *handlers) getPost(w http.ResponseWriter, r *http.Request) {
	h.getGeneric(w, r, h.deps.Posts, "post", errCodeInvalidPostID)
}

// getPage implements GET /wp-json/wp/v2/pages/{id}.
func (h *handlers) getPage(w http.ResponseWriter, r *http.Request) {
	h.getGeneric(w, r, h.deps.Pages, "page", errCodeInvalidPageID)
}

// listGeneric backs both listPosts and listPages — the WP envelope is
// identical for both, only the source and the `type` field differ.
func (h *handlers) listGeneric(w http.ResponseWriter, r *http.Request, src PostSource, typ string) {
	if contextDone(r.Context()) {
		return
	}
	q, badField, ok := parseWPQuery(r)
	if !ok {
		writeError(w, http.StatusBadRequest, errCodeInvalidParam,
			fmt.Sprintf("Invalid parameter(s): %s", badField))
		return
	}

	filter := PostFilter{
		Search:     q.Search,
		Slug:       q.Slug,
		Statuses:   q.Statuses,
		Categories: q.Cats,
		Tags:       q.Tags,
		OrderBy:    q.OrderBy,
		Order:      q.Order,
	}
	rows, err := src.List(r.Context(), filter)
	if err != nil {
		h.deps.Logger.ErrorContext(r.Context(), "wprest: list failed",
			slog.String("type", typ), slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "rest_error", "internal error")
		return
	}

	// Sticky posts in live WP float to the top within their page. The
	// store has already applied the requested order; we re-sort only
	// for "date desc" (the WP default) to match the WP behavior exactly.
	// Other order-bys preserve the store's order.
	if q.OrderBy == "date" && q.Order == "desc" {
		rows = floatSticky(rows)
	}

	total := len(rows)
	page := applyPagination(rows, q.Page, q.PerPage)

	out := make([]wpPostEnvelope, 0, len(page))
	for _, row := range page {
		env := h.toWPEnvelope(row)
		if q.Embed {
			env.Embedded = h.embedFor(r.Context(), row)
		}
		out = append(out, env)
	}

	writePaginationHeaders(w, total, q.PerPage)
	h.writeJSON(w, http.StatusOK, out)
}

// getGeneric backs both getPost and getPage. The errCode parameter lets
// each route emit the correct WP error literal on 404
// ("rest_post_invalid_id" vs "rest_page_invalid_id").
func (h *handlers) getGeneric(w http.ResponseWriter, r *http.Request, src PostSource, typ, errCode string) {
	if contextDone(r.Context()) {
		return
	}
	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id < 1 {
		writeError(w, http.StatusNotFound, errCode, "Invalid post ID.")
		return
	}

	row, err := src.GetByLegacyID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, errCode, "Invalid post ID.")
			return
		}
		h.deps.Logger.ErrorContext(r.Context(), "wprest: get failed",
			slog.String("type", typ), slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "rest_error", "internal error")
		return
	}

	// Defensive: the source should only return rows whose Type matches
	// what the route expects, but a misconfigured implementation would
	// otherwise leak a page through the /posts/{id} endpoint. The 404
	// here matches WP's "post type does not allow this method" code.
	if row.Type != typ {
		writeError(w, http.StatusNotFound, errCode, "Invalid post ID.")
		return
	}

	parsed, _, _ := parseWPQuery(r)
	env := h.toWPEnvelope(row)
	if parsed.Embed {
		env.Embedded = h.embedFor(r.Context(), row)
	}
	h.writeJSON(w, http.StatusOK, env)
}

// toWPEnvelope translates a PostRow into the WP wire shape. The
// translator is the heart of the shim — every field has a documented
// mapping in docs/08-migration-compat.md §11.2.
func (h *handlers) toWPEnvelope(p PostRow) wpPostEnvelope {
	link := fmt.Sprintf("%s/?p=%d", h.deps.SiteURL, p.LegacyID)
	if p.Slug != "" {
		// Live WP uses the slug in the canonical link when permalinks
		// are on. The shim emits `/<slug>/` to match the most common
		// permalink structure; production wiring can override this
		// once we plumb the permalink option here.
		link = fmt.Sprintf("%s/%s/", h.deps.SiteURL, p.Slug)
	}
	return wpPostEnvelope{
		ID:            p.LegacyID,
		Date:          formatWPDate(p.Date),
		DateGMT:       formatWPDate(p.DateGMT),
		GUID:          wpRendered{Rendered: fmt.Sprintf("%s/?p=%d", h.deps.SiteURL, p.LegacyID)},
		Modified:      formatWPDate(p.Modified),
		ModifiedGMT:   formatWPDate(p.ModifiedGMT),
		Slug:          p.Slug,
		Status:        p.Status,
		Type:          p.Type,
		Link:          link,
		Title:         wpRendered{Rendered: p.Title},
		Content:       wpProtectedRendered{Rendered: p.ContentHTML, Protected: p.Protected},
		Excerpt:       wpProtectedRendered{Rendered: p.ExcerptHTML, Protected: p.Protected},
		Author:        p.AuthorID,
		FeaturedMedia: p.FeaturedMedia,
		CommentStatus: orDefault(p.CommentStatus, "open"),
		PingStatus:    orDefault(p.PingStatus, "open"),
		Sticky:        p.Sticky,
		Template:      p.Template,
		Format:        orDefault(p.Format, "standard"),
		Meta:          map[string]any{},
		Categories:    nonNilInts(p.Categories),
		Tags:          nonNilInts(p.Tags),
		Links:         h.linksFor(p),
		// Embedded is set by the caller when q.Embed is true.
	}
}

// formatWPDate renders a time.Time in the WP canonical layout
// "2006-01-02T15:04:05". Live WP omits the timezone offset on the local
// `date` field (it's implicitly site-local) and emits the GMT variant
// in the same layout — both encode the same calendar slot.
func formatWPDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02T15:04:05")
}

// orDefault returns v when non-empty, else def. Used for fields where
// WP emits a nonzero default (comment_status defaults to "open" if the
// option isn't set, etc.).
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// nonNilInts ensures the JSON output is `[]` rather than `null` for an
// empty slice. WP clients sometimes break on `null` for these fields.
func nonNilInts(in []int) []int {
	if in == nil {
		return []int{}
	}
	return in
}

// floatSticky moves sticky=true rows ahead of the rest while preserving
// relative order within each group. This is a faithful reproduction of
// the WP-core "stick this post to the front page" behavior on the REST
// listing. Tests cover the ordering invariant.
func floatSticky(rows []PostRow) []PostRow {
	if len(rows) <= 1 {
		return rows
	}
	stickies := make([]PostRow, 0, len(rows))
	rest := make([]PostRow, 0, len(rows))
	for _, r := range rows {
		if r.Sticky {
			stickies = append(stickies, r)
		} else {
			rest = append(rest, r)
		}
	}
	return append(stickies, rest...)
}

// writeJSON is the success-path response writer. Kept distinct from the
// error path (errors.go::writeError) so we can vary Content-Type, body
// shape, and header semantics independently.
func (h *handlers) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}
