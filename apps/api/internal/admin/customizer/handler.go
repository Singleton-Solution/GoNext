package customizer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/theme"
)

// maxBodyBytes caps the override payload at 64 KiB. The largest
// realistic override (a full palette + typography + spacing scale)
// fits in well under 4 KiB; the cap is "stop a runaway client" rather
// than a tight bound.
const maxBodyBytes = 64 * 1024

// ThemeLoader returns the parsed manifest for the active theme slug.
// Production wires this to a function that reads
// "<ThemeDir>/<slug>/theme.json" and runs theme.Parse; tests inject a
// closure that returns a hand-built ThemeJSON value.
//
// Returning an error from the loader propagates to the GET handler as
// 500 — the operator who landed on the customizer page deserves a hint
// that something is wrong with the on-disk theme, not a blank page.
type ThemeLoader func(ctx context.Context, slug string) (*theme.ThemeJSON, error)

// Deps is the dependency bag for Mount. All fields are required (Logger
// excepted); validate() catches missing fields at boot rather than
// NPE'ing on the first request.
type Deps struct {
	// Store handles the per-slug active-theme + overrides reads/writes.
	Store Store

	// Loader returns the parsed manifest for an active theme slug.
	Loader ThemeLoader

	// Policy resolves the theme.customize capability check.
	Policy policy.Policy

	// Logger receives structured log lines. nil falls back to
	// slog.Default — useful for tests but production wiring should
	// always pass a service logger.
	Logger *slog.Logger
}

func (d Deps) validate() error {
	if d.Store == nil {
		return errors.New("admin/customizer: Store is required")
	}
	if d.Loader == nil {
		return errors.New("admin/customizer: Loader is required")
	}
	if d.Policy == nil {
		return errors.New("admin/customizer: Policy is required")
	}
	return nil
}

// handlers is the resolved-Deps form passed around inside the package.
type handlers struct {
	store  Store
	loader ThemeLoader
	policy policy.Policy
	logger *slog.Logger
}

// Mount wires the customizer routes onto mux under base (typically
// "/api/v1/admin/customizer"). Returns an error rather than panicking
// if Deps is malformed so the boot path surfaces it cleanly.
//
// The route tree:
//
//	GET    {base}/active    — theme + current overrides
//	PUT    {base}/active    — validate + save overrides
//	DELETE {base}/active    — clear overrides (Reset)
//	POST   {base}/preview   — preview merged CSS without persisting
//
// All routes are gated by the theme.customize capability.
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	h := &handlers{
		store:  deps.Store,
		loader: deps.Loader,
		policy: deps.Policy,
		logger: deps.Logger,
	}

	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base+"/active", h.gate(h.getActive))
	mux.Handle("PUT "+base+"/active", h.gate(h.putActive))
	mux.Handle("DELETE "+base+"/active", h.gate(h.deleteActive))
	mux.Handle("POST "+base+"/preview", h.gate(h.postPreview))
	return nil
}

// gate wraps a handler with the auth + theme.customize capability
// check. Returns 401 if no principal is on the context (the auth
// middleware hasn't run, or the request is anonymous); 403 if the
// principal lacks the capability.
func (h *handlers) gate(next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if d := h.policy.Can(pr, policy.CapThemeCustomize, nil); !d.Allowed {
			router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
			return
		}
		next(w, r, pr)
	})
}

// ActiveResponse is the GET response body. The shape is split into the
// theme manifest, its slug, and the current overrides so the admin UI
// can render the "default + override" diff without re-parsing the
// manifest. A nil Overrides field marshals as an empty object — the
// UI's reset state.
type ActiveResponse struct {
	ThemeSlug string           `json:"themeSlug"`
	Theme     *theme.ThemeJSON `json:"theme"`
	Overrides json.RawMessage  `json:"overrides"`
}

// getActive handles GET /active. Reads the active-theme slug, loads
// its manifest, and returns it alongside the persisted overrides (if
// any). On a fresh deploy with no overrides, Overrides is `{}`.
func (h *handlers) getActive(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	slug, err := h.store.ActiveThemeSlug(r.Context())
	if err != nil {
		if errors.Is(err, ErrNoActiveTheme) {
			router.WriteError(w, http.StatusNotFound, "no_active_theme", "no active theme is configured")
			return
		}
		h.logger.ErrorContext(r.Context(), "admin/customizer: active slug lookup failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to load active theme")
		return
	}

	manifest, err := h.loader(r.Context(), slug)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/customizer: theme load failed",
			slog.String("slug", slug),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "theme_load_failed",
			fmt.Sprintf("failed to load theme %q", slug))
		return
	}

	overrides, err := h.store.ReadOverrides(r.Context(), slug)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/customizer: overrides read failed",
			slog.String("slug", slug),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to load overrides")
		return
	}
	if len(overrides) == 0 {
		overrides = json.RawMessage("{}")
	}

	router.WriteJSON(w, http.StatusOK, ActiveResponse{
		ThemeSlug: slug,
		Theme:     manifest,
		Overrides: overrides,
	})
}

