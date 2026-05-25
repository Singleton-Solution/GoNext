package img

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/media"
	"github.com/Singleton-Solution/GoNext/packages/go/media/imgproxy"
)

// AssetLookup is the subset of the media row the handler needs to
// serve a request. Keeping the lookup interface small (rather than
// importing the full admin/media.Store) means the handler can be
// wired against any store backend — the admin's MemoryStore, the
// future Postgres store, or a test fake — without an import cycle
// through the admin package.
//
// LookupByID returns the storage key + MIME type of the source row
// or ErrAssetNotFound when no row matches. The handler does not
// need any of the other admin/media.Asset fields (alt_text, etc.)
// for the proxy path, so they're omitted from the return shape.
type AssetLookup interface {
	LookupByID(ctx context.Context, id string) (AssetRef, error)
}

// AssetRef is the minimal asset descriptor returned by AssetLookup.
type AssetRef struct {
	// ID echoes the requested asset ID; useful for cache key derivation
	// when the lookup applies normalisation (e.g., lower-casing UUID
	// hex digits).
	ID string

	// StorageKey is the source object's key in the storage backend.
	// Passed straight to Source.GetObject.
	StorageKey string

	// MIMEType is the row's recorded MIME. The handler uses it to
	// short-circuit non-image rows with a 415; the transformer
	// will reject them too, but the early check produces a cleaner
	// error and avoids paying for the storage fetch.
	MIMEType string
}

// Source is the read side of the storage backend. The handler asks
// for the source bytes by storage key; the wire shape is identical
// to imageproc.Source (apps/api can satisfy both interfaces with a
// single adapter).
type Source interface {
	GetObject(ctx context.Context, key string) ([]byte, error)
}

// ErrAssetNotFound is the sentinel AssetLookup implementations
// return when no row matches the requested ID. The handler
// translates to HTTP 404.
var ErrAssetNotFound = errors.New("img: asset not found")

// ErrSourceNotFound is the sentinel Source implementations return
// when the storage key resolves to no object. Surfaced as 404 — the
// caller asked for a real asset whose source bytes have gone
// missing.
var ErrSourceNotFound = errors.New("img: source object not found")

// Deps is the dependency bag for Mount. Lookup, Source, Cache, and
// Coalescer are required; Transformer defaults to imgproxy.Default()
// (which picks govips/stdlib at first use); Logger defaults to
// slog.Default.
type Deps struct {
	Lookup      AssetLookup
	Source      Source
	Cache       *imgproxy.Cache
	Coalescer   *media.Coalescer
	Transformer imgproxy.Transformer
	Logger      *slog.Logger

	// MaxSourceBytes caps the source object size accepted by the
	// proxy. Zero means "no cap" — production wiring should set
	// this to match the upload limit (50 MiB) so a corrupt row
	// can't OOM the API process. Tests can leave it zero.
	MaxSourceBytes int64
}

func (d Deps) validate() error {
	if d.Lookup == nil {
		return errors.New("rest/img: Lookup is required")
	}
	if d.Source == nil {
		return errors.New("rest/img: Source is required")
	}
	if d.Cache == nil {
		return errors.New("rest/img: Cache is required")
	}
	if d.Coalescer == nil {
		return errors.New("rest/img: Coalescer is required")
	}
	return nil
}

// Handler is the HTTP entry point. Constructed once at boot via
// Mount and reused; safe for concurrent requests because every
// piece of mutable state (cache, coalescer) is itself safe.
type Handler struct {
	lookup    AssetLookup
	source    Source
	cache     *imgproxy.Cache
	coalescer *media.Coalescer
	transform imgproxy.Transformer
	logger    *slog.Logger
	maxBytes  int64
}

