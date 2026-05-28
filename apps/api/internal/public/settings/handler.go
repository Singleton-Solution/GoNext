// Package settings is the public, read-only REST surface for the site
// identity options — issue #508. It exposes the safe subset of the
// settings registry (core.site.name, core.site.tagline, core.site.url)
// without an authentication gate, so the apps/web container can paint
// <title>, og:site_name, and metadataBase without a session cookie.
//
// Routes (mounted under base, typically /api/v1/public/site):
//
//	GET {base}    — flat JSON: {"name", "tagline", "url"}.
//
// Why a separate package from internal/admin/settings: the admin surface
// is auth-gated, exposes the full registry, and persists changes. The
// public reader is the surface anonymous visitors hit, so it (1) must
// answer without a session and (2) must only surface fields that are
// publicly safe to disclose — name, tagline, url. The rest of the
// registry (default_role, timezone, post-by-email address, …) stays
// behind the admin gate.
//
// Failure mode is "return defaults, never 5xx". A store error or
// missing keys both return the documented defaults with a 200; the
// public site is not load-bearing on this endpoint and falling back to
// stock copy is safer than breaking every public page render.
package settings

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	pkgsettings "github.com/Singleton-Solution/GoNext/packages/go/settings"
)

// Public-default values returned when the registry has no stored value
// for a key, when the key isn't registered, or when the store returns
// an error. These mirror the strings apps/web hardcodes in
// DEFAULT_SITE_OPTIONS so a renderer that fails closed paints the same
// "GoNext" envelope the server-side defaults would have produced.
//
// Note that these intentionally differ from the registry defaults
// declared in packages/go/settings/core.go ("My GoNext Site",
// "Just another GoNext site", "http://localhost:8080") — those defaults
// target a freshly-installed admin form, where the operator hasn't
// chosen a name yet. The public surface targets a freshly-installed
// public site, where the right "didn't pick anything" answer is the
// generic product name with no URL so the renderer skips metadataBase.
const (
	defaultName    = "GoNext"
	defaultTagline = "A site powered by GoNext."
	defaultURL     = ""
)

// The three registry keys that make up the publicly safe projection of
// the core.site group. Kept in a slice so the BulkRead call site stays
// driven by the single source of truth at the top of the file.
const (
	keySiteName    = "core.site.name"
	keySiteTagline = "core.site.tagline"
	keySiteURL     = "core.site.url"
)

// Deps is the dependency bag for [Mount].
type Deps struct {
	// Store is the read side of the settings persistence layer. The
	// public reader only calls BulkRead; any implementation satisfying
	// [pkgsettings.Store] works (MemoryStore in tests, PostgresStore in
	// production).
	Store pkgsettings.Store
	// Logger is optional; defaults to slog.Default().
	Logger *slog.Logger
}

func (d Deps) validate() error {
	if d.Store == nil {
		return errors.New("public/settings: Deps.Store is required")
	}
	return nil
}

type handlers struct {
	store  pkgsettings.Store
	logger *slog.Logger
}

// Mount wires the public site identity routes onto mux under base
// (typically "/api/v1/public/site"). No policy gate: this surface
// serves unauthenticated visitors hitting the marketing landing.
//
// The route tree is intentionally flat — a single GET returning the
// projected {name, tagline, url} object. Callers that need the full
// settings registry hit the admin /api/v1/settings endpoint.
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	h := &handlers{store: deps.Store, logger: deps.Logger}
	base = strings.TrimRight(base, "/")
	if base == "" {
		return errors.New("public/settings: base path must not be empty")
	}
	// Bare-path pattern (no trailing slash, no "{$}") so net/http's
	// ServeMux matches "/api/v1/public/site" exactly without redirecting
	// the trailing-slash form. Matches the admin/settings convention.
	mux.Handle("GET "+base, http.HandlerFunc(h.getSite))
	return nil
}

// siteIdentity is the wire shape returned by getSite. Keeping the
// public surface area small — three string fields — means we can
// add registry keys to core.site.* without worrying about leaking them
// to anonymous visitors. New publicly-visible fields require an
// explicit addition here.
type siteIdentity struct {
	Name    string `json:"name"`
	Tagline string `json:"tagline"`
	URL     string `json:"url"`
}

// defaultSiteIdentity is the canonical "we couldn't read anything"
// response. Spelled out as a value rather than a builder so the
// defaults are visible side-by-side with their per-key constants.
func defaultSiteIdentity() siteIdentity {
	return siteIdentity{
		Name:    defaultName,
		Tagline: defaultTagline,
		URL:     defaultURL,
	}
}

// getSite answers GET /api/v1/public/site with the projected site
// identity. The response always serialises as
// {"name": "...", "tagline": "...", "url": "..."} — three strings, in
// that order on the type, never null, never an envelope.
//
// Failure paths:
//
//   - Store returns an error → log + return the documented defaults
//     with status 200. The public surface is forgiving; a store hiccup
//     should not break the landing page.
//   - Key not in store → BulkRead applies the registry default. We
//     project that through, then overlay the documented public defaults
//     for empty / non-string values so a renderer never receives a
//     blank Name.
func (h *handlers) getSite(w http.ResponseWriter, r *http.Request) {
	out := defaultSiteIdentity()

	keys := []string{keySiteName, keySiteTagline, keySiteURL}
	values, err := h.store.BulkRead(r.Context(), keys)
	if err != nil {
		// A store error is a server problem, not a client one, but the
		// public surface stays forgiving — a default-identity 200 keeps
		// the landing page rendering while a 500 would crash every
		// Server Component that calls this endpoint at render time.
		h.logger.ErrorContext(r.Context(), "public/settings: bulk read failed",
			slog.Any("err", err),
		)
		router.WriteJSON(w, http.StatusOK, out)
		return
	}

	if s, ok := stringValue(values[keySiteName]); ok && s != "" {
		out.Name = s
	}
	if s, ok := stringValue(values[keySiteTagline]); ok && s != "" {
		out.Tagline = s
	}
	if s, ok := stringValue(values[keySiteURL]); ok {
		// URL falls through empty intentionally — an empty URL is a
		// valid "no canonical origin configured" signal. Only the type
		// guard runs here, not the empty-string fallback.
		out.URL = s
	}

	router.WriteJSON(w, http.StatusOK, out)
}

// stringValue narrows an `any` from the registry store down to a
// concrete string. Returns (value, true) on a string, ("", false) on
// any other type — typed defaults in the registry are always strings
// for the three keys we read, so a non-string is a contract violation
// we translate into the package default rather than surfacing as a
// type assertion panic.
func stringValue(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}
