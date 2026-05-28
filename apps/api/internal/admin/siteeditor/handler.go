// Package siteeditor implements the admin Site Editor REST surface
// — the file-based variant. Operators editing the header / footer /
// other declared template parts of the active theme list, fetch, and
// write the raw HTML/template bytes through these routes.
//
// This package is intentionally distinct from
// apps/api/internal/admin/site_editor — that one persists block-tree
// overrides in the options table and is wired at /admin/site_editor,
// while this one writes back to themes/{active}/parts/{name}.html for
// the simpler "edit the template part on disk" workflow at the URL the
// admin Site Editor page (apps/admin/.../appearance/site-editor) calls
// when it falls back to raw content editing.
//
// Route tree:
//
//	GET    /api/v1/admin/site-editor/parts            — list parts of the active theme
//	GET    /api/v1/admin/site-editor/parts/{name}     — read one part's content
//	PUT    /api/v1/admin/site-editor/parts/{name}     — write a part's content back to disk
//
// Capability: manage_themes (theme.edit_parts also accepted) — operators
// without it get 403.
package siteeditor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/theme"
)

// maxPartBytes caps the per-part request body for PUT writes. The HTML
// for a typical header/footer template part is under 4 KiB; a generous
// 256 KiB ceiling stops a hostile client from chewing on disk without
// bounding what realistic themes can declare.
const maxPartBytes = 256 * 1024

// ErrNoActiveTheme is returned by ActiveThemeSlug when no theme row
// exists. Surface as 503 — the editor surface cannot do its job on a
// fresh install before the seeder has run.
var ErrNoActiveTheme = errors.New("siteeditor: no active theme")

// ActiveResolver returns the active theme slug. Production wires this
// to the same options-table read the rest of the admin theme surfaces
// use; tests supply a closure that returns a fixed slug.
type ActiveResolver func(ctx context.Context) (string, error)

// ManifestLoader returns the parsed theme manifest for slug. The
// handler reads the manifest to learn which template parts the theme
// declares (theme.json#templateParts is the source of truth — a part
// the theme didn't declare is not editable, even if a file with that
// name happens to exist).
type ManifestLoader func(ctx context.Context, slug string) (*theme.ThemeJSON, error)

// Deps is the dependency bag for Mount. All fields except Logger are
// required.
type Deps struct {
	// ThemeDir is the on-disk root that holds installed themes. The
	// handler joins this with the active slug plus "parts" to resolve
	// each part file. Typically "./themes" in dev and "/var/lib/gonext/
	// themes" in containers.
	ThemeDir string

	// Active resolves the active theme slug on each request. Re-reading
	// per request means an in-flight theme switch surfaces immediately
	// without rebooting the API.
	Active ActiveResolver

	// Loader returns the parsed manifest for slug. Production passes
	// customizer.FilesystemLoader; tests pass a closure that returns a
	// hand-built ThemeJSON value.
	Loader ManifestLoader

	// Policy resolves the manage_themes capability check.
	Policy policy.Policy

	// Logger receives structured log lines. nil falls back to
	// slog.Default — useful for tests but production wiring should
	// always pass a service logger.
	Logger *slog.Logger
}

func (d Deps) validate() error {
	if strings.TrimSpace(d.ThemeDir) == "" {
		return errors.New("admin/siteeditor: ThemeDir is required")
	}
	if d.Active == nil {
		return errors.New("admin/siteeditor: Active resolver is required")
	}
	if d.Loader == nil {
		return errors.New("admin/siteeditor: Loader is required")
	}
	if d.Policy == nil {
		return errors.New("admin/siteeditor: Policy is required")
	}
	return nil
}

// Handler is the resolved-deps form passed around inside the package.
// Built once by Mount and shared across the registered routes.
type Handler struct {
	themeDir string
	active   ActiveResolver
	loader   ManifestLoader
	policy   policy.Policy
	logger   *slog.Logger
}

