package wprest

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
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

// -----------------------------------------------------------------------------
// Write path — users
// -----------------------------------------------------------------------------

// wpUserWriteBody is the JSON body for POST/PUT/PATCH on /wp/v2/users.
// All fields are pointer-typed so a sparse PATCH leaves untouched
// columns alone. The shim does not interpret password (the sink hashes
// it); we transport it as-is.
type wpUserWriteBody struct {
	Username    *string   `json:"username"`
	Email       *string   `json:"email"`
	Password    *string   `json:"password"`
	Name        *string   `json:"name"`
	Slug        *string   `json:"slug"`
	URL         *string   `json:"url"`
	Description *string   `json:"description"`
	// Roles is the WP-side role slug list. Sinks map these through
	// policy.Role values and apply CapPromoteUsers as appropriate —
	// the shim only forwards.
	Roles *[]string `json:"roles"`
}

func (b *wpUserWriteBody) toUserWriteInput() UserWriteInput {
	out := UserWriteInput{}
	if b.Username != nil {
		out.Username = b.Username
	}
	if b.Email != nil {
		out.Email = b.Email
	}
	if b.Password != nil {
		out.Password = b.Password
	}
	if b.Name != nil {
		out.Name = b.Name
	}
	if b.Slug != nil {
		out.Slug = b.Slug
	}
	if b.URL != nil {
		out.URL = b.URL
	}
	if b.Description != nil {
		out.Description = b.Description
	}
	if b.Roles != nil {
		out.Roles = b.Roles
	}
	return out
}

// createUser implements POST /wp-json/wp/v2/users. Requires the
// CapCreateUsers capability. Promoting via roles[] is a separate
// CapPromoteUsers check the sink applies.
func (h *handlers) createUser(w http.ResponseWriter, r *http.Request) {
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
	if !h.requireCapability(w, pr, policy.CapCreateUsers, nil) {
		return
	}

	var body wpUserWriteBody
	if !decodeWriteBody(w, r, &body) {
		return
	}
	in := body.toUserWriteInput()

	row, err := h.deps.UsersSink.Create(r.Context(), pr.UserID, in)
	if err != nil {
		h.writeUserSinkError(w, r, err, errCodeCannotCreate)
		return
	}

	h.emitAudit(r.Context(), pr, EventUserCreated, "user", strconv.Itoa(row.LegacyID), map[string]any{
		"slug": row.Slug,
	})

	env := h.toWPUserEnvelope(row, false)
	h.writeJSON(w, http.StatusCreated, env)
}

// updateUser implements PUT/PATCH /wp-json/wp/v2/users/{id}.
func (h *handlers) updateUser(w http.ResponseWriter, r *http.Request) {
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
	if !h.requireCapability(w, pr, policy.CapEditUsers, nil) {
		return
	}

	id, ok := parseIDFromPath(w, r, errCodeInvalidUserID, "Invalid user ID.")
	if !ok {
		return
	}

	var body wpUserWriteBody
	if !decodeWriteBody(w, r, &body) {
		return
	}
	in := body.toUserWriteInput()

	row, err := h.deps.UsersSink.Update(r.Context(), pr.UserID, id, in)
	if err != nil {
		h.writeUserSinkError(w, r, err, errCodeCannotEdit)
		return
	}

	h.emitAudit(r.Context(), pr, EventUserUpdated, "user", strconv.Itoa(row.LegacyID), map[string]any{
		"slug": row.Slug,
	})

	env := h.toWPUserEnvelope(row, false)
	h.writeJSON(w, http.StatusOK, env)
}

// deleteUser implements DELETE /wp-json/wp/v2/users/{id}. Per WP REST
// semantics this is a SOFT delete (status flip / deactivate), not a
// row drop — the body shape on success is `{deleted: true, previous}`.
// A future PR may add ?reassign=<id> support to migrate content
// authored by the deleted user.
func (h *handlers) deleteUser(w http.ResponseWriter, r *http.Request) {
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
	if !h.requireCapability(w, pr, policy.CapDeleteUsers, nil) {
		return
	}

	id, ok := parseIDFromPath(w, r, errCodeInvalidUserID, "Invalid user ID.")
	if !ok {
		return
	}

	var reassign *int
	if raw := r.URL.Query().Get("reassign"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			reassign = &n
		}
	}

	row, err := h.deps.UsersSink.Delete(r.Context(), pr.UserID, id, reassign)
	if err != nil {
		h.writeUserSinkError(w, r, err, errCodeCannotDelete)
		return
	}

	h.emitAudit(r.Context(), pr, EventUserDeleted, "user", strconv.Itoa(row.LegacyID), map[string]any{
		"slug": row.Slug,
	})

	h.writeJSON(w, http.StatusOK, map[string]any{
		"deleted":  true,
		"previous": h.toWPUserEnvelope(row, false),
	})
}

// writeUserSinkError translates user-sink errors to WP-shaped responses.
// Per-resource id codes are wired in — ErrDuplicate maps to a user-
// specific `rest_user_exists` code on the way out.
func (h *handlers) writeUserSinkError(w http.ResponseWriter, r *http.Request, err error, cannotXCode string) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, errCodeInvalidUserID, "Invalid user ID.")
	case errors.Is(err, ErrInvalidInput):
		writeError(w, http.StatusBadRequest, errCodeInvalidParam, "Invalid parameter(s).")
	case errors.Is(err, ErrDuplicate):
		writeError(w, http.StatusConflict, errCodeUserExists, "A user with that identifier already exists.")
	default:
		h.deps.Logger.ErrorContext(r.Context(), "wprest: user sink error",
			slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, cannotXCode, "The user could not be saved.")
	}
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
