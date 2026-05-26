// Package frontend is the host-side handler that serves plugin web/
// bundles as ES modules and composes the page-level import map
// browsers consult to resolve plugin specifiers (issue #206).
//
// Two distinct surfaces live here:
//
//   - /api/plugins/{slug}/web/{path...} — static delivery of the
//     plugin's web/ bundle entries. Each file is served as an
//     application/javascript ES module with strong, immutable
//     caching keyed on the bundle's SHA-256 hash. The handler emits
//     a Subresource-Integrity (SRI) header so the importer can pin
//     the bundle and refuse to execute a tampered byte stream.
//
//   - GET /api/plugins/import-map.json — the composed import map of
//     every active plugin's declared module exports. The admin
//     template renders this into a <script type="importmap"> tag at
//     the top of the document; browsers honor the map when resolving
//     bare specifiers issued by subsequent <script type="module">
//     tags.
//
// The package is intentionally narrow: it owns delivery + composition
// and nothing else. Manifest validation lives upstream (the
// frontend-host TS package has the validator; the Go side trusts the
// activation pipeline ran it). Sandboxing primitives — Trusted Types
// policy, SRI verification — are wired by the TS host package
// referenced via the import map's runtime/ paths.
package frontend

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// MaxBundleBytes caps a single served bundle file. 16 MiB matches the
// dev install path (apps/api/internal/plugins/dev). Production bundles
// are smaller than this in practice; the cap is the safety net.
const MaxBundleBytes = 16 << 20

// CacheMaxAge is the Cache-Control max-age applied to bundle responses.
// Bundle URLs are content-addressed by SHA-256 (the handler emits the
// hash in the SRI header AND the file path includes it via the
// admin's link composition), so a long TTL is safe. One year matches
// the immutable-cache convention.
const CacheMaxAge = 365 * 24 * time.Hour

// BundleEntry describes one ES-module file the plugin contributes.
// Bytes is the raw file contents; the handler computes the SHA-256
// once at Register time and reuses it for Cache, ETag, and SRI on
// every subsequent request.
type BundleEntry struct {
	// Path is the URL-tail under /api/plugins/{slug}/web/. Must not
	// contain "..", absolute paths, or backslashes. The handler
	// re-validates on every request as defense-in-depth.
	Path string
	// Bytes is the file contents. The handler owns the slice once
	// registered; callers must not mutate it.
	Bytes []byte
	// ContentType, when empty, defaults to "application/javascript"
	// since the surface is ES modules. Plugins shipping wasm or
	// source maps override it.
	ContentType string
}

// PluginBundle is one plugin's complete contribution to the frontend
// extensions surface. The Slug is the plugin identifier; Imports is
// the import-map fragment the plugin advertises (its declared module
// exports, bare specifier → resolved URL under the plugin's web/
// surface).
type PluginBundle struct {
	Slug    string
	Entries []BundleEntry
	// Imports is the set of bare specifiers this plugin exports to
	// the import map. Each key resolves to a URL relative to the
	// host origin (typically "/api/plugins/{slug}/web/foo.mjs").
	// The composer enforces uniqueness across slugs — two plugins
	// can't both claim the same bare specifier.
	Imports map[string]string
}

// Handler is the per-process registry of plugin bundles + the HTTP
// handlers that serve them. Construct via NewHandler; register plugin
// bundles via Register at activation, drop them via Unregister at
// deactivation.
//
// The handler is safe for concurrent Register / Unregister / Serve;
// the per-slug bundle map is guarded by RWMutex. Bundle bytes are
// stored in memory — they originate from the .gnplugin bundle which
// the lifecycle Manager already loaded — so serving is a cache hit
// every time.
type Handler struct {
	mu sync.RWMutex
	// bundles maps slug → indexed bundle (path → preBuiltEntry). A
	// preBuiltEntry carries the precomputed SHA-256 hash, base64-
	// encoded SRI string, and the immutable byte slice the handler
	// writes to the response.
	bundles map[string]map[string]preBuiltEntry
	// imports composes every active plugin's import-map fragment.
	// Keyed by bare specifier so the JSON serializer emits a stable
	// shape. Mutated only under mu.
	imports map[string]string
	// importsBySlug tracks the imports each slug contributed, so
	// Unregister can subtract precisely without re-walking every
	// registered bundle.
	importsBySlug map[string]map[string]string

	logger *slog.Logger
}

