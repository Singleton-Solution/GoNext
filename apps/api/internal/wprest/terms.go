package wprest

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// wpTermEnvelope is the WP wire shape for a taxonomy term — both
// `/wp/v2/categories/*` and `/wp/v2/tags/*` use it. The taxonomy
// discriminator lives inside the envelope itself (`taxonomy` field) so a
// client that fetched a term via _embedded can still tell which route
// to use for follow-ups.
type wpTermEnvelope struct {
	ID          int                  `json:"id"`
	Count       int                  `json:"count"`
	Description string               `json:"description"`
	Link        string               `json:"link"`
	Name        string               `json:"name"`
	Slug        string               `json:"slug"`
	Taxonomy    string               `json:"taxonomy"`
	Parent      int                  `json:"parent,omitempty"`
	Meta        map[string]any       `json:"meta,omitempty"`
	Links       map[string][]hrefMap `json:"_links"`
}

// listCategories implements GET /wp-json/wp/v2/categories.
func (h *handlers) listCategories(w http.ResponseWriter, r *http.Request) {
	h.listTerms(w, r, h.deps.Categories, "category", "categories")
}

// listTags implements GET /wp-json/wp/v2/tags.
func (h *handlers) listTags(w http.ResponseWriter, r *http.Request) {
	h.listTerms(w, r, h.deps.Tags, "post_tag", "tags")
}

// getCategory implements GET /wp-json/wp/v2/categories/{id}.
func (h *handlers) getCategory(w http.ResponseWriter, r *http.Request) {
	h.getTerm(w, r, h.deps.Categories, "category")
}

// getTag implements GET /wp-json/wp/v2/tags/{id}.
func (h *handlers) getTag(w http.ResponseWriter, r *http.Request) {
	h.getTerm(w, r, h.deps.Tags, "post_tag")
}

// listTerms backs both listCategories and listTags. The taxonomy
// parameter is set on every emitted envelope (categories→"category",
// tags→"post_tag") so embeds and client-side routing both work.
func (h *handlers) listTerms(w http.ResponseWriter, r *http.Request, src TermSource, taxonomy, _ string) {
	if contextDone(r.Context()) {
		return
	}
	q, badField, ok := parseWPQuery(r)
	if !ok {
		writeError(w, http.StatusBadRequest, errCodeInvalidParam,
			fmt.Sprintf("Invalid parameter(s): %s", badField))
		return
	}

	if src == nil {
		writePaginationHeaders(w, 0, q.PerPage)
		h.writeJSON(w, http.StatusOK, []wpTermEnvelope{})
		return
	}

	rows, err := src.List(r.Context())
	if err != nil {
		h.deps.Logger.ErrorContext(r.Context(), "wprest: terms list failed",
			slog.String("taxonomy", taxonomy), slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "rest_error", "internal error")
		return
	}

	total := len(rows)
	page := applyPagination(rows, q.Page, q.PerPage)
	out := make([]wpTermEnvelope, 0, len(page))
	for _, t := range page {
		// Defensive: stamp the taxonomy from the route, in case the
		// source returned blank or wrong values. The route is the
		// ground truth at this dispatch layer.
		if t.Taxonomy == "" {
			t.Taxonomy = taxonomy
		}
		out = append(out, h.toWPTermEnvelope(t))
	}
	writePaginationHeaders(w, total, q.PerPage)
	h.writeJSON(w, http.StatusOK, out)
}

// getTerm backs both getCategory and getTag.
func (h *handlers) getTerm(w http.ResponseWriter, r *http.Request, src TermSource, taxonomy string) {
	if contextDone(r.Context()) {
		return
	}
	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id < 1 {
		writeError(w, http.StatusNotFound, errCodeInvalidTermID, "Term does not exist.")
		return
	}
	if src == nil {
		writeError(w, http.StatusNotFound, errCodeInvalidTermID, "Term does not exist.")
		return
	}

	t, err := src.GetByLegacyID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, errCodeInvalidTermID, "Term does not exist.")
			return
		}
		h.deps.Logger.ErrorContext(r.Context(), "wprest: get term failed",
			slog.String("taxonomy", taxonomy), slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "rest_error", "internal error")
		return
	}
	if t.Taxonomy == "" {
		t.Taxonomy = taxonomy
	}
	h.writeJSON(w, http.StatusOK, h.toWPTermEnvelope(t))
}

// -----------------------------------------------------------------------------
// Write path — terms (categories + tags)
// -----------------------------------------------------------------------------

// wpTermWriteBody is the JSON body for POST/PUT/PATCH on /wp/v2/categories
// and /wp/v2/tags. Tag bodies omit Parent — the field is accepted but
// ignored by the tag sink (post_tag is a flat taxonomy in WP).
type wpTermWriteBody struct {
	Name        *string `json:"name"`
	Slug        *string `json:"slug"`
	Description *string `json:"description"`
	Parent      *int    `json:"parent"`
}

func (b *wpTermWriteBody) toTermWriteInput() TermWriteInput {
	return TermWriteInput{
		Name:        b.Name,
		Slug:        b.Slug,
		Description: b.Description,
		Parent:      b.Parent,
	}
}

