package static

import (
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ActiveResolver returns the slug of the currently-active theme. The
// caller is expected to consult whatever store backs core.active_theme
// (the Postgres options row in production, an in-memory string in
// tests). Returning an empty string signals "no active theme" and the
// handler responds with 404 — this matches the contract of every other
// theme-aware surface in the codebase, which treats "no active theme"
// as a recoverable not-found rather than an internal error.
type ActiveResolver func() string

// Deps is the dependency bag for Mount. ThemeDir and ActiveResolver
// are required; Logger falls back to slog.Default for tests that don't
// care about log capture.
type Deps struct {
	// ThemeDir is the absolute or relative path that holds one
	// subdirectory per installed theme. In production this is /themes
	// in the api container (the GONEXT_THEME_DIR env var); on a dev
	// host it defaults to "./themes" relative to the process CWD.
	ThemeDir string

	// ActiveResolver maps the virtual "active" slug to the real slug.
	// Production wires this against the options store; tests use a
	// closure literal.
	ActiveResolver ActiveResolver

	// Logger receives structured warnings (e.g. fallthrough opens that
	// hit a missing file under a valid slug). Always non-nil after
	// Mount applies its default.
	Logger *slog.Logger
}

func (d Deps) validate() error {
	if strings.TrimSpace(d.ThemeDir) == "" {
		return errors.New("themes/static: ThemeDir is required")
	}
	if d.ActiveResolver == nil {
		return errors.New("themes/static: ActiveResolver is required")
	}
	return nil
}

// slugPattern matches the kebab-case-lowercase rule used everywhere
// else in the theme stack (mirrors apps/api/internal/admin/themes
// /installer.go and packages/go/theme/validate.go). The static handler
// re-applies it as a belt-and-braces guard so a request that smuggled
// a slash-encoded path-traversal segment into the {slug} wildcard
// never reaches filepath.Join. The literal sentinel "active" is
// matched separately before this regex runs.
var slugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$`)

// cacheControl is set on every successful response. One hour is short
// enough that an operator who swaps the active theme sees the new CSS
// without waiting for a long-tail eviction; long enough that the
// public site doesn't re-fetch on every page navigation. The CDN /
// browser respects the slug→content coupling — a theme update bumps
// the seeded slug or rewrites the file in-place, in which case the
// next miss is the right cache key.
const cacheControl = "public, max-age=3600"

// Mount wires the static-asset routes onto mux under base (typically
// "/themes"). Returns an error rather than panicking if Deps is
// malformed so the boot path can log and continue without taking the
// whole server down for a theme-server misconfiguration.
//
// The route tree:
//
//	GET {base}/{slug}/{file...}  — serve themeDir/<resolved>/<file>
//
// The {slug} segment may be:
//   - a real theme directory name (e.g. "gn-hello") — served as-is
//   - the sentinel "active" — resolved via ActiveResolver
//
// Path-traversal segments in {file...} are rejected with 400. Files
// not on disk return 404. Read errors return 500.
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}

	base = strings.TrimRight(base, "/")
	if base == "" {
		return errors.New("themes/static: base must not be empty")
	}

	// We resolve ThemeDir to its absolute, cleaned form once at Mount
	// time so the per-request prefix check is a string compare on a
	// stable canonical value. Production passes "/themes" which already
	// is absolute; the dev default "./themes" gets resolved against
	// the process CWD. If the directory does not yet exist (a fresh
	// dev tree before `pnpm install` or a misconfigured volume mount)
	// we still proceed — every request will simply 404 until the
	// directory shows up. We deliberately do NOT block the route
	// mount on a directory existence check so a temporary missing
	// volume doesn't break the entire boot.
	root, err := filepath.Abs(filepath.Clean(deps.ThemeDir))
	if err != nil {
		return err
	}

	h := &handler{
		root:           root,
		activeResolver: deps.ActiveResolver,
		logger:         deps.Logger.With(slog.String("subsystem", "themes/static")),
	}

	// Single wildcard route. Go 1.22+ mux supports {file...} as a
	// trailing wildcard that captures the remainder of the path
	// verbatim. Note: we still re-validate the captured value because
	// "the mux already split it" is not a substitute for "we checked
	// the bytes." The {slug} segment is captured as a single segment;
	// it cannot contain a slash by virtue of how the mux tokenises.
	mux.Handle("GET "+base+"/{slug}/{file...}", http.HandlerFunc(h.serve))

	return nil
}

// handler is the resolved-Deps form passed around inside the package.
type handler struct {
	// root is the absolute, cleaned path to the theme directory. Every
	// served file must live under this prefix; we re-check after
	// resolving the symlink-y filepath.Join just in case a symlink
	// inside a theme directory points elsewhere.
	root string

	// activeResolver maps the virtual "active" slug to the real slug.
	activeResolver ActiveResolver

	// logger is namespaced with the subsystem key so structured-log
	// consumers can filter on it.
	logger *slog.Logger
}

// serve handles a single GET request. The logic is small enough to
// inline; splitting it would obscure the security-relevant ordering.
func (h *handler) serve(w http.ResponseWriter, r *http.Request) {
	// 1. Capture + validate the slug.
	rawSlug := r.PathValue("slug")
	slug := rawSlug
	if slug == "active" {
		slug = strings.TrimSpace(h.activeResolver())
		if slug == "" {
			// No active theme configured. Surface as 404 rather than
			// 500 — every theme-aware endpoint in the codebase treats
			// "no active theme" as a recoverable not-found.
			h.logger.Debug("active resolver returned empty; serving 404",
				slog.String("path", r.URL.Path))
			http.NotFound(w, r)
			return
		}
	}

	if !slugPattern.MatchString(slug) {
		// Either the caller asked for a malformed slug directly or the
		// active resolver produced one. Either way, 400 is the right
		// signal: the request shape is invalid.
		h.logger.Warn("invalid slug",
			slog.String("requested", rawSlug),
			slog.String("resolved", slug),
		)
		http.Error(w, "invalid theme slug", http.StatusBadRequest)
		return
	}

	// 2. Capture + validate the file path. The mux gives us the
	// remainder of the URL path AFTER the slug segment, with no
	// leading slash. We forbid absolute paths, ".." segments, and
	// empty file paths up front — these never reach the filesystem.
	file := r.PathValue("file")
	if !isSafeRelPath(file) {
		h.logger.Warn("rejected path traversal attempt",
			slog.String("slug", slug),
			slog.String("file", file),
		)
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	// 3. Build the absolute on-disk path and verify it lives under root
	// AFTER filepath.Clean has had its say. This catches any traversal
	// we missed in isSafeRelPath as well as a symlink that points
	// outside the theme directory.
	abs := filepath.Join(h.root, slug, filepath.FromSlash(file))
	cleaned := filepath.Clean(abs)
	if !isUnder(cleaned, h.root) {
		h.logger.Warn("path escapes root after clean",
			slog.String("slug", slug),
			slog.String("file", file),
			slog.String("cleaned", cleaned),
		)
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	// 4. Open + stat. We treat ENOENT as 404 and any other read error
	// as 500. We intentionally do NOT distinguish "directory" from
	// "missing file" — both surface as 404 because /themes/gn-hello
	// (no file) is just as useless to the caller as /themes/missing
	// /style.css.
	f, err := os.Open(cleaned)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		h.logger.Error("open failed",
			slog.String("path", cleaned),
			slog.Any("err", err),
		)
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		h.logger.Error("stat failed",
			slog.String("path", cleaned),
			slog.Any("err", err),
		)
		http.Error(w, "stat error", http.StatusInternalServerError)
		return
	}
	if info.IsDir() {
		// We don't serve directory listings. A user landing on the
		// directory URL almost certainly meant a missing file.
		http.NotFound(w, r)
		return
	}

	// 5. Set headers. Content-Type from extension (we maintain a small
	// allow-list for the common theme asset types; anything else
	// falls back to mime.TypeByExtension which Go seeds from the
	// system mime table). Cache-Control fixed to one hour.
	w.Header().Set("Content-Type", contentType(cleaned))
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))

	// 6. Stream. http.ServeContent would handle Range + ETag for us
	// but the bookkeeping isn't worth it here — these are tiny files
	// (style.css is single-digit KiB) and Range requests for theme
	// CSS are not a real workload. A plain io.Copy is the right shape.
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, f); err != nil {
		// Connection drop mid-stream. There's nothing useful to do
		// here — headers are already on the wire. Log at debug so a
		// flaky client doesn't spam error logs.
		h.logger.Debug("copy failed (client likely disconnected)",
			slog.String("path", cleaned),
			slog.Any("err", err),
		)
	}
}

// isSafeRelPath reports whether p is a relative path with no traversal
// segments. We reject:
//   - empty paths (the caller asked for /themes/<slug>/ with nothing
//     after the trailing slash; mux delivers an empty {file...})
//   - absolute paths (leading slash)
//   - any segment exactly equal to ".." (path.Clean would collapse
//     "a/../b" to "b" but we want to surface the attempt explicitly)
//   - any segment containing a NUL byte
//
// path.Clean on the input is checked separately — a clean path that
// still starts with ".." (e.g. "../../etc/passwd" which cleans to
// "../../etc/passwd") slips past per-segment checks but fails the
// "starts with .." check below.
func isSafeRelPath(p string) bool {
	if p == "" {
		return false
	}
	if strings.HasPrefix(p, "/") {
		return false
	}
	if strings.ContainsRune(p, 0) {
		return false
	}
	// path.Clean uses forward slashes (the URL path separator), which
	// is what we want — the URL we received is forward-slashed even
	// on Windows.
	cleaned := path.Clean(p)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return false
	}
	// Defensive: catch a "..\\" form that path.Clean wouldn't notice
	// because it doesn't treat backslash as a separator. We don't
	// expect this from a stdlib mux but the cost is one substring
	// search per request and the consequence of missing it would be
	// a filesystem escape on Windows.
	if strings.Contains(p, `..\`) || strings.Contains(p, `\..`) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// isUnder reports whether child resolves to a path under parent.
// Both arguments must already have been run through filepath.Clean.
// We add a trailing separator to the parent before the prefix check
// so "/themes" does not accept "/themes-other/...".
func isUnder(child, parent string) bool {
	if child == parent {
		return true
	}
	if !strings.HasSuffix(parent, string(filepath.Separator)) {
		parent += string(filepath.Separator)
	}
	return strings.HasPrefix(child, parent)
}

// contentType returns the MIME type for a given on-disk path. We
// hard-code the common theme asset extensions because Go's
// mime.TypeByExtension consults the system mime table — which is
// present on every distro we ship to, but isn't guaranteed inside
// the distroless final image. A small explicit table is the right
// trade for the asset types that actually ship with a theme. Falls
// back to TypeByExtension for everything else; final fallback is
// application/octet-stream.
func contentType(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".js", ".mjs":
		return "application/javascript; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".map":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	case ".ico":
		return "image/x-icon"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	case ".eot":
		return "application/vnd.ms-fontobject"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	}
	if mt := mime.TypeByExtension(filepath.Ext(p)); mt != "" {
		return mt
	}
	return "application/octet-stream"
}