// Mount wires the site-editor routes onto mux under base (typically
// "/api/v1/admin/site-editor"). Returns an error rather than panicking
// if Deps is malformed so the boot path surfaces it cleanly.
func Mount(mux *http.ServeMux, base string, deps Deps) (*Handler, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	h := &Handler{
		themeDir: deps.ThemeDir,
		active:   deps.Active,
		loader:   deps.Loader,
		policy:   deps.Policy,
		logger:   deps.Logger.With(slog.String("component", "admin.siteeditor")),
	}

	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base+"/parts", h.gate(h.list))
	mux.Handle("GET "+base+"/parts/{name}", h.gate(h.get))
	mux.Handle("PUT "+base+"/parts/{name}", h.gate(h.put))
	return h, nil
}

// gate wraps a handler with the auth + manage_themes capability check.
// We accept either CapManageThemes or the narrower CapThemeEditParts —
// operators who hold either may use the site editor.
func (h *Handler) gate(next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		// Either capability authorizes the surface. manage_themes is the
		// canonical site-editor cap; theme.edit_parts is the narrower
		// surface and exists so a constrained role can still write parts.
		if d := h.policy.Can(pr, policy.CapManageThemes, nil); d.Allowed {
			next(w, r, pr)
			return
		}
		if d := h.policy.Can(pr, policy.CapThemeEditParts, nil); d.Allowed {
			next(w, r, pr)
			return
		}
		router.WriteError(w, http.StatusForbidden, "forbidden",
			"requires manage_themes or theme.edit_parts capability")
	})
}

// Part is one template part as returned by the API. The shape is
// intentionally narrow: name + area metadata + the raw on-disk content.
type Part struct {
	// Name is the slug under themes/{slug}/parts/{name}.html. The
	// renderer keys off this name when resolving the part.
	Name string `json:"name"`

	// Area is the canonical part area declared by theme.json:
	// "header", "footer", "sidebar", "uncategorized". The admin UI
	// groups the sidebar by area when there are many parts.
	Area string `json:"area"`

	// Title is the human-readable label declared in
	// theme.json#templateParts. Falls back to Name when empty.
	Title string `json:"title,omitempty"`

	// Content is the raw HTML/template bytes from
	// themes/{slug}/parts/{name}.html. Empty string when the file
	// doesn't exist on disk yet (the editor opens a blank canvas).
	Content string `json:"content"`
}

// listResponse is the GET /parts payload.
type listResponse struct {
	Theme string `json:"theme"`
	Parts []Part `json:"parts"`
}

// list handles GET /parts. It walks the active theme's
// templateParts declaration, reads each part's on-disk content, and
// returns the joined list. A missing file is surfaced as an empty
// Content so the editor can open it as a blank canvas — declared but
// not yet authored is a valid state.
func (h *Handler) list(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	slug, err := h.active(r.Context())
	if err != nil {
		h.writeActiveError(w, r, err)
		return
	}
	if slug == "" {
		router.WriteError(w, http.StatusServiceUnavailable, "no_active_theme",
			"no theme is currently active; run the installer or set core.active_theme")
		return
	}

	manifest, err := h.loader(r.Context(), slug)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/siteeditor.list: load manifest failed",
			slog.String("slug", slug),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "theme_load_failed",
			fmt.Sprintf("failed to load theme %q", slug))
		return
	}

	out := make([]Part, 0, len(manifest.TemplateParts))
	for _, p := range manifest.TemplateParts {
		content, rerr := h.readPart(slug, p.Name)
		if rerr != nil {
			h.logger.ErrorContext(r.Context(), "admin/siteeditor.list: read part failed",
				slog.String("slug", slug),
				slog.String("part", p.Name),
				slog.Any("err", rerr),
			)
			// Surface an empty body so the operator can author the part
			// from scratch — one corrupt file shouldn't blank the
			// sidebar.
			content = ""
		}
		out = append(out, Part{
			Name:    p.Name,
			Area:    canonicalArea(p.Area),
			Title:   p.Title,
			Content: content,
		})
	}
	// Stable ordering so the admin UI's left-rail list doesn't
	// reshuffle between requests.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	router.WriteJSON(w, http.StatusOK, listResponse{Theme: slug, Parts: out})
}

