package policy

import "context"

// principalCtxKey is the unexported key under which a Principal is
// stored on a context. An unexported type prevents collision with any
// other package that uses context.WithValue.
type principalCtxKey struct{}

// WithPrincipal returns a child context carrying the given Principal.
// The upstream auth middleware calls this once per request after it has
// loaded the user and their roles; downstream middleware and handlers
// (notably Require) read it back with FromContext.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// FromContext returns the Principal stashed on ctx by WithPrincipal.
// The ok return is false when no Principal is on the context — callers
// (typically the Require middleware) treat that as unauthenticated.
//
// FromContext does not allocate when no Principal is present; the zero
// Principal is returned with ok=false.
func FromContext(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	p, ok := ctx.Value(principalCtxKey{}).(Principal)
	return p, ok
}
