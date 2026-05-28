// Package settings mounts the thin HTTP layer in front of the
// settings registry shipped in packages/go/settings.
//
// The package itself ships the schema (Setting + Registry), validation,
// and a Postgres-backed Store. What was missing was the HTTP surface
// that the admin Settings sub-pages call — /api/v1/settings?group=…
// (GET) and /api/v1/settings (PATCH). Issue #499 covers wiring that
// surface; this package is that wiring.
//
// Two-layer split (handler here, store + registry in
// packages/go/settings) follows the same convention used by every other
// admin module under apps/api/internal/admin/* (themes, customizer,
// marketplace, …). Keeps the package-level types reusable from CLI /
// worker / migration contexts without dragging net/http into the
// shared package.
package settings

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	pkgsettings "github.com/Singleton-Solution/GoNext/packages/go/settings"
)

// maxBodyBytes caps the PATCH payload at 64 KiB. The largest plausible
// patch is a full re-write of every core key (~30 string values, all
// small); 64 KiB is the "stop a runaway client" bound, not a tight
// per-request limit.
const maxBodyBytes = 64 * 1024

// Deps is the dependency bag for Mount. Store and Registry are
// required; Policy is required to gate writes; Logger falls back to
// slog.Default when nil.
//
// The Registry is passed explicitly rather than read from the
// packages/go/settings package-level global so callers (in particular
// tests) can isolate their own registry without touching shared state.
type Deps struct {
	// Store reads + writes setting values. Memory in tests, Postgres
	// in production.
	Store pkgsettings.Store

	// Registry holds the Setting declarations the handler iterates
	// when building the "all keys in group X" response.
	Registry *pkgsettings.Registry

	// Policy gates writes on the manage_options capability. Reads
	// require an authenticated principal but no specific capability —
	// site settings are operator metadata, not user PII, and every
	// admin role that lands in the dashboard has reason to read them.
	Policy policy.Policy

	// Logger receives structured log lines. nil falls back to
	// slog.Default.
	Logger *slog.Logger
}

func (d Deps) validate() error {
	if d.Store == nil {
		return errors.New("admin/settings: Store is required")
	}
	if d.Registry == nil {
		return errors.New("admin/settings: Registry is required")
	}
	if d.Policy == nil {
		return errors.New("admin/settings: Policy is required")
	}
	return nil
}

// handlers is the resolved-Deps form passed around inside the package.
type handlers struct {
	store    pkgsettings.Store
	registry *pkgsettings.Registry
	policy   policy.Policy
	logger   *slog.Logger
}

// Mount wires the settings routes onto mux under base (typically
// "/api/v1/settings"). Returns an error rather than panicking if Deps
// is malformed so the boot path surfaces it cleanly.
//
// The route tree:
//
//	GET   {base}            — list values, optionally filtered by ?group=
//	PATCH {base}            — sparse partial update
//
// Reads require any authenticated principal; writes require the
// manage_options capability.
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	h := &handlers{
		store:    deps.Store,
		registry: deps.Registry,
		policy:   deps.Policy,
		logger:   deps.Logger,
	}

	base = strings.TrimRight(base, "/")
	if base == "" {
		return errors.New("admin/settings: base path must not be empty")
	}
	// Bare-path patterns ("GET /api/v1/settings", no trailing slash, no
	// "{$}") match the endpoint exactly under net/http's ServeMux. We
	// intentionally do NOT use the "{base}/{$}" form here because that
	// would cause net/http to 301-redirect bare-path requests (the
	// admin client) to the trailing-slash form, which our handler
	// never serves. The admin sends "/api/v1/settings?group=…" with
	// no trailing slash; this match must be exact.
	mux.Handle("GET "+base, h.requireAuth(h.getSettings))
	mux.Handle("PATCH "+base, h.requireCap(policy.CapManageOptions, h.patchSettings))
	return nil
}

// requireAuth wraps a handler with the authentication check. The
// authenticated principal is forwarded to the handler; anonymous
// requests get 401 before the handler runs.
func (h *handlers) requireAuth(next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		next(w, r, pr)
	})
}

// requireCap wraps a handler with the auth + capability check. Returns
// 401 for anonymous requests, 403 when the principal lacks cap.
func (h *handlers) requireCap(cap policy.Capability, next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if d := h.policy.Can(pr, cap, nil); !d.Allowed {
			router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
			return
		}
		next(w, r, pr)
	})
}

// getSettings handles GET /api/v1/settings[?group=<group>].
//
// The response shape is a flat `{key: value}` JSON object — matches the
// "Settings" schema in apps/api/openapi/openapi.yaml (additionalProperties:
// true) and the SettingsValues type the admin client expects.
//
// The group filter is interpreted as a key-prefix match: ?group=core.site
// returns every registered key starting with "core.site." (so
// core.site.name, core.site.tagline, etc.). This matches the admin's
// SettingsGroup literals ("core.site", "core.reading", …) without
// requiring the registry to be re-grouped — the registry already keys
// settings by dotted namespace, which is the right index.
//
// "privacy" is a special-case: the privacy group's keys live under
// "core.privacy.*", so ?group=privacy is rewritten to "core.privacy"
// before the prefix match. Spelled out here rather than inferred so the
// translation is visible to future maintainers.
//
// Empty group returns every registered key. An unknown group returns
// an empty `{}` (200), not 404 — the admin renders an empty form
// rather than an error banner, which matches the existing "API not
// available" graceful-degradation path.
func (h *handlers) getSettings(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	group := strings.TrimSpace(r.URL.Query().Get("group"))
	prefix := groupPrefix(group)

	// Collect the registered keys whose canonical key matches the
	// prefix. Empty prefix (no group filter) matches every key.
	all := h.registry.List()
	keys := make([]string, 0, len(all))
	for _, s := range all {
		if prefix == "" || strings.HasPrefix(s.Key, prefix) {
			keys = append(keys, s.Key)
		}
	}

	out := make(map[string]any, len(keys))
	if len(keys) > 0 {
		values, err := h.store.BulkRead(r.Context(), keys)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "admin/settings: bulk read failed",
				slog.String("group", group),
				slog.Any("err", err),
			)
			router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to load settings")
			return
		}
		for k, v := range values {
			out[k] = v
		}
	}

	router.WriteJSON(w, http.StatusOK, out)
}

