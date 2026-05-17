package audit

import (
	"net/http"
)

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
// Failures from Store.Emit are intentionally swallowed: if the audit
// store is down, the user-facing request still completes. Operators
// who want hard-fail behavior should wrap their Store with a decorator
// that panics — the right policy is operator-specific, not package-wide.
//
// Place this middleware AFTER httpx.RequestID and the X-Forwarded-For
// trust check so the request_id is available and r.RemoteAddr reflects
// the real client, but BEFORE any business-logic middleware that you
// want audited.
func Middleware(emitter *Emitter) func(http.Handler) http.Handler {
	if emitter == nil {
		panic("audit.Middleware: emitter is required")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isStateChanging(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			// We emit BEFORE serving so the row reflects "this request
			// arrived", even if the handler panics or never returns. The
			// emit is best-effort: an error is dropped on the floor (see
			// godoc). If you want post-response status capture, layer a
			// second middleware after this one that watches the writer.
			e := emitter.WithHTTP(r)
			_ = e.Emit(r.Context(), "http.request",
				WithMetadata(map[string]any{
					"method": r.Method,
					"path":   r.URL.Path,
				}),
			)

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
