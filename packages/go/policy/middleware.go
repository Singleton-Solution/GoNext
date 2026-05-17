package policy

import "net/http"

// Require returns middleware that enforces a primitive capability before
// the wrapped handler runs. The shape matches packages/go/httpx.Middleware
// — func(http.Handler) http.Handler — so it composes with the rest of
// the chassis.
//
// The middleware:
//
//  1. Reads the Principal off the request context (set by upstream auth
//     middleware via WithPrincipal). If absent, returns 401 Unauthorized.
//
//  2. Calls p.Can with the requested capability and nil resource. If the
//     decision denies, returns 403 Forbidden with the decision's Reason
//     as the response body (the reason is safe to surface — see Decision
//     docs).
//
//  3. Otherwise hands off to next.
//
// Require is the route-level gate. Object-level (meta-capability) checks
// still happen inside the handler after the target resource is loaded:
//
//	mux.Handle("POST /api/posts",
//	    policy.Require(pol, policy.CapPublishPosts)(publishHandler))
//
//	// inside publishHandler, after loading the post:
//	pr, _ := policy.FromContext(r.Context())
//	d := pol.Can(pr, policy.CapEditOthersPosts, post)
//	if !d.Allowed {
//	    http.Error(w, d.Reason, http.StatusForbidden)
//	    return
//	}
//
// The caller is responsible for having put the Principal on the context
// (the auth middleware does this); Require does not load users itself.
func Require(p Policy, capability Capability) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pr, ok := FromContext(r.Context())
			if !ok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			d := p.Can(pr, capability, nil)
			if !d.Allowed {
				http.Error(w, d.Reason, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