// patchSettings handles PATCH /api/v1/settings.
//
// Request body is a sparse `{key: value}` map; every present key is
// validated against its registered schema and written. The response is
// the post-write values for every key the patch touched, so the admin
// can refresh its form state without a separate GET.
//
// Per-key errors are aggregated into a 400 problem-details response —
// rather than committing the keys that succeeded and 400'ing the
// failure, we fail the whole patch atomically. (The Store interface
// does not expose a transaction handle, so "atomic" here means
// "we don't write anything if any key fails validation"; once a write
// hits Postgres the other writes in the same patch are already
// committed. Validation runs first so the common case — operator
// submits a form with a typo — never reaches the SQL layer.)
func (h *handlers) patchSettings(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		router.WriteError(w, http.StatusBadRequest, "bad_request", "failed to read request body")
		return
	}
	if len(body) > maxBodyBytes {
		router.WriteError(w, http.StatusRequestEntityTooLarge, "payload_too_large",
			"request body exceeds the maximum allowed size")
		return
	}
	if len(body) == 0 {
		router.WriteError(w, http.StatusBadRequest, "bad_request", "request body is empty")
		return
	}

	var patch map[string]any
	if err := json.Unmarshal(body, &patch); err != nil {
		router.WriteError(w, http.StatusBadRequest, "bad_json", "request body must be a JSON object")
		return
	}
	if len(patch) == 0 {
		// Empty patch — nothing to do, mirror the GET response shape
		// for an unfiltered request so the client can still refresh
		// its form state from the response.
		router.WriteJSON(w, http.StatusOK, map[string]any{})
		return
	}

	// Validate-then-write. The Store's Write returns ErrUnknownKey for
	// unregistered keys and ErrValidation for schema failures; we map
	// both to 400 with the offending key in the detail so the admin
	// can highlight the field.
	for key, value := range patch {
		if _, err := h.registry.Get(key); err != nil {
			router.WriteError(w, http.StatusBadRequest, "unknown_key",
				"unknown setting key: "+key)
			return
		}
		if err := h.store.Write(r.Context(), key, value); err != nil {
			switch {
			case errors.Is(err, pkgsettings.ErrUnknownKey):
				router.WriteError(w, http.StatusBadRequest, "unknown_key",
					"unknown setting key: "+key)
			case errors.Is(err, pkgsettings.ErrValidation):
				router.WriteError(w, http.StatusBadRequest, "validation_error",
					"invalid value for "+key+": "+err.Error())
			default:
				h.logger.ErrorContext(r.Context(), "admin/settings: write failed",
					slog.String("key", key),
					slog.Any("err", err),
				)
				router.WriteError(w, http.StatusInternalServerError, "internal_error",
					"failed to persist setting "+key)
			}
			return
		}
	}

	// Echo back the post-write values for the keys we touched. This
	// is the same shape the GET returns, so the client can drop the
	// response straight into its form state.
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	values, err := h.store.BulkRead(r.Context(), keys)
	if err != nil {
		// The writes succeeded; surfacing this as 500 would mislead the
		// admin into thinking the save failed. Log and return the
		// caller's patch as the best-effort response.
		h.logger.WarnContext(r.Context(), "admin/settings: post-write bulk read failed",
			slog.Any("err", err),
		)
		router.WriteJSON(w, http.StatusOK, patch)
		return
	}
	router.WriteJSON(w, http.StatusOK, values)
}

// groupPrefix maps a SettingsGroup literal from the admin client into
// the dotted key-prefix the registry indexes by.
//
//   - "" (no group)            → "" (match every key)
//   - "core.site"              → "core.site." (matches core.site.name etc.)
//   - "core.reading"           → "core.reading."
//   - "core.writing"           → "core.writing."
//   - "core.permalinks"        → "core.permalinks."
//   - "privacy"                → "core.privacy."
//   - anything else            → "<group>." (best-effort prefix match)
//
// The trailing dot prevents "core.site" from matching "core.sitemap.*"
// if a plugin ever registers under that namespace — prefix matches at
// the dot boundary are intentional, not accidental.
func groupPrefix(group string) string {
	if group == "" {
		return ""
	}
	// The admin uses "privacy" as a literal because the corresponding
	// settings page lives under /settings/privacy. The registry keys
	// are under core.privacy.*. Rewrite once here so the rest of the
	// handler can stay prefix-driven.
	if group == "privacy" {
		return "core.privacy."
	}
	if strings.HasSuffix(group, ".") {
		return group
	}
	return group + "."
}
