package themes

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// maxUploadBytes caps the multipart body. The actual ZIP bytes are
// further limited by MaxThemeZipSize inside Install; this is the
// outer guard so an attacker can't burn server memory uploading 1 GiB
// of multipart noise before we ever look at the inner archive.
const maxUploadBytes = MaxThemeZipSize + 64*1024 // +64KiB for multipart framing

// Deps is the dependency bag for Mount.
type Deps struct {
	// ThemeDir is the absolute directory where installed themes
	// live. Required. The installer extracts into a subdirectory
	// under this path; the listing handler reads from it.
	ThemeDir string

	// Active is the active-theme option store. Required for the
	// activate handler; the list endpoint also reads through it to
	// stamp the response with the current slug.
	Active ActiveStore

	// Policy resolves the install / switch capability checks.
	// Required.
	Policy policy.Policy

	// Logger receives structured log lines. nil falls back to
	// slog.Default — useful for tests but production wiring should
	// always pass a service logger.
	Logger *slog.Logger
}

func (d Deps) validate() error {
	if d.ThemeDir == "" {
		return errors.New("admin/themes: ThemeDir is required")
	}
	if d.Active == nil {
		return errors.New("admin/themes: Active is required")
	}
	if d.Policy == nil {
		return errors.New("admin/themes: Policy is required")
	}
	return nil
}

type handlers struct {
	themeDir string
	active   ActiveStore
	policy   policy.Policy
	logger   *slog.Logger
}

// Mount wires the themes admin routes onto mux under base (typically
// "/api/v1/admin/themes"). Returns an error rather than panicking if
// Deps is malformed so the boot path surfaces it cleanly.
//
// Route tree:
//
//	GET    {base}            — list installed themes + active slug
//	POST   {base}/install    — accept .gntheme ZIP upload
//	POST   {base}/activate   — switch active theme
//
// Capabilities (per packages/go/policy/capabilities.go):
//
//	GET  {base}            → manage_themes
//	POST {base}/install    → install_themes
//	POST {base}/activate   → switch_themes
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	h := &handlers{
		themeDir: deps.ThemeDir,
		active:   deps.Active,
		policy:   deps.Policy,
		logger:   deps.Logger,
	}

	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base, h.gate(policy.CapManageThemes, h.list))
	mux.Handle("POST "+base+"/install", h.gate(policy.CapInstallThemes, h.install))
	mux.Handle("POST "+base+"/activate", h.gate(policy.CapSwitchThemes, h.activate))
	return nil
}

// gate wraps a handler with the auth + capability check. Returns 401
// when no principal is on the context, 403 when the principal lacks
// the capability.
func (h *handlers) gate(cap policy.Capability, next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
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

// listResponse is the GET /themes response. Themes carries every
// directory whose theme.json parsed cleanly; ActiveSlug is the slug
// the switcher should render with the "Active" badge.
type listResponse struct {
	Themes     []ThemeInfo `json:"themes"`
	ActiveSlug string      `json:"active_slug"`
}

func (h *handlers) list(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	themes, err := ListInstalled(r.Context(), h.themeDir)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/themes: list failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list themes")
		return
	}
	active, err := h.active.Get(r.Context())
	if err != nil && !errors.Is(err, ErrNoActiveTheme) {
		h.logger.ErrorContext(r.Context(), "admin/themes: active read failed", slog.Any("err", err))
		// Soft-fail: surface the list even when the active read errored.
		// The switcher will render without a badge rather than 500ing.
		active = ""
	}
	router.WriteJSON(w, http.StatusOK, listResponse{Themes: themes, ActiveSlug: active})
}

// installResponse is the POST /install response on success. Slug is
// the directory the theme landed under; Title is the manifest title
// so the UI can echo it back in a confirmation toast.
type installResponse struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Version int    `json:"version"`
}

