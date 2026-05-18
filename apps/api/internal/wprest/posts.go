package wprest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
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

// -----------------------------------------------------------------------------
// Write path — request bodies
// -----------------------------------------------------------------------------

// wpPostWriteBody is the wire-format payload WP REST clients send on
// POST/PUT/PATCH /wp-json/wp/v2/posts (and /pages — the body shape is
// identical). All fields are pointer-typed so a caller can submit a
// sparse PATCH that touches only one column without us defaulting the
// rest to empty strings.
//
// Title and content arrive in either object form
// (`{"title": {"raw": "..."}}`) or string form (`{"title": "..."}`).
// We model the object variants as separate `*Raw` structs and pick
// whichever is set; a future PR can add full WP-style `protected`
// envelopes if a plugin needs them.
type wpPostWriteBody struct {
	// Slug is the URL slug. WP enforces lowercase + hyphen on the
	// server; we forward it verbatim and let the sink decide.
	Slug *string `json:"slug"`

	// Title arrives either as a plain string or {raw:"..."}. The
	// helper wpRawString picks whichever the client sent.
	Title   *wpRawString `json:"title"`
	Content *wpRawString `json:"content"`
	Excerpt *wpRawString `json:"excerpt"`

	// Status maps directly to the WP post status string ("publish",
	// "draft", "private", "pending", "future"). The sink translates
	// to the native status alphabet.
	Status *string `json:"status"`

	// Format / Template / CommentStatus / PingStatus are passed through
	// untouched. Sinks decide whether to honor unknown values.
	Format        *string `json:"format"`
	Template      *string `json:"template"`
	CommentStatus *string `json:"comment_status"`
	PingStatus    *string `json:"ping_status"`

	// Author is the WP legacy_int_id of the desired author. The shim
	// does not validate that the principal may write on the author's
	// behalf — that's an object-level policy concern handled by the
	// sink (typically a sink-side "edit_others_posts" cap check).
	Author *int `json:"author"`

	// FeaturedMedia is the legacy_int_id of the attachment row to mark
	// as featured. Unverified by the shim.
	FeaturedMedia *int `json:"featured_media"`

	Sticky *bool `json:"sticky"`

	// Categories and Tags are slices of legacy_int_ids. The shim
	// resolves them via TermSource (see resolveTerms) to surface
	// rest_term_invalid early for unknown ids.
	Categories *[]int `json:"categories"`
	Tags       *[]int `json:"tags"`

	// Date / DateGMT may carry a backdated or scheduled timestamp.
	// WP accepts RFC3339 or its YYYY-MM-DDTHH:MM:SS local form.
	Date    *time.Time `json:"date"`
	DateGMT *time.Time `json:"date_gmt"`

	// Password sets a per-post password.
	Password *string `json:"password"`

	// Meta is the WP meta envelope; we pass it through to the sink
	// without interpretation.
	Meta map[string]any `json:"meta"`
}

// wpRawString models the WP convention where text fields are either a
// plain string or an object `{raw: "...", rendered: "..."}`. Writers
// send `raw` (renderable) and we ignore any `rendered` they accidentally
// include — only the raw input is the source of truth.
type wpRawString struct {
	Raw      string
	Rendered string
}

// UnmarshalJSON accepts either a plain string or the {raw, rendered}
// object. Empty input is treated as the empty string.
func (s *wpRawString) UnmarshalJSON(data []byte) error {
	// Try string first — the common case.
	var asStr string
	if err := json.Unmarshal(data, &asStr); err == nil {
		s.Raw = asStr
		return nil
	}
	// Fall through to the {raw, rendered} object form.
	var asObj struct {
		Raw      string `json:"raw"`
		Rendered string `json:"rendered"`
	}
	if err := json.Unmarshal(data, &asObj); err != nil {
		return fmt.Errorf("wpRawString: %w", err)
	}
	s.Raw = asObj.Raw
	s.Rendered = asObj.Rendered
	return nil
}

