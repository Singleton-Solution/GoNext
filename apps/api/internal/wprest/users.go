package wprest

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
)

// wpUserEnvelope is the public projection of a user that the
// /wp-json/wp/v2/users surface emits.
//
// We deliberately omit private fields (email, roles, capabilities,
// extra_capabilities, registered_date) in this read-only PR — those are
// gated behind authentication, which is not in scope for #89. The set
// here matches what unauthenticated `/wp-json/wp/v2/users` returns on a
// live WP install with the default permission policy.
type wpUserEnvelope struct {
	ID          int                  `json:"id"`
	Name        string               `json:"name"`
	URL         string               `json:"url"`
	Description string               `json:"description"`
	Link        string               `json:"link"`
	Slug        string               `json:"slug"`
	AvatarURLs  map[string]string    `json:"avatar_urls"`
	Meta        map[string]any       `json:"meta,omitempty"`
	Links       map[string][]hrefMap `json:"_links"`
	Embedded    map[string]any       `json:"_embedded,omitempty"`
}

// listUsers implements GET /wp-json/wp/v2/users.
//
// When Deps.Users is nil (the public-users surface is disabled), this
// returns an empty array + the standard pagination headers. Live WP
// returns 401 in that case; we choose a quieter degradation that
// doesn't break migration cohort dashboards which probe the endpoint
// existence.
func (h *handlers) listUsers(w http.ResponseWriter, r *http.Request) {
	if contextDone(r.Context()) {
		return
	}
	q, badField, ok := parseWPQuery(r)
	if !ok {
		writeError(w, http.StatusBadRequest, errCodeInvalidParam,
			fmt.Sprintf("Invalid parameter(s): %s", badField))
		return
	}

	if h.deps.Users == nil {
		writePaginationHeaders(w, 0, q.PerPage)
		h.writeJSON(w, http.StatusOK, []wpUserEnvelope{})
		return
	}

	rows, err := h.deps.Users.List(r.Context())
	if err != nil {
		h.deps.Logger.ErrorContext(r.Context(), "wprest: users list failed",
			slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "rest_error", "internal error")
		return
	}

	total := len(rows)
	page := applyPagination(rows, q.Page, q.PerPage)
	out := make([]wpUserEnvelope, 0, len(page))
	for _, u := range page {
		out = append(out, h.toWPUserEnvelope(u, q.Embed))
	}
	writePaginationHeaders(w, total, q.PerPage)
	h.writeJSON(w, http.StatusOK, out)
}

// getUser implements GET /wp-json/wp/v2/users/{id}.
func (h *handlers) getUser(w http.ResponseWriter, r *http.Request) {
	if contextDone(r.Context()) {
		return
	}
	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id < 1 {
		writeError(w, http.StatusNotFound, errCodeInvalidUserID, "Invalid user ID.")
		return
	}

	if h.deps.Users == nil {
		writeError(w, http.StatusNotFound, errCodeInvalidUserID, "Invalid user ID.")
		return
	}

	u, err := h.deps.Users.GetByLegacyID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, errCodeInvalidUserID, "Invalid user ID.")
			return
		}
		h.deps.Logger.ErrorContext(r.Context(), "wprest: get user failed",
			slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "rest_error", "internal error")
		return
	}

	q, _, _ := parseWPQuery(r)
	h.writeJSON(w, http.StatusOK, h.toWPUserEnvelope(u, q.Embed))
}

// toWPUserEnvelope translates a UserRow into the public WP user shape.
// withEmbed is currently a no-op for users (there's nothing embeddable
// at this scope) but the parameter is plumbed so a future PR can add
// e.g. an embedded author-of-posts list without changing the signature.
func (h *handlers) toWPUserEnvelope(u UserRow, _ bool) wpUserEnvelope {
	link := fmt.Sprintf("%s/author/%s/", h.deps.SiteURL, u.Slug)
	avatar := u.AvatarURL
	avatars := map[string]string{}
	// Live WP returns avatar URLs at 24/48/96 — same URL with a
	// size query param. We mirror the keys even when we only have a
	// single source URL.
	if avatar != "" {
		avatars["24"] = avatar
		avatars["48"] = avatar
		avatars["96"] = avatar
	}
	return wpUserEnvelope{
		ID:          u.LegacyID,
		Name:        u.Name,
		URL:         u.URL,
		Description: u.Description,
		Link:        link,
		Slug:        u.Slug,
		AvatarURLs:  avatars,
		Links:       h.linksForUser(u),
	}
}
