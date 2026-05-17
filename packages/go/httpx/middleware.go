package httpx

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/log"
)

// HeaderRequestID is the canonical request-correlation header. Upstream
// proxies often set it; if present, we honor the incoming value (so a
// trace can be followed across services). Otherwise we generate one.
const HeaderRequestID = "X-Request-Id"

// RequestID is a middleware that ensures every request has a unique
// X-Request-Id header. If the incoming request carries the header AND
// the value is well-formed (16-64 chars, hex/alphanumeric/-_), we honor
// it. Otherwise we generate a new 16-byte hex value.
//
// The request ID is also attached to the request's context-bound logger
// via packages/go/log.WithRequest, so subsequent log.FromContext(r.Context())
// calls in the handler emit `request_id=<id>` automatically.
//
// The response always includes the X-Request-Id header so clients can
// correlate their reports.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(HeaderRequestID)
			if !validRequestID(id) {
				id = newRequestID()
			}
			w.Header().Set(HeaderRequestID, id)

			ctx := log.WithRequest(r.Context(), log.RequestFields{RequestID: id})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// validRequestID returns true if s looks like a reasonable request ID
// (length 16-64, only alphanumerics, dash, underscore). This rejects
// header smuggling attempts (newlines, control chars) and overlong
// values that would balloon logs.
func validRequestID(s string) bool {
	if len(s) < 16 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '-' || c == '_':
		default:
			return false
		}
	}
	return true
}

// newRequestID returns a fresh 32-char hex string (16 random bytes).
// On the practically-impossible failure of crypto/rand, falls back to
// a sentinel so we never serve a request without an ID.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is catastrophic; we cannot generate even
		// a session ID. The best we can do is mark the request with a
		// recognizable sentinel so operators see the alert.
		return "unknown-no-entropy"
	}
	return hex.EncodeToString(b[:])
}

// Logger is a middleware that emits one structured log line per request
// after the response is written. Fields:
//
//	method, path, status, duration_ms, bytes, user_agent, remote_addr,
//	request_id (from RequestID middleware), trace_id (if set upstream).
//
// Errors (5xx) are logged at WARN; client errors (4xx) at INFO; everything
// else at DEBUG. Health-check paths can be filtered by passing skipPaths.
//
// Place this middleware AFTER RequestID so log lines include the ID.
func Logger(baseLogger *slog.Logger, skipPaths ...string) Middleware {
	skip := make(map[string]struct{}, len(skipPaths))
	for _, p := range skipPaths {
		skip[p] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := skip[r.URL.Path]; ok {
				next.ServeHTTP(w, r)
				return
			}

			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)
			dur := time.Since(start)

			// Use baseLogger directly so the destination is predictable
			// (the logger the caller passed in). Request scope is added by
			// enriching with the request_id pulled off the response header
			// — RequestID middleware, if installed outer to Logger, has
			// already set the header before we reach this point.
			l := baseLogger
			if rid := w.Header().Get(HeaderRequestID); rid != "" {
				l = l.With(slog.String("request_id", rid))
			}

			level := slog.LevelDebug
			switch {
			case rw.status >= 500:
				level = slog.LevelWarn
			case rw.status >= 400:
				level = slog.LevelInfo
			}

			l.LogAttrs(r.Context(), level, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rw.status),
				slog.Int64("duration_ms", dur.Milliseconds()),
				slog.Int("bytes", rw.bytes),
				slog.String("remote_addr", r.RemoteAddr),
				slog.String("user_agent", r.UserAgent()),
			)
		})
	}
}

// Recovery catches panics in downstream handlers, logs a stack trace via
// the provided logger, and returns 500 Internal Server Error to the client.
// The response body never reveals the panic message (which may contain
// sensitive data); it's an opaque 500.
//
// Place this middleware FIRST in the chain so it covers everything.
func Recovery(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					// http.ErrAbortHandler is the signal for a deliberate
					// abort (e.g., flushed response, conn hijack failure).
					// Don't log it as a panic.
					if rec == http.ErrAbortHandler {
						panic(rec)
					}

					stack := debug.Stack()
					logger.Error("http handler panic",
						slog.Any("recovered", rec),
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
						slog.String("stack", string(stack)),
					)

					// If the handler hasn't written headers yet, send 500.
					// If it has, the response is already in flight and the
					// best we can do is log and drop the connection.
					if !headersWritten(w) {
						http.Error(w, "Internal Server Error", http.StatusInternalServerError)
					}
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// headersWritten is a best-effort check: if the ResponseWriter is our
// responseWriter wrapper, we can ask. Otherwise return false (assume
// nothing's been sent — slightly worse for the panic case but unlikely
// to make things worse than they already are).
func headersWritten(w http.ResponseWriter) bool {
	if rw, ok := w.(*responseWriter); ok {
		return rw.wroteHeader
	}
	return false
}

// responseWriter wraps http.ResponseWriter to capture the status code
// and bytes written. Used by Logger middleware.
type responseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if rw.wroteHeader {
		return // standard behavior: superfluous WriteHeader calls are ignored
	}
	rw.status = code
	rw.wroteHeader = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytes += n
	return n, err
}

// Unwrap exposes the underlying ResponseWriter so http.ResponseController
// (Hijack, Flush, Push, etc.) works. Required by Go 1.20+ best practice.
func (rw *responseWriter) Unwrap() http.ResponseWriter { return rw.ResponseWriter }

// Implement http.Flusher if the underlying writer supports it. Many
// patterns (SSE, streaming responses) require this.
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// String is for debug logging only; never expose response writers via fmt.
func (rw *responseWriter) String() string {
	return fmt.Sprintf("responseWriter{status=%d, bytes=%d}", rw.status, rw.bytes)
}
