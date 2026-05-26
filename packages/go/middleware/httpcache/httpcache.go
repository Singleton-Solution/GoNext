package httpcache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/Singleton-Solution/GoNext/packages/go/httpx"
)

// Options configures the middleware. The zero value is valid — it emits
// ETag on safe responses and short-circuits If-None-Match matches to 304,
// without setting Vary or Cache-Control.
type Options struct {
	// Vary is the list of request headers the response body depends on.
	// When non-empty the middleware emits `Vary: <h1>, <h2>, ...`. A
	// caller that personalizes by language ought to pass {"Accept-Language"};
	// CORS-aware callers pass {"Origin"}. Empty disables Vary entirely
	// — callers whose body is pure-function-of-URL get the smallest
	// possible header set.
	Vary []string

	// CacheControl, when non-empty, is set on safe responses as the
	// Cache-Control header. Common values:
	//
	//   "public, max-age=60"          public reads, CDN-friendly
	//   "private, max-age=0, must-revalidate"  per-user reads
	//   "no-cache"                    always revalidate (still uses ETag)
	//
	// Empty leaves the header alone — the underlying handler may set it
	// itself, and we never overwrite an existing value (the buffering
	// layer's WriteHeader passes through any header the handler set
	// upstream).
	CacheControl string

	// MaxBodyBytes bounds the in-memory buffer the middleware allocates
	// per safe request. A response that would exceed this is flushed
	// directly to the wire without ETag computation — protection against
	// a misconfigured caller wrapping a streaming endpoint.
	//
	// Zero defaults to DefaultMaxBodyBytes (1 MiB). Set to -1 to disable
	// the bound (NOT recommended — see package doc on streaming).
	MaxBodyBytes int
}

// DefaultMaxBodyBytes is the per-request buffer cap when Options.MaxBodyBytes
// is zero. 1 MiB matches the maxBodyBytes constant used by the REST
// write path; it's enough for any reasonable JSON feed and small enough
// that an accidental wrap of a streaming endpoint fails fast.
const DefaultMaxBodyBytes = 1 << 20

// Middleware returns an httpx.Middleware that wraps safe-method
// responses with the cache-header machinery described in the package doc.
//
// For unsafe methods, the returned middleware is a pure pass-through —
// no allocation, no Header writes, no ResponseWriter wrapping.
//
// Multiple instances may be composed: an outer wrap that sets
// `Vary: Accept-Encoding` (gzip), an inner that sets `Vary:
// Accept-Language`. The middleware merges its Vary list into any
// existing Vary header rather than overwriting, so the order doesn't
// matter for correctness.
func Middleware(opts Options) httpx.Middleware {
	maxBytes := opts.MaxBodyBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxBodyBytes
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			bw := newBufferingWriter(w, maxBytes)
			next.ServeHTTP(bw, r)

			// The handler may have explicitly opted-out by setting
			// Cache-Control: no-store or private. Honor that — never
			// emit an ETag for a response the upstream said is not
			// cacheable. (See package doc, "private" bucket.)
			if hasNoStore(bw.Header().Get("Cache-Control")) {
				bw.flush(w)
				return
			}

			if bw.overflowed {
				// Body grew past MaxBodyBytes mid-write; the response
				// has already been partially sent to the wire by the
				// passthrough Write call. Just finish flushing.
				bw.flush(w)
				return
			}

			// Compute ETag over the buffered body. We use SHA-256
			// truncated to 16 bytes (32 hex chars) — collision
			// probability for any realistic deployment is vanishing,
			// and a shorter header keeps response size down.
			body := bw.buf.Bytes()
			sum := sha256.Sum256(body)
			etag := `"` + hex.EncodeToString(sum[:16]) + `"`
			bw.Header().Set("ETag", etag)

			if len(opts.Vary) > 0 {
				mergeVary(bw.Header(), opts.Vary)
			}
			if opts.CacheControl != "" && bw.Header().Get("Cache-Control") == "" {
				bw.Header().Set("Cache-Control", opts.CacheControl)
			}

			// Short-circuit on If-None-Match.
			if etagMatches(r.Header.Get("If-None-Match"), etag) {
				// 304 MUST NOT include a body. Strip Content-Length /
				// Content-Type so downstream proxies don't gag on the
				// mismatch.
				bw.Header().Del("Content-Length")
				bw.Header().Del("Content-Type")
				// Copy our buffered headers (including the freshly-set
				// ETag) onto the underlying writer before sending the
				// 304 — the client needs the ETag to re-cache.
				bw.copyHeadersOut()
				w.WriteHeader(http.StatusNotModified)
				return
			}

			bw.flush(w)
		})
	}
}

// isSafeMethod reports whether method is one of the HTTP methods that
// the spec treats as cacheable + idempotent. GET and HEAD are the
// canonical pair; OPTIONS is technically safe but typically handled by
// a CORS middleware that already sets its own headers, so we leave it
// alone here.
func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// hasNoStore reports whether the Cache-Control header indicates the
// response is not cacheable. We treat both "no-store" and "private" as
// opt-outs: the former is the standards-correct way to say "do not
// cache anywhere"; the latter says "do not cache in shared caches",
// which is enough to make ETag plumbing meaningless for our use case
// (private responses shouldn't be cacheable at the CDN).
func hasNoStore(cc string) bool {
	if cc == "" {
		return false
	}
	lower := strings.ToLower(cc)
	return strings.Contains(lower, "no-store") || strings.Contains(lower, "private")
}

