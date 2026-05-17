package log

import (
	"context"
	"log/slog"
)

// ctxKey is an unexported type used as the context key. Unexported so external
// packages cannot collide with ours; the only way to put a logger in / get a
// logger out is through this package's helpers.
type ctxKey struct{}

// loggerKey is the actual key value. Using a value of type ctxKey{} (not a
// string) prevents accidental collision with other libraries.
var loggerKey = ctxKey{}

// FromContext returns the logger associated with ctx, or slog.Default() if
// none has been attached. Never returns nil.
//
// This is the canonical accessor for app code: in any function that takes a
// context, call FromContext(ctx) to get a logger with all request-scoped
// fields already attached.
func FromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// WithLogger returns a new context that carries the given logger. Subsequent
// FromContext calls on the returned context will return l.
//
// Used by middleware to attach a logger that already has request-scoped
// fields baked in (trace_id, request_id, user_id, etc.) so handler code
// doesn't have to re-pass them on every log call.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if l == nil {
		return ctx
	}
	return context.WithValue(ctx, loggerKey, l)
}

// RequestFields are the per-request attributes attached to the context-bound
// logger. All fields are optional; empty fields are omitted from log output.
//
// PluginSlug is set by the plugin host when a plugin's WASM hook is currently
// executing, so log lines from plugin code are attributed correctly.
//
// TenantID is reserved for v2 multi-tenant; today it's always empty.
type RequestFields struct {
	TraceID    string
	SpanID     string
	RequestID  string
	UserID     string
	PluginSlug string
	TenantID   string
}

// WithRequest derives a new context whose logger has the supplied fields
// pre-attached. Empty fields are dropped.
//
// Typical use is HTTP middleware:
//
//	func RequestID(next http.Handler) http.Handler {
//	    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//	        rid := uuid.NewString()
//	        ctx := log.WithRequest(r.Context(), log.RequestFields{RequestID: rid})
//	        next.ServeHTTP(w, r.WithContext(ctx))
//	    })
//	}
//
// Calls compose: calling WithRequest a second time replaces only the
// non-empty fields. To clear a field, use WithLogger directly.
func WithRequest(ctx context.Context, fields RequestFields) context.Context {
	base := FromContext(ctx)
	attrs := fields.toAttrs()
	if len(attrs) == 0 {
		return ctx
	}
	// slog.With on []any: we need the slog.Attr to any slice form.
	args := make([]any, 0, len(attrs))
	for _, a := range attrs {
		args = append(args, a)
	}
	return WithLogger(ctx, base.With(args...))
}

// toAttrs converts non-empty RequestFields to []slog.Attr in a stable order.
// Stable order makes test assertions deterministic.
func (r RequestFields) toAttrs() []slog.Attr {
	out := make([]slog.Attr, 0, 6)
	if r.TraceID != "" {
		out = append(out, slog.String("trace_id", r.TraceID))
	}
	if r.SpanID != "" {
		out = append(out, slog.String("span_id", r.SpanID))
	}
	if r.RequestID != "" {
		out = append(out, slog.String("request_id", r.RequestID))
	}
	if r.UserID != "" {
		out = append(out, slog.String("user_id", r.UserID))
	}
	if r.PluginSlug != "" {
		out = append(out, slog.String("plugin_slug", r.PluginSlug))
	}
	if r.TenantID != "" {
		out = append(out, slog.String("tenant_id", r.TenantID))
	}
	return out
}