// get handles GET /parts/{name}. Same content read as list, but for a
// single part — used by the admin editor when opening a specific part
// without re-fetching the whole list.
func (h *Handler) get(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	name := r.PathValue("name")
	if err := validatePartName(name); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_name", err.Error())
		return
	}

	slug, err := h.active(r.Context())
	if err != nil {
		h.writeActiveError(w, r, err)
		return
	}
	manifest, err := h.loader(r.Context(), slug)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/siteeditor.get: load manifest failed",
			slog.String("slug", slug),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "theme_load_failed",
			fmt.Sprintf("failed to load theme %q", slug))
		return
	}

	decl, ok := findTemplatePart(manifest, name)
	if !ok {
		router.WriteError(w, http.StatusNotFound, "part_not_found",
			"the requested part is not declared by the active theme")
		return
	}

	content, err := h.readPart(slug, name)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/siteeditor.get: read part failed",
			slog.String("slug", slug),
			slog.String("part", name),
			slog.Any("err", err),
		)
		// Same posture as list — a missing file becomes an empty body.
		content = ""
	}

	router.WriteJSON(w, http.StatusOK, Part{
		Name:    decl.Name,
		Area:    canonicalArea(decl.Area),
		Title:   decl.Title,
		Content: content,
	})
}

// putRequest is the body shape for PUT /parts/{name}.
type putRequest struct {
	Content string `json:"content"`
}

// put handles PUT /parts/{name}. The handler writes the supplied
// content back to themes/{slug}/parts/{name}.html, creating the parts
// directory if it doesn't exist. The write is atomic via a temp file +
// rename so a partial write on disk full doesn't leave the operator
// looking at a corrupt header.
//
// Validation:
//   - The part name must match the theme's declared templateParts —
//     writing an undeclared name returns 404. This is the security
//     hinge: it bounds the file path to the set of names the theme
//     manifests claim to own.
//   - The part name must be a single segment (no '/', '\\', '..', no
//     leading/trailing whitespace). Path traversal returns 400.
//   - Content size is capped at maxPartBytes.
func (h *Handler) put(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	name := r.PathValue("name")
	if err := validatePartName(name); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_name", err.Error())
		return
	}

	slug, err := h.active(r.Context())
	if err != nil {
		h.writeActiveError(w, r, err)
		return
	}
	manifest, err := h.loader(r.Context(), slug)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/siteeditor.put: load manifest failed",
			slog.String("slug", slug),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "theme_load_failed",
			fmt.Sprintf("failed to load theme %q", slug))
		return
	}
	decl, ok := findTemplatePart(manifest, name)
	if !ok {
		router.WriteError(w, http.StatusNotFound, "part_not_found",
			"the requested part is not declared by the active theme")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxPartBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		router.WriteError(w, http.StatusRequestEntityTooLarge, "body_too_large",
			fmt.Sprintf("part payload must not exceed %d bytes", maxPartBytes))
		return
	}

	var req putRequest
	if jerr := json.Unmarshal(raw, &req); jerr != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}

	if err := h.writePart(slug, name, req.Content); err != nil {
		h.logger.ErrorContext(r.Context(), "admin/siteeditor.put: write failed",
			slog.String("slug", slug),
			slog.String("part", name),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error",
			"failed to save part content")
		return
	}

	h.logger.InfoContext(r.Context(), "admin/siteeditor: part written",
		slog.String("slug", slug),
		slog.String("part", name),
		slog.String("by", pr.UserID),
	)

	router.WriteJSON(w, http.StatusOK, Part{
		Name:    decl.Name,
		Area:    canonicalArea(decl.Area),
		Title:   decl.Title,
		Content: req.Content,
	})
}

