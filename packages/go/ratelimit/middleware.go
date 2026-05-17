package ratelimit

import (
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	gonext_log "github.com/Singleton-Solution/GoNext/packages/go/log"
)

// HeaderRetryAfter is the standard HTTP header used to communicate the
// rate-limit cooldown to clients.
const HeaderRetryAfter = "Retry-After"

// Middleware returns an HTTP middleware that consults l for every
// request, keyed by keyFn. When l denies the request, the middleware
// responds with 429 Too Many Requests and a Retry-After header
// rounded UP to the nearest whole second (RFC 7231 §7.1.3 mandates an
// integer seconds value).
//
// If keyFn returns an empty string the request is allowed through
// without being counted — this is the escape hatch for callers that
// want to skip throttling for certain requests (admin-token health
// checks, internal RPC). To always count, ensure keyFn returns a
// non-empty value such as "anon".
//
// If l returns an error (Redis unreachable, etc.) the middleware fails
// OPEN: the request proceeds, and a WARN line is emitted to the
// request-scoped logger. The reasoning: rate-limit availability should
// not become a single point of failure for the auth path; outages
// route around it. Operators who prefer fail-closed can implement a
// thin wrapper that turns the error into 503.
func Middleware(l Limiter, keyFn func(*http.Request) string) func(http.Handler) http.Handler {
	if l == nil {
		panic("ratelimit.Middleware: limiter is nil")
	}
	if keyFn == nil {
		panic("ratelimit.Middleware: keyFn is nil")
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFn(r)
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}

			allowed, retryAfter, err := l.Allow(r.Context(), key)
			if err != nil {
				// Fail open. Surface the error to the request logger
				// so an operator notices the dropped-throttle window.
				gonext_log.FromContext(r.Context()).Warn(
					"rate limiter backend error; failing open",
					slog.String("err", err.Error()),
					slog.String("key", key),
				)
				next.ServeHTTP(w, r)
				return
			}
			if allowed {
				next.ServeHTTP(w, r)
				return
			}

			writeRateLimited(w, retryAfter)
		})
	}
}

// writeRateLimited writes a 429 response with a Retry-After header.
// retryAfter is rounded UP to the next whole second; a sub-second
// retryAfter would otherwise round to zero and tell the client to
// retry instantly, defeating the limiter.
func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set(HeaderRetryAfter, strconv.Itoa(seconds))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte("Too Many Requests\n"))
}

// KeyByIP returns the client IP as the bucket key. It honors
// X-Forwarded-For when present (taking the leftmost entry — the
// original client per RFC 7239 conventions) and falls back to
// r.RemoteAddr. The port is stripped so requests from the same client
// on different ephemeral ports share a bucket.
//
// NOTE: trusting X-Forwarded-For without a TrustedProxies allowlist
// lets a malicious client spoof their bucket. In production, the
// httpx server should be fronted by an X-Forwarded-For sanitization
// middleware that only accepts the header from trusted upstreams.
// This helper assumes that work has been done; if your edge does not
// sanitize, swap in KeyByRemoteAddr below.
func KeyByIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if comma := strings.IndexByte(xff, ','); comma >= 0 {
			xff = xff[:comma]
		}
		ip := strings.TrimSpace(xff)
		if ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// KeyByRemoteAddr returns the bucket key from r.RemoteAddr only,
// ignoring X-Forwarded-For. Safe to use unconditionally, but doesn't
// see the real client behind a reverse proxy.
func KeyByRemoteAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