// toPostWriteInput translates the wire body into the typed sink input.
// The translation is intentionally mechanical so the rules stay
// auditable: WP field name → native field name, with the few
// special-cases (title.raw → ContentHTML/Title plain) called out.
//
// The post type comes from the route (the caller already chose
// posts.go vs pages.go via dispatch), so we accept it as a parameter
// rather than reading it from the body.
func (b *wpPostWriteBody) toPostWriteInput(postType string) PostWriteInput {
	out := PostWriteInput{Type: postType}
	if b.Slug != nil {
		out.Slug = b.Slug
	}
	if b.Title != nil {
		v := b.Title.Raw
		out.Title = &v
	}
	if b.Content != nil {
		v := b.Content.Raw
		out.ContentHTML = &v
	}
	if b.Excerpt != nil {
		v := b.Excerpt.Raw
		out.ExcerptHTML = &v
	}
	if b.Status != nil {
		out.Status = b.Status
	}
	if b.Format != nil {
		out.Format = b.Format
	}
	if b.Template != nil {
		out.Template = b.Template
	}
	if b.CommentStatus != nil {
		out.CommentStatus = b.CommentStatus
	}
	if b.PingStatus != nil {
		out.PingStatus = b.PingStatus
	}
	if b.Author != nil {
		out.AuthorID = b.Author
	}
	if b.FeaturedMedia != nil {
		out.FeaturedMedia = b.FeaturedMedia
	}
	if b.Sticky != nil {
		out.Sticky = b.Sticky
	}
	if b.Categories != nil {
		out.Categories = b.Categories
	}
	if b.Tags != nil {
		out.Tags = b.Tags
	}
	if b.Date != nil {
		out.Date = b.Date
	}
	if b.DateGMT != nil {
		out.DateGMT = b.DateGMT
	}
	if b.Password != nil {
		out.Password = b.Password
	}
	if b.Meta != nil {
		out.Meta = b.Meta
	}
	return out
}

// -----------------------------------------------------------------------------
// Write handlers — posts
// -----------------------------------------------------------------------------

// createPost implements POST /wp-json/wp/v2/posts.
func (h *handlers) createPost(w http.ResponseWriter, r *http.Request) {
	h.createPostLike(w, r, h.deps.PostsSink, "post", policy.CapEditPosts, EventPostCreated, errCodeInvalidPostID)
}

// updatePost implements PUT/PATCH /wp-json/wp/v2/posts/{id}.
func (h *handlers) updatePost(w http.ResponseWriter, r *http.Request) {
	h.updatePostLike(w, r, h.deps.PostsSink, "post", policy.CapEditPosts, EventPostUpdated, errCodeInvalidPostID)
}

// deletePost implements DELETE /wp-json/wp/v2/posts/{id}.
func (h *handlers) deletePost(w http.ResponseWriter, r *http.Request) {
	h.deletePostLike(w, r, h.deps.PostsSink, "post", policy.CapDeletePosts, EventPostDeleted, errCodeInvalidPostID)
}

// createPage / updatePage / deletePage mirror the post handlers with
// page-flavored capability slugs and audit events. The dispatcher (sink,
// type, error code) is the only difference.
func (h *handlers) createPage(w http.ResponseWriter, r *http.Request) {
	h.createPostLike(w, r, h.deps.PagesSink, "page", policy.CapEditPages, EventPageCreated, errCodeInvalidPageID)
}
func (h *handlers) updatePage(w http.ResponseWriter, r *http.Request) {
	h.updatePostLike(w, r, h.deps.PagesSink, "page", policy.CapEditPages, EventPageUpdated, errCodeInvalidPageID)
}
func (h *handlers) deletePage(w http.ResponseWriter, r *http.Request) {
	h.deletePostLike(w, r, h.deps.PagesSink, "page", policy.CapDeletePages, EventPageDeleted, errCodeInvalidPageID)
}

// createPostLike is the shared body for posts/pages create. Each step
// is short and skippable on failure — the layout matches the gate order
// in the issue spec: nonce → principal → capability → decode → resolve
// terms → call sink → audit → write response.
func (h *handlers) createPostLike(w http.ResponseWriter, r *http.Request, sink PostSink, typ string, requiredCap policy.Capability, eventType, errIDCode string) {
	if contextDone(r.Context()) {
		return
	}
	if !h.requireNonce(w, r) {
		return
	}
	pr, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if !h.requireCapability(w, pr, requiredCap, nil) {
		return
	}

	var body wpPostWriteBody
	if !decodeWriteBody(w, r, &body) {
		return
	}
	in := body.toPostWriteInput(typ)

	// Resolve term references up front so the WP error
	// (rest_term_invalid, 400) surfaces before we burn cycles in the
	// sink. The translator runs only the lookups; the sink is still
	// free to re-validate against its own state if it stores ids in
	// the row.
	if err := h.resolveTermRefs(r.Context(), in); err != nil {
		writeError(w, http.StatusBadRequest, errCodeInvalidTermID,
			"Invalid term reference.")
		return
	}

	row, err := sink.Create(r.Context(), pr.UserID, in)
	if err != nil {
		h.writeSinkError(w, r, err, errIDCode, errCodeCannotCreate)
		return
	}

	h.emitAudit(r.Context(), pr, eventType, typ, strconv.Itoa(row.LegacyID), map[string]any{
		"status": row.Status,
		"slug":   row.Slug,
	})

	env := h.toWPEnvelope(row)
	h.writeJSON(w, http.StatusCreated, env)
}