// Mount wires the public /img routes onto mux under base (typically
// "/img"). The router pattern uses Go 1.22's path-value syntax so
// the assetID and spec arrive via r.PathValue.
//
// Route tree:
//
//	GET {base}/{id}/{spec} — render or serve from cache
//	HEAD {base}/{id}/{spec} — same shape, body suppressed
//
// HEAD is wired explicitly so a caller checking "is this variant
// pre-rendered?" without paying for the body can call it; the
// implementation runs the same coalesced render and lets the stdlib
// drop the body.
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Transformer == nil {
		deps.Transformer = imgproxy.Default()
	}

	h := &Handler{
		lookup:    deps.Lookup,
		source:    deps.Source,
		cache:     deps.Cache,
		coalescer: deps.Coalescer,
		transform: deps.Transformer,
		logger:    deps.Logger,
		maxBytes:  deps.MaxSourceBytes,
	}

	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base+"/{id}/{spec}", http.HandlerFunc(h.serve))
	mux.Handle("HEAD "+base+"/{id}/{spec}", http.HandlerFunc(h.serve))
	return nil
}

// serve is the request handler. The flow:
//
//  1. Parse + validate the spec (400 on failure).
//  2. Look up the asset (404 on missing row).
//  3. Reject non-image MIME types early (415).
//  4. Check the disk cache. On hit, stream the entry with the
//     immutable Cache-Control header.
//  5. On miss, ask the coalescer to render. The coalescer's leader
//     fetches the source bytes, runs the transformer, writes the
//     cache entry, and returns the bytes; followers reuse the
//     leader's bytes.
//  6. Stream the rendered bytes with the same cache headers.
func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	specRaw := r.PathValue("spec")
	id := r.PathValue("id")

	spec, err := imgproxy.Parse(specRaw)
	if err != nil {
		h.writeText(w, http.StatusBadRequest, err.Error())
		return
	}

	ref, err := h.lookup.LookupByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrAssetNotFound) {
			h.writeText(w, http.StatusNotFound, "asset not found")
			return
		}
		h.logger.WarnContext(r.Context(),
			"rest/img: lookup failed",
			slog.String("id", id),
			slog.String("err", err.Error()),
		)
		h.writeText(w, http.StatusInternalServerError, "lookup failed")
		return
	}

	if !isImageMIME(ref.MIMEType) {
		h.writeText(w, http.StatusUnsupportedMediaType, "asset is not an image")
		return
	}

	// Fast path: serve from cache without touching the coalescer.
	// The cache layer is a single os.Open so the overhead is the
	// syscall and a stat — much cheaper than the singleflight
	// channel rendezvous.
	if entry, err := h.cache.Get(ref.ID, spec); err == nil {
		h.writeCachedEntry(w, r, entry)
		return
	} else if !errors.Is(err, imgproxy.ErrCacheMiss) {
		h.logger.WarnContext(r.Context(),
			"rest/img: cache read failed",
			slog.String("id", ref.ID),
			slog.String("spec", spec.Canonical()),
			slog.String("err", err.Error()),
		)
		// Fall through to render — a cache read failure shouldn't
		// turn a legitimate request into a 500.
	}

	// Slow path: render via coalescer. The key includes both id and
	// spec so concurrent requests for different specs of the same
	// asset don't serialise; concurrent requests for the same spec
	// collapse to one leader.
	coalKey := ref.ID + "|" + spec.Canonical()
	bytesRendered, _, err := h.coalescer.Get(r.Context(), coalKey, func() ([]byte, error) {
		return h.render(r.Context(), ref, spec)
	})
	if err != nil {
		h.handleRenderError(w, r, err)
		return
	}

	// After the coalescer returned, the leader may have written the
	// cache entry. Try the cache one more time — that lets us
	// stream from disk (and pick up the on-disk ModTime + size) for
	// followers, rather than re-buffering the bytes in memory.
	if entry, err := h.cache.Get(ref.ID, spec); err == nil {
		h.writeCachedEntry(w, r, entry)
		return
	}
	// If the cache read fails post-render, serve from the in-memory
	// bytes. The Last-Modified is "now" because we just rendered.
	h.writeRenderedBytes(w, r, spec, bytesRendered)
}