// readPart reads themes/{slug}/parts/{name}.html. A missing file is
// not an error — declared-but-not-yet-authored is a legitimate state
// for the editor, so callers get an empty string instead.
func (h *Handler) readPart(slug, name string) (string, error) {
	path := h.partPath(slug, name)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// writePart writes content to themes/{slug}/parts/{name}.html
// atomically. We MkdirAll the parts directory first so a freshly
// installed theme without a parts/ folder still accepts writes.
func (h *Handler) writePart(slug, name, content string) error {
	dir := filepath.Join(h.themeDir, slug, "parts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("siteeditor: mkdir %q: %w", dir, err)
	}
	final := filepath.Join(dir, name+".html")
	tmp, err := os.CreateTemp(dir, "."+name+".html.*.tmp")
	if err != nil {
		return fmt.Errorf("siteeditor: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op on success after Rename
	if _, werr := io.WriteString(tmp, content); werr != nil {
		_ = tmp.Close()
		return fmt.Errorf("siteeditor: write temp: %w", werr)
	}
	if cerr := tmp.Close(); cerr != nil {
		return fmt.Errorf("siteeditor: close temp: %w", cerr)
	}
	if rerr := os.Rename(tmpName, final); rerr != nil {
		return fmt.Errorf("siteeditor: rename %q -> %q: %w", tmpName, final, rerr)
	}
	return nil
}

// partPath joins themeDir/{slug}/parts/{name}.html. Caller is
// responsible for having already validated name (validatePartName +
// findTemplatePart); this function does NOT re-check.
func (h *Handler) partPath(slug, name string) string {
	return filepath.Join(h.themeDir, slug, "parts", name+".html")
}

// writeActiveError translates the small set of "no active theme"
// sentinels into HTTP responses. Anything we don't recognise is logged
// and surfaced as 500.
func (h *Handler) writeActiveError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNoActiveTheme) {
		router.WriteError(w, http.StatusServiceUnavailable, "no_active_theme",
			"no theme is currently active; run the installer or set core.active_theme")
		return
	}
	h.logger.ErrorContext(r.Context(), "admin/siteeditor: active resolver failed",
		slog.Any("err", err))
	router.WriteError(w, http.StatusInternalServerError, "internal_error",
		"failed to resolve active theme")
}

// validatePartName guards the file-path projection. The name must be a
// single path segment with no separators, no parent-directory tokens,
// and no leading/trailing whitespace. Returns nil iff the name is safe
// to join with themeDir/{slug}/parts/ without traversing out.
//
// We mirror the same lower-case + alphanumeric + '-' / '_' shape the
// existing site_editor package uses for consistency, but the security
// check is the explicit absence of '/', '\\', '..', and any control
// chars.
func validatePartName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(name) != name {
		return errors.New("name must not contain leading or trailing whitespace")
	}
	if strings.ContainsAny(name, `/\`) {
		return errors.New("name must not contain path separators")
	}
	if name == "." || name == ".." || strings.Contains(name, "..") {
		return errors.New("name must not contain parent-directory tokens")
	}
	for _, ch := range name {
		switch {
		case ch >= 'a' && ch <= 'z':
		case ch >= '0' && ch <= '9':
		case ch == '-' || ch == '_':
		default:
			return errors.New("name must be lower-case alphanumeric with '-' or '_'")
		}
	}
	return nil
}

// findTemplatePart returns the TemplatePartDef for name iff the theme
// declares it. The boolean is the "is declared" flag; callers map
// false to 404.
func findTemplatePart(t *theme.ThemeJSON, name string) (theme.TemplatePartDef, bool) {
	if t == nil {
		return theme.TemplatePartDef{}, false
	}
	for _, p := range t.TemplateParts {
		if p.Name == name {
			return p, true
		}
	}
	return theme.TemplatePartDef{}, false
}

// canonicalArea normalizes the theme.json#templateParts.area value into
// the response shape callers expect. The admin UI keys its sidebar by
// the three canonical buckets — "header", "footer", "general" — so an
// empty or unrecognised area becomes "general" in the response.
//
// "uncategorized" (the theme.json default for non-header/non-footer
// parts) collapses to "general" too, because the admin UI only knows
// the three labels and a fourth would render as an empty group header.
func canonicalArea(area string) string {
	switch strings.ToLower(strings.TrimSpace(area)) {
	case "header":
		return "header"
	case "footer":
		return "footer"
	default:
		return "general"
	}
}