// preBuiltEntry is a BundleEntry plus the SHA-256 we compute once at
// Register so per-request handlers don't redo the hash.
type preBuiltEntry struct {
	bytes       []byte
	contentType string
	sriHash     string // "sha256-<base64>" — ready to drop into the integrity attribute
	etag        string // strong ETag, the same value as the SRI hex tail
}

// NewHandler builds an empty Handler. Logger defaults to slog.Default
// when nil.
func NewHandler(logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		bundles:       make(map[string]map[string]preBuiltEntry),
		imports:       make(map[string]string),
		importsBySlug: make(map[string]map[string]string),
		logger:        logger,
	}
}

// Register installs a plugin's bundle. Returns an error when the
// bundle is malformed (oversized file, "..", duplicate path) or when
// any of the imports collides with another slug's contribution. The
// caller is the lifecycle Manager at Activate; the error path bubbles
// up to a parked-Errored row so the operator sees the issue.
//
// Re-Register replaces the previous bundle in one pass — no half-state
// where some old files are still served and some are missing.
func (h *Handler) Register(b PluginBundle) error {
	if b.Slug == "" {
		return errors.New("frontend.Register: slug is required")
	}
	indexed, err := buildIndex(b.Entries)
	if err != nil {
		return fmt.Errorf("frontend.Register %q: %w", b.Slug, err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// Detect collisions in the import map. We do this BEFORE
	// committing the new entries so a conflicting registration leaves
	// the previous state intact.
	for spec := range b.Imports {
		owner, ok := h.imports[spec]
		if !ok {
			continue
		}
		// Same-slug re-registration is fine — Unregister-below will
		// drop the old import and the new one takes the same slot.
		if owner == b.Slug {
			continue
		}
		// Re-look it up via importsBySlug too: a stale entry would be
		// a programmer error here, but we'd rather refuse than allow
		// the new registration to silently overwrite.
		if _, isOurs := h.importsBySlug[b.Slug][spec]; !isOurs {
			return fmt.Errorf("frontend.Register %q: import %q already owned by plugin %q",
				b.Slug, spec, owner)
		}
	}
	// Subtract previous imports under this slug so a partial overlap
	// (some kept, some renamed) leaves the right composite state.
	for spec := range h.importsBySlug[b.Slug] {
		delete(h.imports, spec)
	}
	delete(h.importsBySlug, b.Slug)
	// Commit.
	h.bundles[b.Slug] = indexed
	if len(b.Imports) > 0 {
		cp := make(map[string]string, len(b.Imports))
		for k, v := range b.Imports {
			h.imports[k] = v
			cp[k] = v
		}
		h.importsBySlug[b.Slug] = cp
	}
	h.logger.Info("frontend: plugin bundle registered",
		slog.String("slug", b.Slug),
		slog.Int("files", len(indexed)),
		slog.Int("imports", len(b.Imports)),
	)
	return nil
}

// Unregister drops a slug's bundle and its contribution to the import
// map. Idempotent: an unknown slug returns nil silently.
func (h *Handler) Unregister(slug string) {
	if slug == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.bundles, slug)
	for spec := range h.importsBySlug[slug] {
		delete(h.imports, spec)
	}
	delete(h.importsBySlug, slug)
	h.logger.Info("frontend: plugin bundle unregistered", slog.String("slug", slug))
}

// ServeBundle implements the /api/plugins/{slug}/web/{path...} handler.
// Path traversal attempts ("..") 400 before any map lookup. An unknown
// slug or path 404s.
//
// Response headers:
//   - Content-Type: as registered (or application/javascript)
//   - Content-Length: precomputed from the byte slice
//   - Cache-Control: public, max-age=31536000, immutable
//   - ETag: the precomputed hex
//   - X-SRI: the precomputed sha256-<base64> string. The Go side
//     emits the same value in the import-map page metadata; the
//     admin uses both to fail closed on tampering.
func (h *Handler) ServeBundle(w http.ResponseWriter, r *http.Request) {
	slug, rel, ok := parseBundlePath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := safePath(rel); err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	h.mu.RLock()
	files, slugOK := h.bundles[slug]
	var entry preBuiltEntry
	var entryOK bool
	if slugOK {
		entry, entryOK = files[rel]
	}
	h.mu.RUnlock()
	if !slugOK || !entryOK {
		http.NotFound(w, r)
		return
	}
	// Conditional GET — if the caller sent the same ETag we already
	// computed at Register time, short-circuit with 304.
	if match := r.Header.Get("If-None-Match"); match != "" {
		if strings.Contains(match, entry.etag) {
			w.Header().Set("ETag", entry.etag)
			w.Header().Set("Cache-Control", cacheControlValue())
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Content-Type", entry.contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(entry.bytes)))
	w.Header().Set("Cache-Control", cacheControlValue())
	w.Header().Set("ETag", entry.etag)
	w.Header().Set("X-SRI", entry.sriHash)
	// Cross-origin isolation: plugin scripts MUST be loaded as ES
	// modules from the same origin; this header surfaces clearly in
	// dev tools when something tries to fetch the file as a classic
	// script.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(entry.bytes)
}

// ServeImportMap serves GET /api/plugins/import-map.json with the
// composed import map of all active plugin bundles. The admin template
// renders this URL into a <script type="importmap"> tag at the top of
// the document.
//
// The output is stable: the imports map iteration order is sorted so
// two calls with identical state produce byte-identical responses
// (essential for caching and for tests).
func (h *Handler) ServeImportMap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.mu.RLock()
	imports := make(map[string]string, len(h.imports))
	for k, v := range h.imports {
		imports[k] = v
	}
	h.mu.RUnlock()

	// Stable JSON: sort keys.
	keys := make([]string, 0, len(imports))
	for k := range imports {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(`{"imports":{`)
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		key, _ := json.Marshal(k)
		val, _ := json.Marshal(imports[k])
		b.Write(key)
		b.WriteString(":")
		b.Write(val)
	}
	b.WriteString("}}\n")

	body := b.String()
	w.Header().Set("Content-Type", "application/importmap+json")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	// Short TTL — operators routinely flip plugins on/off and the
	// import map needs to follow. 60s gives a CDN something to cache
	// but recovers quickly from a deactivation.
	w.Header().Set("Cache-Control", "public, max-age=60, must-revalidate")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = io.WriteString(w, body)
}

// ImportMapSnapshot returns the composed map of bare specifiers to URLs
// for callers that prefer to embed the map directly into the document
// rather than fetch /api/plugins/import-map.json. Used by the SSR
// template.
//
// The snapshot is a sorted-key map for stable output. Callers may
// safely retain the returned map — it is a fresh allocation, not the
// handler's internal state.
func (h *Handler) ImportMapSnapshot() map[string]string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]string, len(h.imports))
	for k, v := range h.imports {
		out[k] = v
	}
	return out
}

