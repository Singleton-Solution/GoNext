package earlyhints

import (
	"log/slog"
	"net/http"

	"github.com/Singleton-Solution/GoNext/packages/go/httpx"
)

// Options configures the Early Hints middleware. The zero value is
// valid and means "use defaults"; in particular Logger defaults to
// slog.Default() so production wiring without an explicit logger does
// not panic.
type Options struct {
	// Logger receives WARN entries for provider failures. When nil,
	// the middleware uses slog.Default(). The middleware never logs
	// at INFO/DEBUG on the hot path — that volume would drown real
	// signal.
	Logger *slog.Logger

	// MaxHeaderBytes caps the total serialized size of the Link
	// headers in one 103 response. Hints emitted past the budget
	// are dropped and a single WARN is logged for the request.
	// Default (0) → 8 KiB.
	MaxHeaderBytes int

	// MaxHints caps the number of Hint entries serialized in one
	// 103 response. Hints past the cap are dropped silently
	// (avoiding a per-hint log line). Default (0) → 50, which is
	// already excessive: a typical page has <10 critical assets.
	MaxHints int

	// KeepHeadersOnFinal controls whether the Link headers we
	// emitted on the 103 are also kept on the final response. Default
	// false: the Link headers are removed from w.Header() after the
	// 103 flush so the 200 isn't bloated by them. Set true if a
	// downstream cache (Varnish, Cloudflare) needs to see the Link
	// preload on the cacheable response to re-use them for warm hits.
	KeepHeadersOnFinal bool
}

// defaultMaxHints is the per-request cap when Options.MaxHints is zero.
const defaultMaxHints = 50

// defaultMaxHeaderBytes is the per-request serialized Link total when
// Options.MaxHeaderBytes is zero.
const defaultMaxHeaderBytes = 8 * 1024

// linkHeaderName is the HTTP header carrying the preload directives.
const linkHeaderName = "Link"

// Middleware returns an httpx.Middleware that asks the provider for
// hints, flushes them as a 103 Early Hints response, then runs the
// inner handler unchanged.
//
// When provider is nil the middleware is a passthrough — useful for
// tests and for the disabled-via-config code path.
//
// The middleware is safe for concurrent use; it carries no mutable
// state. All per-request allocations happen inside the closure (the
// Link header values).
func Middleware(provider HintsProvider, opts Options) httpx.Middleware {
	if provider == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	maxHints := opts.MaxHints
	if maxHints <= 0 {
		maxHints = defaultMaxHints
	}
	maxBytes := opts.MaxHeaderBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxHeaderBytes
	}
	keepFinal := opts.KeepHeadersOnFinal

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// RFC 8297 §2: interim responses (1xx) are an HTTP/1.1+
			// feature. HTTP/1.0 clients (and the rare HTTP/0.9
			// curiosity) will choke on the framing; skip the 103.
			if !r.ProtoAtLeast(1, 1) {
				next.ServeHTTP(w, r)
				return
			}

			hints, err := provider.HintsFor(r)
			if err != nil {
				// Performance optimization — never break the request.
				// Log via the middleware-level logger so tests can
				// inject a capturing handler without having to also
				// wire the request-context logger.
				logger.WarnContext(r.Context(),
					"earlyhints: provider failed; skipping 103",
					slog.String("path", r.URL.Path),
					slog.String("error", err.Error()),
				)
				next.ServeHTTP(w, r)
				return
			}

			if len(hints) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			writeEarlyHints(w, r, hints, maxHints, maxBytes, keepFinal, logger)
			next.ServeHTTP(w, r)
		})
	}
}

// writeEarlyHints flushes the 103 response with the given hints.
// Returns true when a 103 was actually sent, false when no hints were
// serializable. Callers always continue to the inner handler — the
// 103 is best-effort.
//
// The function is split out of the closure so it has a name in
// stack traces and so tests can exercise the budgeting path in
// isolation.
//
// Mechanism: per Go 1.21+ semantics, calling w.WriteHeader with
// http.StatusEarlyHints AFTER setting Link headers on w.Header()
// causes net/http to flush a 103 interim response with the current
// header set. The headers stay in w.Header() so the subsequent 200
// would echo them — we remove them after the flush unless
// KeepHeadersOnFinal is true. See:
//
//	https://pkg.go.dev/net/http#StatusEarlyHints
//	src/net/http/serve_test.go TestEarlyHints
func writeEarlyHints(
	w http.ResponseWriter,
	r *http.Request,
	hints []Hint,
	maxHints int,
	maxBytes int,
	keepFinal bool,
	logger *slog.Logger,
) bool {
	values := make([]string, 0, len(hints))
	used := 0
	dropped := 0
	for i, h := range hints {
		if i >= maxHints {
			dropped = len(hints) - maxHints
			break
		}
		v := h.linkHeader()
		if v == "" {
			continue
		}
		if budgetReached(used, len(v), maxBytes) {
			dropped += len(hints) - i
			break
		}
		values = append(values, v)
		used += len(v) + 2 // approx ", " separator on the wire
	}
	if len(values) == 0 {
		return false
	}
	if dropped > 0 {
		logger.WarnContext(r.Context(),
			"earlyhints: dropped hints over budget",
			slog.String("path", r.URL.Path),
			slog.Int("dropped", dropped),
			slog.Int("max_hints", maxHints),
			slog.Int("max_bytes", maxBytes),
		)
	}

	// Snapshot any pre-existing Link headers so we can restore them
	// after the 103 flush. We DO want any Link headers set by an
	// outer middleware (e.g. CSP report-to as a Link header is not
	// a thing, but Reporting API endpoints sometimes use Link) to
	// survive on the final response.
	hdr := w.Header()
	prev := append([]string(nil), hdr.Values(linkHeaderName)...)

	// Replace the Link header set with our hints, write the interim
	// 103, then restore.
	hdr.Del(linkHeaderName)
	for _, v := range values {
		hdr.Add(linkHeaderName, v)
	}

	w.WriteHeader(http.StatusEarlyHints)

	// After WriteHeader(StatusEarlyHints), the same header map is
	// what net/http will use for the final response. If we leave the
	// preload Links in place, they'll be repeated on the 200 — which
	// is what the stdlib TestEarlyHints actually shows. That is
	// usually wasted bytes on a non-cacheable HTML response, so we
	// strip them by default. Operators who deliberately want the
	// final response to carry the same Links (e.g. for an upstream
	// cache to re-replay them on subsequent hits) opt in with
	// KeepHeadersOnFinal.
	if !keepFinal {
		hdr.Del(linkHeaderName)
		for _, v := range prev {
			hdr.Add(linkHeaderName, v)
		}
	}
	return true
}
