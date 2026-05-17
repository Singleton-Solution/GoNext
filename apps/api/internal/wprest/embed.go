package wprest

import (
	"context"
	"errors"
	"fmt"
)

// HAL-style discovery + embedding.
//
// WP REST has two complementary mechanisms:
//
//   - `_links` is always present on a resource. It is a map of relation
//     keys (e.g. "self", "author", "wp:term") to one or more `{href: ...}`
//     entries. Clients use _links to discover related resources without
//     hardcoding URLs.
//
//   - `_embedded` is OPT-IN: it's only populated when the request carries
//     `?_embed` (any value). When present, the server expands the embeddable
//     relations into inline copies of the referenced resources, saving the
//     client a round-trip per related entity. The author and term sets are
//     the canonical embeddable relations; we mirror that here.
//
// The shim builds _links unconditionally and _embedded only when q.Embed
// is true. Both shapes match live WP exactly enough for the
// @wordpress/api-fetch JS client to consume.

// hrefMap is one entry in a _links relation list. WP allows extra keys
// per relation (e.g. "embeddable", "templated") — we model that with
// `omitempty` fields so the JSON stays minimal when the flags are
// defaults.
type hrefMap struct {
	Href       string `json:"href"`
	Embeddable bool   `json:"embeddable,omitempty"`
	Templated  bool   `json:"templated,omitempty"`
	Taxonomy   string `json:"taxonomy,omitempty"`
}

// linksFor builds the WP `_links` map for a post/page resource.
// Relations are namespaced per WP convention ("wp:..." for internal-WP
// resources, "self" / "collection" for HAL standard).
func (h *handlers) linksFor(p PostRow) map[string][]hrefMap {
	base := h.deps.SiteURL + BasePath + "/wp/v2"
	collection := "posts"
	if p.Type == "page" {
		collection = "pages"
	}

	links := map[string][]hrefMap{
		"self":       {{Href: fmt.Sprintf("%s/%s/%d", base, collection, p.LegacyID)}},
		"collection": {{Href: fmt.Sprintf("%s/%s", base, collection)}},
		"about":      {{Href: fmt.Sprintf("%s/types/%s", base, p.Type)}},
		"author": {{
			Href:       fmt.Sprintf("%s/users/%d", base, p.AuthorID),
			Embeddable: true,
		}},
	}

	// wp:term lists one entry per taxonomy the post is in. Live WP
	// returns this even when the post has no terms — clients rely on
	// the presence of the relation to drive their term-lookup logic.
	wpTerm := []hrefMap{
		{
			Href:       fmt.Sprintf("%s/categories?post=%d", base, p.LegacyID),
			Embeddable: true,
			Taxonomy:   "category",
		},
		{
			Href:       fmt.Sprintf("%s/tags?post=%d", base, p.LegacyID),
			Embeddable: true,
			Taxonomy:   "post_tag",
		},
	}
	links["wp:term"] = wpTerm

	if p.FeaturedMedia > 0 {
		links["wp:featuredmedia"] = []hrefMap{{
			Href:       fmt.Sprintf("%s/media/%d", base, p.FeaturedMedia),
			Embeddable: true,
		}}
	}

	return links
}

// linksForUser builds the _links map for a user resource. Smaller than
// the post link set — users only have self + collection.
func (h *handlers) linksForUser(u UserRow) map[string][]hrefMap {
	base := h.deps.SiteURL + BasePath + "/wp/v2"
	return map[string][]hrefMap{
		"self":       {{Href: fmt.Sprintf("%s/users/%d", base, u.LegacyID)}},
		"collection": {{Href: fmt.Sprintf("%s/users", base)}},
	}
}

// linksForTerm builds the _links map for a term resource.
func (h *handlers) linksForTerm(t TermRow) map[string][]hrefMap {
	base := h.deps.SiteURL + BasePath + "/wp/v2"
	collection := "categories"
	if t.Taxonomy == "post_tag" {
		collection = "tags"
	}
	return map[string][]hrefMap{
		"self":       {{Href: fmt.Sprintf("%s/%s/%d", base, collection, t.LegacyID)}},
		"collection": {{Href: fmt.Sprintf("%s/%s", base, collection)}},
		"about":      {{Href: fmt.Sprintf("%s/taxonomies/%s", base, t.Taxonomy)}},
	}
}

// embedFor builds the WP `_embedded` map for a post/page when the
// request carried ?_embed. The map mirrors the embeddable subset of
// _links: "author" expands to a single-entry array of user envelopes,
// "wp:term" expands to one entry per taxonomy, each an array of term
// envelopes.
//
// Missing related entities (e.g. an author legacy_id that no longer
// resolves) are tolerated: the entry slot is filled with a WP-shaped
// stub `{code:"rest_not_found", message:"...", data:{status:404}}`,
// which is exactly what live WP does. The client knows how to skip those
// stubs.
func (h *handlers) embedFor(ctx context.Context, p PostRow) map[string]any {
	embedded := map[string]any{}

	// author: always one entry.
	if h.deps.Users != nil && p.AuthorID > 0 {
		if u, err := h.deps.Users.GetByLegacyID(ctx, p.AuthorID); err == nil {
			embedded["author"] = []any{h.toWPUserEnvelope(u, false)}
		} else if errors.Is(err, ErrNotFound) {
			embedded["author"] = []any{embedNotFound("rest_user_invalid_id", "Invalid user ID.")}
		}
		// Other errors: omit the relation entirely so the client falls
		// back to a separate fetch. Logging is the dispatcher's job.
	} else if p.AuthorID > 0 {
		embedded["author"] = []any{embedNotFound("rest_user_invalid_id", "Invalid user ID.")}
	}

	// wp:term: an array per taxonomy, each itself an array.
	terms := make([]any, 0, 2)
	terms = append(terms, h.resolveTermSet(ctx, p.Categories, h.deps.Categories))
	terms = append(terms, h.resolveTermSet(ctx, p.Tags, h.deps.Tags))
	embedded["wp:term"] = terms

	return embedded
}

// resolveTermSet looks up each id in src and returns the resulting term
// envelopes as a []any. Missing src or missing ids degrade gracefully —
// the slot is included but empty / contains rest_not_found stubs.
func (h *handlers) resolveTermSet(ctx context.Context, ids []int, src TermSource) []any {
	out := make([]any, 0, len(ids))
	if src == nil {
		return out
	}
	for _, id := range ids {
		t, err := src.GetByLegacyID(ctx, id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				out = append(out, embedNotFound("rest_term_invalid", "Term does not exist."))
				continue
			}
			// Other errors: skip silently — keep the response intact.
			continue
		}
		out = append(out, h.toWPTermEnvelope(t))
	}
	return out
}

// embedNotFound builds the WP "missing embedded entity" placeholder.
// Live WP emits this whenever a _links relation resolves to a deleted
// or restricted resource; clients check the `code` field to skip.
func embedNotFound(code, message string) map[string]any {
	return map[string]any{
		"code":    code,
		"message": message,
		"data":    map[string]any{"status": 404},
	}
}
