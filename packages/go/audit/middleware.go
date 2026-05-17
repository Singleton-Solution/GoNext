package audit

import (
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/Singleton-Solution/GoNext/packages/go/log"
)

// HeaderRequestID is the canonical request-correlation header. Mirrors
// httpx.HeaderRequestID; redeclared here so the audit package does not
// take a dependency on httpx (which depends on log already).
const HeaderRequestID = "X-Request-Id"

// EmitFailureRecorder is the hook the middleware uses to surface a
// Store.Emit failure to whatever metrics pipeline the operator is
// running. The audit package does not depend on a specific metrics
// library; operators wire packages/go/metrics (or their own client)
// into this hook at boot.
//
// Implementations MUST be safe for concurrent use from many goroutines
// and MUST return quickly — the recorder runs on the request hot path.
//
// labels carries the event_type (and, in the future, possibly more
// dimensions). Recorders should treat unknown keys as opaque.
type EmitFailureRecorder interface {
	IncEmitFailure(labels map[string]string)
}

// EmitFailureFunc adapts a plain function to EmitFailureRecorder.
type EmitFailureFunc func(labels map[string]string)

// IncEmitFailure implements EmitFailureRecorder.
func (f EmitFailureFunc) IncEmitFailure(labels map[string]string) { f(labels) }

// defaultEmitFailureCounter is a process-global counter used when no
// EmitFailureRecorder is wired in. It exists so operators who haven't
// hooked up a metrics pipeline yet can still introspect failures via
// EmitFailureCount (used in tests and admin debug endpoints).
type defaultEmitFailureCounter struct {
	mu    sync.RWMutex
	byKey map[string]*atomic.Int64
	total atomic.Int64
}

func (c *defaultEmitFailureCounter) IncEmitFailure(labels map[string]string) {
	c.total.Add(1)
	key := labels["event_type"]
	if key == "" {
		key = "_unset"
	}
	c.mu.RLock()
	counter, ok := c.byKey[key]
	c.mu.RUnlock()
	if !ok {
		c.mu.Lock()
		if c.byKey == nil {
			c.byKey = make(map[string]*atomic.Int64)
		}
		counter, ok = c.byKey[key]
		if !ok {
			counter = &atomic.Int64{}
			c.byKey[key] = counter
		}
		c.mu.Unlock()
	}
	counter.Add(1)
}

func (c *defaultEmitFailureCounter) get(eventType string) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if counter, ok := c.byKey[eventType]; ok {
		return counter.Load()
	}
	return 0
}

func (c *defaultEmitFailureCounter) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byKey = nil
	c.total.Store(0)
}

// defaultFailureCounter is the package-wide fallback counter that the
// middleware increments when no EmitFailureRecorder was configured.
// Exposed via DefaultEmitFailureCount for observability.
var defaultFailureCounter = &defaultEmitFailureCounter{}

// DefaultEmitFailureCount returns the number of middleware emit failures
// observed for eventType using the package-default counter. Pass the
// empty string for the grand total across all event types.
//
// This counter is incremented only when no explicit
// EmitFailureRecorder is wired in via MiddlewareOption.
func DefaultEmitFailureCount(eventType string) int64 {
	if eventType == "" {
		return defaultFailureCounter.total.Load()
	}
	return defaultFailureCounter.get(eventType)
}

// ResetDefaultEmitFailureCount zeroes the package-default counter.
// Intended for tests; production code should not need it.
func ResetDefaultEmitFailureCount() { defaultFailureCounter.reset() }

// MiddlewareOption configures the audit middleware at construction.
type MiddlewareOption func(*middlewareConfig)

type middlewareConfig struct {
	recorder EmitFailureRecorder
}

// WithEmitFailureRecorder installs the hook called whenever the
// middleware's Store.Emit returns a non-nil error. Operators wire this
// to their metrics client (Prometheus counter, OTel meter, etc.) so an
// audit-store outage is visible.
func WithEmitFailureRecorder(r EmitFailureRecorder) MiddlewareOption {
	return func(c *middlewareConfig) { c.recorder = r }
}

// Middleware emits an http.request audit event for state-changing HTTP
// methods (POST, PUT, PATCH, DELETE). Safe methods (GET, HEAD, OPTIONS,
// TRACE) are passed through without audit overhead — they shouldn't be
// mutating state, and auditing every read would drown the table.
//
// The emitter parameter is the root Emitter; this middleware does NOT
// know the authenticated user — actor-aware emission belongs in the
// auth middleware, which runs after this one and can either re-emit
// or set the actor on the context-bound emitter. The audit row from
// this middleware captures method, path, IP, and User-Agent so even
// pre-auth state-changing requests (POST /login) leave a trace.
//
// If the request carries an X-Request-Id header (set by httpx.RequestID),
// the value is included in the emitted event's Metadata under the
// "request_id" key, so audit rows correlate with HTTP request logs.
//
// Failures from Store.Emit do NOT abort the request — auditing is
// best-effort from the handler's perspective, so an audit-store outage
// does not take down the user-facing path. But failures are no longer
// silent: each one is logged at WARN via the request-context logger and
// recorded against the configured EmitFailureRecorder (or the package
// default counter, see DefaultEmitFailureCount).
//
// Place this middleware AFTER httpx.RequestID and the X-Forwarded-For
// trust check so the request_id is available and r.RemoteAddr reflects
// the real client, but BEFORE any business-logic middleware that you
// want audited.
func Middleware(emitter *Emitter, opts ...MiddlewareOption) func(http.Handler) http.Handler {
	if emitter == nil {
		panic("audit.Middleware: emitter is required")
	}
	cfg := middlewareConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	recorder := cfg.recorder
	if recorder == nil {
		recorder = defaultFailureCounter
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isStateChanging(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			// We emit BEFORE serving so the row reflects "this request
			// arrived", even if the handler panics or never returns.
			// If you want post-response status capture, layer a second
			// middleware after this one that watches the writer.
			e := emitter.WithHTTP(r)
			meta := map[string]any{
				"method": r.Method,
				"path":   r.URL.Path,
			}
			// Correlate with HTTP request logs when httpx.RequestID has
			// run upstream. The header is the canonical carrier; it's
			// always set on the request by that middleware, including
			// for client-supplied IDs that pass validation.
			if rid := r.Header.Get(HeaderRequestID); rid != "" {
				meta["request_id"] = rid
			}

			if err := e.Emit(r.Context(), "http.request", WithMetadata(meta)); err != nil {
				// Auditing is best-effort from the request handler's
				// perspective: the user-facing request still completes.
				// But the failure is no longer silent — we log it and
				// bump a counter so an audit-store outage is visible to
				// operators.
				log.FromContext(r.Context()).LogAttrs(r.Context(), slog.LevelWarn,
					"audit: emit failed",
					slog.String("event_type", "http.request"),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Any("error", err),
				)
				recorder.IncEmitFailure(map[string]string{
					"event_type": "http.request",
				})
			}

			next.ServeHTTP(w, r)
		})
	}
}

// isStateChanging reports whether method is one the audit middleware
// should emit for. The list is the standard HTTP "unsafe" methods.
//
// We intentionally don't include CONNECT (proxy-only) or TRACE (debug,
// often disabled). If a custom method is in use, callers can wrap this
// middleware or emit manually.
func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
