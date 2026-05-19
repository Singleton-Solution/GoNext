package pat

import (
	"context"
	"net/http"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// capsCtxKey is the unexported key the PAT middleware uses to stash the
// intersected capability set on the request context. Separate from the
// policy package's principalCtxKey because the intersection is a PAT-
// specific concept — a session-authenticated request has no caps key
// and falls through to the role-derived path inside BasicPolicy.
type capsCtxKey struct{}

// tokenIDCtxKey carries the PAT row id for audit logging downstream.
// Handlers can read it with TokenIDFromContext for "this action was
// performed via PAT <id>" log lines.
type tokenIDCtxKey struct{}

// WithCapabilities returns a child context carrying the intersected
// CapabilitySet. The middleware calls this after successful PAT
// resolution; downstream policy checks call CapsFromContext to read
// it back.
func WithCapabilities(ctx context.Context, caps policy.CapabilitySet) context.Context {
	return context.WithValue(ctx, capsCtxKey{}, caps)
}

// CapsFromContext returns the intersected CapabilitySet stashed on ctx
// by the PAT middleware. The ok return is false when no PAT
// intersection happened (e.g. cookie-session request, anonymous
// request), in which case the caller falls back to its usual
// role-derived path.
//
// The returned set is the live map, not a copy. Callers must not
// mutate it; if they need to extend or filter, copy first.
func CapsFromContext(ctx context.Context) (policy.CapabilitySet, bool) {
	if ctx == nil {
		return nil, false
	}
	c, ok := ctx.Value(capsCtxKey{}).(policy.CapabilitySet)
	return c, ok
}

// WithTokenID returns a child context carrying the PAT row id.
func WithTokenID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, tokenIDCtxKey{}, id)
}

// TokenIDFromContext returns the PAT row id stashed on ctx by the PAT
// middleware. Useful for audit log lines that want to attribute an
// action to a specific token (not just a user).
func TokenIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	s, ok := ctx.Value(tokenIDCtxKey{}).(string)
	return s, ok
}

// Require returns a middleware that enforces a capability check using
// the PAT-intersected caps when present, falling back to the normal
// policy.Require path otherwise. The two-tier check means:
//
//   - PAT-authenticated request: only the intersected caps count. A
//     PAT scoped to {posts.read} cannot satisfy {posts.write} even if
//     the user's roles would normally allow it.
//   - Session-authenticated request: the policy.Policy answers as
//     usual, using the Principal's Roles.
//
// Mounting Require on a route is equivalent to mounting policy.Require
// for the session case; the PAT case is the only behavioral
// difference. Routes that don't care about PAT auth (admin UI HTML
// endpoints) can keep using policy.Require directly.
func Require(p policy.Policy, capability policy.Capability) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			if caps, ok := CapsFromContext(ctx); ok {
				if caps.Has(capability) {
					next.ServeHTTP(w, r)
					return
				}
				http.Error(w, "token scope does not grant "+string(capability), http.StatusForbidden)
				return
			}
			// Fall through to the standard role-derived path.
			policy.Require(p, capability)(next).ServeHTTP(w, r)
		})
	}
}