func (h *handlers) install(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	// Limit the request body before we touch it. http.MaxBytesReader
	// wraps the body so any subsequent ReadAll bails with a 413-style
	// error rather than letting the multipart parser eat the
	// over-large body.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)

	// The installer accepts both raw application/zip POST bodies and
	// multipart/form-data with a single "file" field. The former is
	// nicer for curl + scripts; the latter is what the browser sends
	// by default.
	contentType := r.Header.Get("Content-Type")
	var data []byte
	var readErr error
	if strings.HasPrefix(contentType, "multipart/form-data") {
		data, readErr = readMultipart(r)
	} else {
		data, readErr = io.ReadAll(r.Body)
	}
	if readErr != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_upload", readErr.Error())
		return
	}
	if len(data) == 0 {
		router.WriteError(w, http.StatusBadRequest, "empty_upload", "request body is empty")
		return
	}

	result, err := Install(h.themeDir, data)
	if err != nil {
		switch {
		case errors.Is(err, ErrZipMissingManifest),
			errors.Is(err, ErrInvalidManifest),
			errors.Is(err, ErrInvalidSlug),
			errors.Is(err, ErrUnsafePath),
			errors.Is(err, ErrEntryTooLarge),
			errors.Is(err, ErrTooManyFiles):
			router.WriteError(w, http.StatusBadRequest, "invalid_theme", err.Error())
		case errors.Is(err, ErrThemeExists):
			router.WriteError(w, http.StatusConflict, "theme_exists", err.Error())
		default:
			h.logger.ErrorContext(r.Context(), "admin/themes: install failed",
				slog.Any("err", err),
				slog.String("by", pr.UserID),
			)
			router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to install theme")
		}
		return
	}
	h.logger.InfoContext(r.Context(), "admin/themes: theme installed",
		slog.String("slug", result.Slug),
		slog.String("by", pr.UserID),
	)
	router.WriteJSON(w, http.StatusCreated, installResponse{
		Slug:    result.Slug,
		Title:   result.Manifest.Title,
		Version: result.Manifest.Version,
	})
}

// activateRequest is the POST /activate body.
type activateRequest struct {
	Slug string `json:"slug"`
}

func (h *handlers) activate(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	var req activateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024)).Decode(&req); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}
	req.Slug = strings.TrimSpace(req.Slug)
	if req.Slug == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_slug", "slug is required")
		return
	}
	if !ThemeInstalled(h.themeDir, req.Slug) {
		router.WriteError(w, http.StatusNotFound, "theme_not_installed",
			fmt.Sprintf("theme %q is not installed", req.Slug))
		return
	}
	if err := h.active.Set(r.Context(), req.Slug); err != nil {
		h.logger.ErrorContext(r.Context(), "admin/themes: activate write failed",
			slog.String("slug", req.Slug),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to switch theme")
		return
	}
	h.logger.InfoContext(r.Context(), "admin/themes: theme activated",
		slog.String("slug", req.Slug),
		slog.String("by", pr.UserID),
	)
	router.WriteJSON(w, http.StatusOK, map[string]string{"active_slug": req.Slug})
}

// readMultipart pulls the first non-empty file part out of a
// multipart/form-data request. We do not enforce a specific field
// name — operators sometimes name the field "theme", "file",
// "upload", or "archive" depending on the form library they reach
// for; first non-empty file wins.
func readMultipart(r *http.Request) ([]byte, error) {
	if err := r.ParseMultipartForm(MaxThemeZipSize); err != nil {
		return nil, fmt.Errorf("multipart parse: %w", err)
	}
	if r.MultipartForm == nil {
		return nil, errors.New("missing multipart form")
	}
	for _, files := range r.MultipartForm.File {
		for _, fh := range files {
			if fh.Size == 0 {
				continue
			}
			f, err := fh.Open()
			if err != nil {
				return nil, fmt.Errorf("open part: %w", err)
			}
			defer f.Close()
			body, err := io.ReadAll(io.LimitReader(f, MaxThemeZipSize+1))
			if err != nil {
				return nil, fmt.Errorf("read part: %w", err)
			}
			if int64(len(body)) > MaxThemeZipSize {
				return nil, fmt.Errorf("upload exceeds %d bytes", MaxThemeZipSize)
			}
			return body, nil
		}
	}
	return nil, errors.New("no file uploaded")
}