// putActive handles PUT /active. Validates the request body against
// the active theme's manifest and persists it under
// "theme_mods.<slug>". On success, returns the freshly-merged manifest
// so the admin UI can refresh its preview without an extra round trip.
func (h *handlers) putActive(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	slug, err := h.store.ActiveThemeSlug(r.Context())
	if err != nil {
		if errors.Is(err, ErrNoActiveTheme) {
			router.WriteError(w, http.StatusNotFound, "no_active_theme", "no active theme is configured")
			return
		}
		h.logger.ErrorContext(r.Context(), "admin/customizer: active slug lookup failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to load active theme")
		return
	}

	manifest, err := h.loader(r.Context(), slug)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/customizer: theme load failed",
			slog.String("slug", slug),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "theme_load_failed",
			fmt.Sprintf("failed to load theme %q", slug))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		router.WriteError(w, http.StatusRequestEntityTooLarge, "body_too_large",
			fmt.Sprintf("override payload must not exceed %d bytes", maxBodyBytes))
		return
	}

	_, _, validationErrs, parseErr := ValidateOverride(manifest, raw)
	if parseErr != nil {
		if errors.Is(parseErr, ErrEmptyOverride) {
			router.WriteError(w, http.StatusBadRequest, "empty_override",
				"override payload was empty; use DELETE to clear overrides")
			return
		}
		router.WriteError(w, http.StatusBadRequest, "invalid_path", parseErr.Error())
		return
	}
	if len(validationErrs) > 0 {
		writeValidationProblem(w, slug, validationErrs)
		return
	}

	if err := h.store.WriteOverrides(r.Context(), slug, json.RawMessage(raw)); err != nil {
		h.logger.ErrorContext(r.Context(), "admin/customizer: write failed",
			slog.String("slug", slug),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to save overrides")
		return
	}
	h.logger.InfoContext(r.Context(), "admin/customizer: overrides saved",
		slog.String("slug", slug),
		slog.String("by", pr.UserID),
	)

	router.WriteJSON(w, http.StatusOK, ActiveResponse{
		ThemeSlug: slug,
		Theme:     manifest,
		Overrides: json.RawMessage(raw),
	})
}