// SRIByURL returns the precomputed Subresource-Integrity hash for a
// served URL, or empty string when the URL is unknown. Used by the
// admin SSR template to fail closed on tampering: the integrity
// attribute on every <script> generated for plugin imports is filled
// from this lookup.
//
// urlPath is the URL the import map produced, e.g.
// "/api/plugins/seo/web/seo.mjs".
func (h *Handler) SRIByURL(urlPath string) string {
	slug, rel, ok := parseBundlePath(urlPath)
	if !ok {
		return ""
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if files, ok := h.bundles[slug]; ok {
		if entry, ok := files[rel]; ok {
			return entry.sriHash
		}
	}
	return ""
}

// ImportMapScriptTag composes the `<script type="importmap">` tag the
// SSR template embeds in the document head. The output is stable
// (sorted keys) and small enough to inline; consumers that prefer the
// fetched form can use ServeImportMap instead.
func (h *Handler) ImportMapScriptTag() string {
	snapshot := h.ImportMapSnapshot()
	keys := make([]string, 0, len(snapshot))
	for k := range snapshot {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(`<script type="importmap">{"imports":{`)
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		key, _ := json.Marshal(k)
		val, _ := json.Marshal(snapshot[k])
		b.Write(key)
		b.WriteString(":")
		b.Write(val)
	}
	b.WriteString("}}</script>")
	return b.String()
}

// buildIndex precomputes the SHA-256 + base64 SRI + ETag for every
// entry in the bundle. Returns the path → preBuiltEntry map, or an
// error on the first malformed entry.
func buildIndex(entries []BundleEntry) (map[string]preBuiltEntry, error) {
	out := make(map[string]preBuiltEntry, len(entries))
	for _, e := range entries {
		if err := safePath(e.Path); err != nil {
			return nil, fmt.Errorf("entry %q: %w", e.Path, err)
		}
		if int64(len(e.Bytes)) > MaxBundleBytes {
			return nil, fmt.Errorf("entry %q exceeds %d bytes", e.Path, MaxBundleBytes)
		}
		if _, dup := out[e.Path]; dup {
			return nil, fmt.Errorf("duplicate entry path %q", e.Path)
		}
		sum := sha256.Sum256(e.Bytes)
		// SRI is "sha256-" + base64(raw 32 bytes).
		sri := "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
		ct := e.ContentType
		if ct == "" {
			ct = "application/javascript"
		}
		// ETag = strong, double-quoted hex of the digest. Matches the
		// stdlib's net/http.ServeContent convention.
		etag := `"` + hexDigest(sum[:]) + `"`
		out[e.Path] = preBuiltEntry{
			bytes:       e.Bytes,
			contentType: ct,
			sriHash:     sri,
			etag:        etag,
		}
	}
	return out, nil
}

// parseBundlePath cracks "/api/plugins/{slug}/web/{path}" into its
// slug and trailing path components. Returns ok=false for any other
// shape so the handler 404s without further inspection.
func parseBundlePath(p string) (slug, rel string, ok bool) {
	const prefix = "/api/plugins/"
	if !strings.HasPrefix(p, prefix) {
		return "", "", false
	}
	rest := p[len(prefix):]
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		return "", "", false
	}
	slug = rest[:slash]
	tail := rest[slash+1:]
	const webPrefix = "web/"
	if !strings.HasPrefix(tail, webPrefix) {
		return "", "", false
	}
	rel = tail[len(webPrefix):]
	if rel == "" {
		return "", "", false
	}
	return slug, rel, true
}

// safePath rejects path traversal attempts. The validator runs on
// every request as defense-in-depth even though buildIndex enforces
// the same rules at Register time — a buggy collaborator should not
// be able to bypass the check by injecting a malformed registration.
func safePath(p string) error {
	if p == "" {
		return errors.New("empty path")
	}
	if strings.ContainsAny(p, "\\") {
		return errors.New("backslash in path")
	}
	cleaned := path.Clean("/" + p)
	if cleaned != "/"+p {
		return errors.New("path not in canonical form")
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "..") || strings.Contains(p, "/..") {
		return errors.New("path traversal")
	}
	return nil
}

// hexDigest returns the lowercase hex of b. Inlined here so we don't
// pull in encoding/hex for a 32-byte input.
func hexDigest(b []byte) string {
	const hexChars = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexChars[c>>4]
		out[i*2+1] = hexChars[c&0x0f]
	}
	return string(out)
}

// cacheControlValue is the precomputed Cache-Control header for
// content-addressed assets. Lifted out of the per-request hot path so
// no allocation happens per response.
func cacheControlValue() string {
	return fmt.Sprintf("public, max-age=%d, immutable", int(CacheMaxAge.Seconds()))
}