// mergeVary appends each entry of want into h's Vary header, skipping
// values already present. The merge avoids the common pitfall of two
// middlewares overwriting each other's Vary contributions.
func mergeVary(h http.Header, want []string) {
	existing := h.Get("Vary")
	have := make(map[string]struct{})
	if existing != "" {
		for _, part := range strings.Split(existing, ",") {
			have[strings.ToLower(strings.TrimSpace(part))] = struct{}{}
		}
	}
	out := existing
	for _, v := range want {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := have[strings.ToLower(v)]; ok {
			continue
		}
		if out == "" {
			out = v
		} else {
			out = out + ", " + v
		}
		have[strings.ToLower(v)] = struct{}{}
	}
	if out != "" {
		h.Set("Vary", out)
	}
}

// etagMatches reports whether any comma-separated value in
// ifNoneMatch equals etag. We treat `*` as "matches anything" per
// RFC 7232 §3.2. Both weak (`W/"..."`) and strong forms are accepted
// as a match against a strong tag — the standard says weak comparison
// is the right operator for If-None-Match.
func etagMatches(ifNoneMatch, etag string) bool {
	if ifNoneMatch == "" || etag == "" {
		return false
	}
	for _, raw := range strings.Split(ifNoneMatch, ",") {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if v == "*" {
			return true
		}
		// Strip optional weak prefix before comparing — RFC 7232 §2.3.2
		// "weak comparison" treats W/"x" and "x" as equal.
		v = strings.TrimPrefix(v, "W/")
		if v == etag {
			return true
		}
	}
	return false
}

// bufferingWriter is the http.ResponseWriter wrapper used on safe
// responses. It captures status, headers, and body until the handler
// returns, at which point the middleware decides whether to flush the
// buffered response or rewrite it as a 304.
//
// On overflow (body grows past maxBytes), we transition into
// passthrough mode: the buffered prefix is flushed to the underlying
// writer and all subsequent writes go straight to the wire. This is
// the safety net for callers that accidentally wrap a streaming
// endpoint — the response still completes correctly, it just doesn't
// get an ETag.
type bufferingWriter struct {
	rw         http.ResponseWriter
	header     http.Header
	buf        bytes.Buffer
	status     int
	written    bool
	overflowed bool
	maxBytes   int
}

func newBufferingWriter(rw http.ResponseWriter, maxBytes int) *bufferingWriter {
	bw := &bufferingWriter{
		rw:       rw,
		header:   make(http.Header),
		status:   http.StatusOK,
		maxBytes: maxBytes,
	}
	// Pre-copy any headers the chain set upstream (e.g. CSP / CORS
	// middleware that ran before us). They become the starting point
	// for our header map; the handler may add to or overwrite them.
	for k, v := range rw.Header() {
		bw.header[k] = v
	}
	return bw
}

// Header returns the buffered header map. Writes against this are
// captured until flush; the underlying ResponseWriter's headers are
// only mutated when we flush.
func (b *bufferingWriter) Header() http.Header { return b.header }

// WriteHeader records the status code. Multiple calls are tolerated —
// the last one wins, matching net/http's tolerant-but-warns semantics.
// The actual underlying WriteHeader is deferred to flush so we can
// rewrite to 304.
func (b *bufferingWriter) WriteHeader(status int) {
	b.status = status
	b.written = true
}

// Write buffers data until maxBytes; past that point, it falls back to
// streaming the rest straight to the wire (and the middleware skips
// ETag generation). Returns the same (n, err) semantics as the
// underlying writer for the passthrough case.
func (b *bufferingWriter) Write(p []byte) (int, error) {
	if b.overflowed {
		return b.rw.Write(p)
	}
	if b.maxBytes > 0 && b.buf.Len()+len(p) > b.maxBytes {
		// Switch to passthrough: flush what we have so far, plus this
		// chunk, directly. The middleware will see overflowed==true on
		// return and skip ETag.
		b.copyHeadersOut()
		b.rw.WriteHeader(b.status)
		if b.buf.Len() > 0 {
			_, _ = b.rw.Write(b.buf.Bytes())
			b.buf.Reset()
		}
		b.overflowed = true
		return b.rw.Write(p)
	}
	return b.buf.Write(p)
}

// flush writes the buffered response to the underlying ResponseWriter.
// Called by the middleware after the handler returns (and after any
// ETag manipulation has run).
func (b *bufferingWriter) flush(w http.ResponseWriter) {
	if b.overflowed {
		// Already flushed; nothing to do.
		return
	}
	b.copyHeadersOut()
	if b.written {
		w.WriteHeader(b.status)
	}
	if b.buf.Len() > 0 {
		_, _ = w.Write(b.buf.Bytes())
	}
}

// copyHeadersOut applies the buffered header map to the underlying
// ResponseWriter. Idempotent — calling it twice is fine; the second
// call is a no-op because we only mutate keys we own.
func (b *bufferingWriter) copyHeadersOut() {
	out := b.rw.Header()
	for k, v := range b.header {
		out[k] = v
	}
}
