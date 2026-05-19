package tokens

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/auth/pat"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// Deps is the dependency bag for Mount. Every field is required;
// validate() catches missing fields at boot rather than NPE'ing on the
// first request.
type Deps struct {
	// Store is the PAT persistence layer. Required.
	Store pat.Store

	// UserCaps resolves the user's effective capability set. Required
	// — the issue handler narrows the requested scopes against this
	// to surface an "effective" set in the response, and the middleware
	// uses the same resolver for the auth path.
	UserCaps pat.UserCapsFunc

	// Logger receives structured log lines. nil falls back to
	// slog.Default — fine for tests, but production wiring should
	// always pass a service logger.
	Logger *slog.Logger

	// Now, if set, replaces time.Now. Used by tests to pin expiry.
	Now func() time.Time
}

func (d Deps) validate() error {
	if d.Store == nil {
		return errors.New("admin/tokens: Store is required")
	}
	if d.UserCaps == nil {
		return errors.New("admin/tokens: UserCaps resolver is required")
	}
	return nil
}

// handlers is the resolved-Deps form passed around inside the package.
type handlers struct {
	store    pat.Store
	userCaps pat.UserCapsFunc
	logger   *slog.Logger
	now      func() time.Time
}

// Mount wires the /me/tokens routes onto mux under base (typically
// "/api/v1/me/tokens"). Returns an error rather than panicking if
// Deps is malformed.
//
// The route tree:
//
//	GET    {base}        — list current user's active tokens
//	POST   {base}        — issue; response carries plaintext ONCE
//	DELETE {base}/{id}   — revoke (idempotent; second call is 204)
//
// Every route requires the user to be authenticated. The caller is
// responsible for mounting the session and/or PAT auth middleware
// upstream so the Principal is on the request context.
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	h := &handlers{
		store:    deps.Store,
		userCaps: deps.UserCaps,
		logger:   deps.Logger,
		now:      deps.Now,
	}
	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base, h.gate(h.list))
	mux.Handle("POST "+base, h.gate(h.issue))
	mux.Handle("DELETE "+base+"/{id}", h.gate(h.revoke))
	return nil
}

// gate ensures a Principal is on the context. It does NOT do a
// capability check — every authenticated user may manage their own
// tokens. Cross-user surfaces use policy.Require with an explicit cap.
func (h *handlers) gate(next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok || pr.UserID == "" {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		next(w, r, pr)
	})
}

// TokenView is the on-wire shape returned by list and revoke. It is
// deliberately separate from pat.PAT so the hash never appears in JSON
// even if a future refactor changes the in-memory layout.
type TokenView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// IssuedTokenView is the on-wire shape returned by the issue handler.
// The plaintext field is the ONLY place the operator will ever see
// the full token. The UI's TokenReveal component is responsible for
// gating dismissal behind a "save it" confirmation.
type IssuedTokenView struct {
	TokenView
	Plaintext string `json:"plaintext"`
	// EffectiveScopes is the intersection of the requested scopes with
	// the user's effective capability set. Surfaced so the UI can warn
	// "you asked for posts.write, but your role doesn't grant that;
	// the token can only do posts.read".
	EffectiveScopes []string `json:"effective_scopes"`
	// SaveNow is a UI hint that the plaintext is non-recoverable.
	// Clients SHOULD render this prominently; we surface it as a flag
	// rather than relying on the UI to know.
	SaveNow bool `json:"save_now"`
}

func toView(p pat.PAT) TokenView {
	return TokenView{
		ID:         p.ID,
		Name:       p.Name,
		Prefix:     p.Prefix,
		Scopes:     append([]string{}, p.Scopes...),
		CreatedAt:  p.CreatedAt,
		LastUsedAt: p.LastUsedAt,
		ExpiresAt:  p.ExpiresAt,
	}
}

// list handles GET /me/tokens.
func (h *handlers) list(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	rows, err := h.store.List(r.Context(), pr.UserID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/tokens: list failed",
			slog.String("user_id", pr.UserID),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list tokens")
		return
	}
	out := make([]TokenView, 0, len(rows))
	for _, p := range rows {
		out = append(out, toView(p))
	}
	router.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}

// IssueRequest is the JSON body for POST /me/tokens.
type IssueRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	// ExpiresIn is the duration before the token expires, expressed
	// as one of: "30d", "90d", "1y", "never". The presets match the
	// UI radio group. Empty string means "never".
	ExpiresIn string `json:"expires_in"`
}

// presetExpiry maps the preset string to a duration. Unknown presets
// return ok=false; the handler maps that to a 400.
func presetExpiry(s string, now time.Time) (*time.Time, bool) {
	switch strings.TrimSpace(s) {
	case "", "never":
		return nil, true
	case "30d":
		t := now.Add(30 * 24 * time.Hour)
		return &t, true
	case "90d":
		t := now.Add(90 * 24 * time.Hour)
		return &t, true
	case "1y":
		t := now.Add(365 * 24 * time.Hour)
		return &t, true
	default:
		return nil, false
	}
}

// issue handles POST /me/tokens.
func (h *handlers) issue(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	var req IssueRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_body", "request body is not valid JSON")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		router.WriteError(w, http.StatusBadRequest, "invalid_name", "name is required")
		return
	}
	if len(req.Scopes) == 0 {
		router.WriteError(w, http.StatusBadRequest, "invalid_scopes", "at least one scope is required")
		return
	}
	// Dedup scopes — a multi-select UI can submit duplicates after
	// a quick double-click; we store the canonical set.
	req.Scopes = dedupScopes(req.Scopes)
	expiresAt, ok := presetExpiry(req.ExpiresIn, h.now())
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_expires_in", "expires_in must be one of: never, 30d, 90d, 1y")
		return
	}

	plaintext, row, hash, err := pat.New(pr.UserID, req.Name, req.Scopes, expiresAt)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/tokens: mint failed",
			slog.String("user_id", pr.UserID),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to mint token")
		return
	}
	stored, err := h.store.Issue(r.Context(), row, hash)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/tokens: insert failed",
			slog.String("user_id", pr.UserID),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to persist token")
		return
	}

	caps, err := h.userCaps(r.Context(), pr.UserID)
	if err != nil {
		// We've already inserted the row; logging the cap-resolution
		// failure but returning the token is the right call — the
		// operator needs the plaintext NOW and the UI can re-fetch
		// effective_scopes later.
		h.logger.WarnContext(r.Context(), "admin/tokens: caps lookup failed",
			slog.String("user_id", pr.UserID),
			slog.Any("err", err),
		)
		caps = policy.CapabilitySet{}
	}
	effective := pat.Intersect(req.Scopes, caps)
	effectiveSlugs := make([]string, 0, len(effective))
	for c := range effective {
		effectiveSlugs = append(effectiveSlugs, string(c))
	}

	view := IssuedTokenView{
		TokenView:       toView(stored),
		Plaintext:       plaintext,
		EffectiveScopes: effectiveSlugs,
		SaveNow:         true,
	}
	router.WriteJSON(w, http.StatusCreated, view)
}

// revoke handles DELETE /me/tokens/{id}.
func (h *handlers) revoke(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "token id is required")
		return
	}
	err := h.store.Revoke(r.Context(), pr.UserID, id)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, pat.ErrNotFound):
		router.WriteError(w, http.StatusNotFound, "not_found", "token not found")
	default:
		h.logger.ErrorContext(r.Context(), "admin/tokens: revoke failed",
			slog.String("user_id", pr.UserID),
			slog.String("token_id", id),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to revoke token")
	}
}

func dedupScopes(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