// deleteActive handles DELETE /active. Clears any persisted overrides
// for the active theme; idempotent — calling on a clean install is a
// 204, not an error.
func (h *handlers) deleteActive(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	slug, err := h.store.ActiveThemeSlug(r.Context())
	if err != nil {
		if errors.Is(err, ErrNoActiveTheme) {
			router.WriteError(w, http.StatusNotFound, "no_active_theme", "no active theme is configured")
			return
		}
		h.logger.ErrorContext(r.Context(), "admin/customizer: active slug lookup failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to load active theme")
		return
	}
	if err := h.store.DeleteOverrides(r.Context(), slug); err != nil {
		h.logger.ErrorContext(r.Context(), "admin/customizer: delete failed",
			slog.String("slug", slug),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to clear overrides")
		return
	}
	h.logger.InfoContext(r.Context(), "admin/customizer: overrides cleared",
		slog.String("slug", slug),
		slog.String("by", pr.UserID),
	)
	w.WriteHeader(http.StatusNoContent)
}

// PreviewRequest is the POST /preview body. The admin React form posts
// the in-flight override state straight through; we accept either the
// override object directly OR a wrapper {"overrides": <ThemeOverrides>}
// because two generations of the admin form sit in production today and
// re-shipping the API while the UI still uses the old wrapper would
// strand operators on a 400.
type PreviewRequest struct {
	Overrides json.RawMessage `json:"overrides,omitempty"`
}

// PreviewResponse carries the rendered CSS the iframe applies. The
// shape stays narrow on purpose — every additional field is something
// the renderer would have to learn to read.
type PreviewResponse struct {
	// ThemeSlug echoes the slug the preview was computed for. The admin
	// surface uses it to verify the preview matches the theme the form
	// thinks it's editing (a theme switch mid-edit invalidates the
	// preview).
	ThemeSlug string `json:"themeSlug"`
	// CSSCustomProperties is the ":root { … }" block produced by
	// EmitCSSCustomProperties on the merged manifest. The empty string is
	// a legitimate result (e.g. a theme with no tokens at all) — the
	// renderer treats "" as "render with browser defaults".
	CSSCustomProperties string `json:"cssCustomProperties"`
}

// postPreview handles POST /preview. The flow mirrors putActive without
// the persistence: load the active theme, merge the overrides, run
// validation, and return the emitted CSS. An empty body is treated as
// "preview with no overrides" — the operator wants to see what the
// theme looks like without any of their pending edits, which the admin
// UI uses to drive the "Reset" preview state.
//
// We deliberately don't 400 on validation errors here: previews are
// keystroke-grained and the form often passes through "partially typed"
// values. A bad color becomes an empty entry in the merged manifest,
// not a 400 — the operator's next keystroke usually fixes it. Strict
// validation lives on PUT, which is the operator's explicit "commit"
// gesture.
func (h *handlers) postPreview(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	slug, err := h.store.ActiveThemeSlug(r.Context())
	if err != nil {
		if errors.Is(err, ErrNoActiveTheme) {
			router.WriteError(w, http.StatusNotFound, "no_active_theme", "no active theme is configured")
			return
		}
		h.logger.ErrorContext(r.Context(), "admin/customizer: active slug lookup failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to load active theme")
		return
	}

	manifest, err := h.loader(r.Context(), slug)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/customizer: theme load failed",
			slog.String("slug", slug),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "theme_load_failed",
			fmt.Sprintf("failed to load theme %q", slug))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		router.WriteError(w, http.StatusRequestEntityTooLarge, "body_too_large",
			fmt.Sprintf("override payload must not exceed %d bytes", maxBodyBytes))
		return
	}

	// extractOverrides unwraps an optional `{"overrides": {...}}` envelope
	// so the admin client can post either the override or the wrapper.
	overrideBytes := extractOverridesPayload(raw)

	merged := cloneTheme(manifest)
	// Empty body or empty overrides → preview the unmodified theme.
	if len(bytes.TrimSpace(overrideBytes)) > 0 && !bytes.Equal(bytes.TrimSpace(overrideBytes), []byte("{}")) && !bytes.Equal(bytes.TrimSpace(overrideBytes), []byte("null")) {
		// Decode loosely (without DisallowUnknownFields): preview is
		// best-effort. Unknown keys come from a draft schema in the UI;
		// dropping them quietly is friendlier than 400'ing on every
		// keystroke that touches a future field.
		override := &theme.ThemeJSON{Version: theme.CurrentVersion}
		if jerr := json.Unmarshal(overrideBytes, override); jerr != nil {
			router.WriteError(w, http.StatusBadRequest, "invalid_json",
				"preview overrides are not valid JSON")
			return
		}
		mergeTheme(merged, override)
	}

	css := merged.EmitCSSCustomProperties()
	router.WriteJSON(w, http.StatusOK, PreviewResponse{
		ThemeSlug:           slug,
		CSSCustomProperties: css,
	})
}

// extractOverridesPayload unwraps `{"overrides": <body>}` if present,
// otherwise treats the whole body as the override directly. Returns the
// raw body unchanged when it can't be parsed as JSON — the caller's
// strict decode will surface the syntax error with a useful message.
func extractOverridesPayload(raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return trimmed
	}
	var envelope PreviewRequest
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return trimmed
	}
	if len(envelope.Overrides) > 0 {
		return envelope.Overrides
	}
	return trimmed
}

// writeValidationProblem emits a 400 with the per-path validation
// errors in the body. The shape mirrors what other admin surfaces use
// for field-level errors: a single "code" identifier plus a flat list
// of {path, message} entries so the UI can highlight every offending
// field at once.
func writeValidationProblem(w http.ResponseWriter, slug string, errs []theme.ValidationError) {
	type fieldErr struct {
		Path    string `json:"path"`
		Message string `json:"message"`
	}
	type problem struct {
		Type   string     `json:"type"`
		Title  string     `json:"title"`
		Status int        `json:"status"`
		Code   string     `json:"code"`
		Detail string     `json:"detail"`
		Errors []fieldErr `json:"errors"`
	}

	fields := make([]fieldErr, 0, len(errs))
	for _, e := range errs {
		fields = append(fields, fieldErr{Path: e.Path, Message: e.Message})
	}
	body := problem{
		Type:   "about:blank",
		Title:  http.StatusText(http.StatusBadRequest),
		Status: http.StatusBadRequest,
		Code:   "invalid_override",
		Detail: fmt.Sprintf("override for theme %q has %d validation error(s)", slug, len(errs)),
		Errors: fields,
	}
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(body)
}
