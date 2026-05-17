package audit

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
)

// Emitter is the ergonomic wrapper around Store. It carries the
// per-request context (actor, IP, user-agent, plugin slug) so handlers
// don't have to re-thread those into every Emit call.
//
// Lifecycle: build one Emitter at process start that points at the
// chosen Store. Per request, call WithRequest to bind the actor and
// HTTP context — that returns a derived Emitter whose Emit calls
// auto-populate ActorUserID, IP, UserAgent. Handlers then call
// derived.Emit(ctx, "post.published", ...opts).
//
// A zero Emitter is unusable; always go through NewEmitter.
type Emitter struct {
	store Store

	// Captured per-request context. Empty on the root Emitter.
	actorUserID string
	pluginSlug  string
	ip          string
	userAgent   string
}

// NewEmitter builds the root Emitter for a process. store is required.
func NewEmitter(store Store) *Emitter {
	if store == nil {
		// We could return an error, but Emitter is constructed once at
		// boot — a nil store is a wiring bug that should crash early.
		panic("audit.NewEmitter: store is required")
	}
	return &Emitter{store: store}
}

// Store returns the underlying Store. Useful for admin endpoints that
// want to List directly without an extra dependency.
func (e *Emitter) Store() Store { return e.store }

// WithActor returns a derived Emitter that auto-populates ActorUserID.
// The receiver is not mutated.
func (e *Emitter) WithActor(userID string) *Emitter {
	cp := *e
	cp.actorUserID = userID
	return &cp
}

// WithPlugin returns a derived Emitter that auto-populates the plugin
// slug. Used by the plugin runtime when proxying audit.emit calls.
func (e *Emitter) WithPlugin(slug string) *Emitter {
	cp := *e
	cp.pluginSlug = slug
	return &cp
}

// WithHTTP returns a derived Emitter that captures the IP and User-Agent
// from r. Use this from middleware so handler-level Emit calls don't
// need to inspect the request.
func (e *Emitter) WithHTTP(r *http.Request) *Emitter {
	cp := *e
	cp.ip = clientIP(r)
	cp.userAgent = r.UserAgent()
	return &cp
}

// WithRequest is a convenience that combines WithActor and WithHTTP.
// Typical usage in a handler: `e := emitter.WithRequest(r, currentUser.ID)`.
func WithRequest(base *Emitter, r *http.Request, actorUserID string) *Emitter {
	return base.WithActor(actorUserID).WithHTTP(r)
}

// EmitOption is a functional option for Emit. Options compose; later
// options win when they overlap (e.g. two WithMetadata calls merge,
// with the later call's keys taking precedence).
type EmitOption func(*Event)

// WithTarget sets the ResourceType / ResourceID. Use the empty string
// for events without a specific target (e.g. failed login).
func WithTarget(resourceType, resourceID string) EmitOption {
	return func(e *Event) {
		e.ResourceType = resourceType
		e.ResourceID = resourceID
	}
}

// WithSeverity overrides the default SeverityInfo for this event.
func WithSeverity(s Severity) EmitOption {
	return func(e *Event) { e.Severity = s }
}

// WithMetadata merges m into the event's Metadata map. Subsequent
// WithMetadata calls merge in order, with later keys winning.
func WithMetadata(m map[string]any) EmitOption {
	return func(e *Event) {
		if e.Metadata == nil {
			e.Metadata = make(map[string]any, len(m))
		}
		for k, v := range m {
			e.Metadata[k] = v
		}
	}
}

// WithActorOverride overrides the captured actor for one Emit call.
// Rarely needed — the normal pattern is WithActor on the Emitter.
// Useful when one handler emits an event on behalf of a different
// user (e.g. admin impersonation start, where the actor is the admin
// but the event also has a target user).
func WithActorOverride(userID string) EmitOption {
	return func(e *Event) { e.ActorUserID = userID }
}

// WithIP overrides the captured IP for one Emit call. Use sparingly.
func WithIP(ip string) EmitOption {
	return func(e *Event) { e.IP = ip }
}

// Emit records an audit event. The captured Emitter fields fill in
// defaults; EmitOptions override them.
//
// Returns the Store's error wrapped with %w. Callers that treat audit
// emission as best-effort (the common case for hot-path actions)
// should log-and-continue rather than failing the user-facing request.
func (e *Emitter) Emit(ctx context.Context, eventType string, opts ...EmitOption) error {
	if e == nil || e.store == nil {
		return errors.New("audit: Emit called on nil Emitter")
	}
	if eventType == "" {
		return errors.Join(ErrInvalidEvent, errors.New("eventType is required"))
	}
	evt := Event{
		EventType:       eventType,
		ActorUserID:     e.actorUserID,
		ActorPluginSlug: e.pluginSlug,
		IP:              e.ip,
		UserAgent:       e.userAgent,
	}
	for _, opt := range opts {
		opt(&evt)
	}
	return e.store.Emit(ctx, evt)
}

// clientIP returns a best-effort client IP. It prefers the leftmost
// entry in X-Forwarded-For (set by trusted proxies — middleware that
// honors X-Forwarded-* lives in packages/go/httpx). If absent, falls
// back to splitting host:port from r.RemoteAddr. If even that fails,
// returns RemoteAddr verbatim — the audit row is best-effort.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// XFF is a comma-separated list; the first entry is the
		// original client per RFC 7239 §5.2. Trim whitespace.
		if idx := strings.IndexByte(xff, ','); idx >= 0 {
			xff = xff[:idx]
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