// updatePostLike — shared body for PUT/PATCH on posts/pages.
func (h *handlers) updatePostLike(w http.ResponseWriter, r *http.Request, sink PostSink, typ string, requiredCap policy.Capability, eventType, errIDCode string) {
	if contextDone(r.Context()) {
		return
	}
	if !h.requireNonce(w, r) {
		return
	}
	pr, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if !h.requireCapability(w, pr, requiredCap, nil) {
		return
	}

	id, ok := parseIDFromPath(w, r, errIDCode, "Invalid post ID.")
	if !ok {
		return
	}

	var body wpPostWriteBody
	if !decodeWriteBody(w, r, &body) {
		return
	}
	in := body.toPostWriteInput(typ)

	if err := h.resolveTermRefs(r.Context(), in); err != nil {
		writeError(w, http.StatusBadRequest, errCodeInvalidTermID,
			"Invalid term reference.")
		return
	}

	row, err := sink.Update(r.Context(), pr.UserID, id, in)
	if err != nil {
		h.writeSinkError(w, r, err, errIDCode, errCodeCannotEdit)
		return
	}

	h.emitAudit(r.Context(), pr, eventType, typ, strconv.Itoa(row.LegacyID), map[string]any{
		"status": row.Status,
		"slug":   row.Slug,
	})

	env := h.toWPEnvelope(row)
	h.writeJSON(w, http.StatusOK, env)
}

// deletePostLike — shared body for DELETE on posts/pages. The response
// is the WP-shaped `{deleted: true, previous: {...}}` envelope, which
// is what the @wordpress/api-fetch client expects on a 200.
func (h *handlers) deletePostLike(w http.ResponseWriter, r *http.Request, sink PostSink, typ string, requiredCap policy.Capability, eventType, errIDCode string) {
	if contextDone(r.Context()) {
		return
	}
	if !h.requireNonce(w, r) {
		return
	}
	pr, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if !h.requireCapability(w, pr, requiredCap, nil) {
		return
	}

	id, ok := parseIDFromPath(w, r, errIDCode, "Invalid post ID.")
	if !ok {
		return
	}

	row, err := sink.Delete(r.Context(), pr.UserID, id)
	if err != nil {
		h.writeSinkError(w, r, err, errIDCode, errCodeCannotDelete)
		return
	}

	h.emitAudit(r.Context(), pr, eventType, typ, strconv.Itoa(row.LegacyID), map[string]any{
		"slug": row.Slug,
	})

	// WP-shape: `{deleted: bool, previous: <envelope>}`. The envelope is
	// what the row looked like immediately before deletion — clients
	// rely on it to repaint a soft-trashed item or to undo.
	h.writeJSON(w, http.StatusOK, map[string]any{
		"deleted":  true,
		"previous": h.toWPEnvelope(row),
	})
}

// resolveTermRefs verifies that every id in in.Categories and in.Tags
// resolves through the wired TermSource. Missing refs short-circuit
// with ErrInvalidTerm so the WP error surfaces. When a source is nil
// (the test deployment) we skip — the sink is then responsible for any
// validation it cares about.
func (h *handlers) resolveTermRefs(ctx context.Context, in PostWriteInput) error {
	if in.Categories != nil && h.deps.Categories != nil {
		for _, id := range *in.Categories {
			if _, err := h.deps.Categories.GetByLegacyID(ctx, id); err != nil {
				return ErrInvalidTerm
			}
		}
	}
	if in.Tags != nil && h.deps.Tags != nil {
		for _, id := range *in.Tags {
			if _, err := h.deps.Tags.GetByLegacyID(ctx, id); err != nil {
				return ErrInvalidTerm
			}
		}
	}
	return nil
}

// writeSinkError maps sink-layer sentinels to WP-shaped responses. Any
// unknown error is treated as an internal failure and the body shape
// matches what live WP emits on the same error class.
//
// invalidIDCode is the per-route id-error code (rest_post_invalid_id /
// rest_page_invalid_id / rest_user_invalid_id). cannotXCode is the
// per-route capability code (rest_cannot_create / _edit / _delete);
// callers pass the one matching the verb.
func (h *handlers) writeSinkError(w http.ResponseWriter, r *http.Request, err error, invalidIDCode, cannotXCode string) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, invalidIDCode, "Resource not found.")
	case errors.Is(err, ErrInvalidTerm):
		writeError(w, http.StatusBadRequest, errCodeInvalidTermID,
			"Invalid term reference.")
	case errors.Is(err, ErrInvalidInput):
		writeError(w, http.StatusBadRequest, errCodeInvalidParam,
			"Invalid parameter(s).")
	case errors.Is(err, ErrDuplicate):
		writeError(w, http.StatusConflict, errCodePostExists,
			"A resource with that identifier already exists.")
	default:
		h.deps.Logger.ErrorContext(r.Context(), "wprest: sink error",
			slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, cannotXCode,
			"The resource could not be saved.")
	}
}
