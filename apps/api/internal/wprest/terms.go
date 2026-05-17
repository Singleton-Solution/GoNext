package wprest

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
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