// createCategory / createTag share createTerm. Each calls in with the
// dispatcher (sink + taxonomy + cap).
func (h *handlers) createCategory(w http.ResponseWriter, r *http.Request) {
	h.createTerm(w, r, h.deps.CategoriesSink, "category", policy.CapManageCategories)
}
func (h *handlers) createTag(w http.ResponseWriter, r *http.Request) {
	h.createTerm(w, r, h.deps.TagsSink, "post_tag", policy.CapManageTags)
}
func (h *handlers) updateCategory(w http.ResponseWriter, r *http.Request) {
	h.updateTerm(w, r, h.deps.CategoriesSink, "category", policy.CapManageCategories)
}
func (h *handlers) updateTag(w http.ResponseWriter, r *http.Request) {
	h.updateTerm(w, r, h.deps.TagsSink, "post_tag", policy.CapManageTags)
}
func (h *handlers) deleteCategory(w http.ResponseWriter, r *http.Request) {
	h.deleteTerm(w, r, h.deps.CategoriesSink, "category", policy.CapManageCategories)
}
func (h *handlers) deleteTag(w http.ResponseWriter, r *http.Request) {
	h.deleteTerm(w, r, h.deps.TagsSink, "post_tag", policy.CapManageTags)
}

// createTerm — shared body for category/tag create.
func (h *handlers) createTerm(w http.ResponseWriter, r *http.Request, sink TermSink, taxonomy string, requiredCap policy.Capability) {
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

	var body wpTermWriteBody
	if !decodeWriteBody(w, r, &body) {
		return
	}
	in := body.toTermWriteInput()

	row, err := sink.Create(r.Context(), pr.UserID, taxonomy, in)
	if err != nil {
		h.writeTermSinkError(w, r, err, errCodeCannotCreate)
		return
	}
	if row.Taxonomy == "" {
		row.Taxonomy = taxonomy
	}

	h.emitAudit(r.Context(), pr, EventTermCreated, taxonomy, strconv.Itoa(row.LegacyID), map[string]any{
		"slug": row.Slug,
		"name": row.Name,
	})

	h.writeJSON(w, http.StatusCreated, h.toWPTermEnvelope(row))
}

// updateTerm — shared body for category/tag PUT/PATCH.
func (h *handlers) updateTerm(w http.ResponseWriter, r *http.Request, sink TermSink, taxonomy string, requiredCap policy.Capability) {
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

	id, ok := parseIDFromPath(w, r, errCodeInvalidTermID, "Term does not exist.")
	if !ok {
		return
	}

	var body wpTermWriteBody
	if !decodeWriteBody(w, r, &body) {
		return
	}
	in := body.toTermWriteInput()

	row, err := sink.Update(r.Context(), pr.UserID, taxonomy, id, in)
	if err != nil {
		h.writeTermSinkError(w, r, err, errCodeCannotEdit)
		return
	}
	if row.Taxonomy == "" {
		row.Taxonomy = taxonomy
	}

	h.emitAudit(r.Context(), pr, EventTermUpdated, taxonomy, strconv.Itoa(row.LegacyID), map[string]any{
		"slug": row.Slug,
		"name": row.Name,
	})

	h.writeJSON(w, http.StatusOK, h.toWPTermEnvelope(row))
}

// deleteTerm — shared body for category/tag DELETE. Live WP supports
// `?force=true` to permanently delete a term; the shim forwards the
// query param via the sink interface (sinks may interpret).
func (h *handlers) deleteTerm(w http.ResponseWriter, r *http.Request, sink TermSink, taxonomy string, requiredCap policy.Capability) {
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

	id, ok := parseIDFromPath(w, r, errCodeInvalidTermID, "Term does not exist.")
	if !ok {
		return
	}

	row, err := sink.Delete(r.Context(), pr.UserID, taxonomy, id)
	if err != nil {
		h.writeTermSinkError(w, r, err, errCodeCannotDelete)
		return
	}
	if row.Taxonomy == "" {
		row.Taxonomy = taxonomy
	}

	h.emitAudit(r.Context(), pr, EventTermDeleted, taxonomy, strconv.Itoa(row.LegacyID), map[string]any{
		"slug": row.Slug,
	})

	h.writeJSON(w, http.StatusOK, map[string]any{
		"deleted":  true,
		"previous": h.toWPTermEnvelope(row),
	})
}

// writeTermSinkError maps term-sink errors to WP-shaped responses.
func (h *handlers) writeTermSinkError(w http.ResponseWriter, r *http.Request, err error, cannotXCode string) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, errCodeInvalidTermID, "Term does not exist.")
	case errors.Is(err, ErrInvalidInput):
		writeError(w, http.StatusBadRequest, errCodeInvalidParam, "Invalid parameter(s).")
	case errors.Is(err, ErrDuplicate):
		writeError(w, http.StatusConflict, errCodeTermExists, "A term with that slug already exists.")
	default:
		h.deps.Logger.ErrorContext(r.Context(), "wprest: term sink error",
			slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, cannotXCode, "The term could not be saved.")
	}
}

// toWPTermEnvelope translates a TermRow into the WP wire shape.
func (h *handlers) toWPTermEnvelope(t TermRow) wpTermEnvelope {
	collection := "category"
	if t.Taxonomy == "post_tag" {
		collection = "tag"
	}
	link := fmt.Sprintf("%s/%s/%s/", h.deps.SiteURL, collection, t.Slug)
	return wpTermEnvelope{
		ID:          t.LegacyID,
		Count:       t.Count,
		Description: t.Description,
		Link:        link,
		Name:        t.Name,
		Slug:        t.Slug,
		Taxonomy:    t.Taxonomy,
		Parent:      t.Parent,
		Links:       h.linksForTerm(t),
	}
}