// render runs the full miss path: fetch source bytes, transform,
// write cache. Called from inside the coalescer's leader closure so
// only one render runs per (id, spec) at a time.
func (h *Handler) render(ctx context.Context, ref AssetRef, spec imgproxy.Spec) ([]byte, error) {
	body, err := h.source.GetObject(ctx, ref.StorageKey)
	if err != nil {
		return nil, err
	}
	if h.maxBytes > 0 && int64(len(body)) > h.maxBytes {
		return nil, fmt.Errorf("rest/img: source exceeds max bytes (%d > %d)", len(body), h.maxBytes)
	}

	res, err := h.transform.Transform(ctx, bytes.NewReader(body), spec)
	if err != nil {
		return nil, err
	}

	if err := h.cache.Put(ref.ID, spec, res.Bytes); err != nil {
		// Cache write failures are non-fatal — we still have the
		// bytes in memory and can serve the request. The next
		// request will re-render, which is wasteful but correct.
		h.logger.WarnContext(ctx,
			"rest/img: cache write failed",
			slog.String("id", ref.ID),
			slog.String("spec", spec.Canonical()),
			slog.String("err", err.Error()),
		)
	}
	return res.Bytes, nil
}

// writeCachedEntry streams an os.File-backed cache entry. The
// returned reader is closed by the io.Copy + defer pair.
func (h *Handler) writeCachedEntry(w http.ResponseWriter, r *http.Request, entry *imgproxy.CacheEntry) {
	defer entry.Reader.Close()
	setImageHeaders(w, entry.MIMEType, entry.Size, entry.ModTime)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, entry.Reader); err != nil {
		// Client disconnected mid-stream; the bytes that did land
		// are still useful to whatever proxy is in front of us.
		h.logger.DebugContext(r.Context(),
			"rest/img: stream cached entry interrupted",
			slog.String("err", err.Error()),
		)
	}
}

// writeRenderedBytes serves in-memory bytes (post-render, when the
// cache write failed). ModTime is "now" because the cache entry
// doesn't exist to pull a real mtime from.
func (h *Handler) writeRenderedBytes(w http.ResponseWriter, r *http.Request, spec imgproxy.Spec, body []byte) {
	setImageHeaders(w, spec.Format.MIMEType(), int64(len(body)), time.Now())
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

// handleRenderError maps the transformer / source errors to HTTP
// status codes. The errors.Is check chain mirrors the documented
// status table in doc.go.
func (h *Handler) handleRenderError(w http.ResponseWriter, r *http.Request, err error) {
	ctx := r.Context()
	switch {
	case errors.Is(err, ErrSourceNotFound):
		h.writeText(w, http.StatusNotFound, "source not found")
	case errors.Is(err, imgproxy.ErrUnsupportedFormat):
		h.writeText(w, http.StatusUnsupportedMediaType, "source format not supported")
	case errors.Is(err, imgproxy.ErrEmptySource):
		h.writeText(w, http.StatusUnsupportedMediaType, "source is empty")
	case errors.Is(err, context.Canceled):
		// Client gave up. We didn't fail to do our job; don't log
		// at warn level for what is normally a benign event.
		h.writeText(w, 499 /* client closed request */, "client cancelled")
	case errors.Is(err, context.DeadlineExceeded):
		h.writeText(w, http.StatusGatewayTimeout, "render timed out")
	default:
		h.logger.WarnContext(ctx,
			"rest/img: render failed",
			slog.String("err", err.Error()),
		)
		h.writeText(w, http.StatusInternalServerError, "render failed")
	}
}

// writeText emits a plain-text error response. Used by every
// non-2xx path so the operator inspecting the wire gets a
// human-readable reason rather than an empty body.
func (h *Handler) writeText(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(msg))
}

// setImageHeaders applies the response headers used on every 2xx
// path. The Cache-Control value is the immutable-1-year shape
// recommended by the issue brief; Vary: Accept lets a future
// content-negotiation pass swap WebP for AVIF without breaking
// downstream caches.
func setImageHeaders(w http.ResponseWriter, mime string, size int64, modTime time.Time) {
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Vary", "Accept")
	if size > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	}
	if !modTime.IsZero() {
		w.Header().Set("Last-Modified", modTime.UTC().Format(http.TimeFormat))
	}
}

// isImageMIME reports whether mime is one of the prefixes the
// transformer can decode. Empty mime is treated as "trust the
// transformer to figure it out" — the row may have been created by
// a path that didn't sniff the MIME at upload time.
func isImageMIME(mime string) bool {
	if mime == "" {
		return true
	}
	return strings.HasPrefix(strings.ToLower(mime), "image/")
}
