package csp

import (
	"net/http"

	"github.com/Singleton-Solution/GoNext/packages/go/httpx"
	"github.com/Singleton-Solution/GoNext/packages/go/middleware/security"
)

// Options configures the CSP Middleware. The zero value is valid.
type Options struct {
	// ReportOnly switches the emitted header to
	// Content-Security-Policy-Report-Only. Recommended during initial
	// rollout: violations are reported via report-uri / report-to but
	// the page is NOT actually blocked. Once report counts stabilize,
	// flip to false for enforcement.
	ReportOnly bool

	// Header overrides the response-header name. Empty (default) uses
	// "Content-Security-Policy" or "Content-Security-Policy-Report-Only"
	// per ReportOnly. Override only if you need a vendor-specific
	// variant (e.g. legacy "X-Content-Security-Policy" for old IE) —
	// this is exceptional.
	Header string

	// NonceFromContext is an optional override for the per-request
	// nonce extraction. When nil, the middleware uses
	// security.NonceFromContext, which reads the nonce attached by
	// security.WithNonce.
	//
	// Override to plug in an alternate nonce store (test fixtures,
	// edge-injected nonces, …). The hook MUST be fast and allocation-free
	// on the hot path; it runs on every request.
	NonceFromContext func(*http.Request) string

	// RequireTrustedTypes, when non-empty, forces every emitted CSP to
	// carry `require-trusted-types-for 'script'` plus a `trusted-types`
	// directive listing the named policies.
	//
	// The names supplied here are MERGED with whatever the underlying
	// Policy already declares in TrustedTypes — duplicates are dropped.
	// Sinks are always set to {"script"} when the slice is non-empty;
	// callers needing a different sink set should configure the Policy
	// directly.
	//
	// This is the recommended way to require Trusted Types for
	// plugin-contributed JS in the admin: declare the host policy names
	// (e.g. "gn-plugin", "dompurify") here and the middleware will
	// guarantee they appear on every response regardless of the
	// underlying Policy shape.
	RequireTrustedTypes []string
}

// headerName returns the response-header name to set.
func (o Options) headerName() string {
	if o.Header != "" {
		return o.Header
	}
	if o.ReportOnly {
		return "Content-Security-Policy-Report-Only"
	}
	return "Content-Security-Policy"
}

// Middleware returns an httpx.Middleware that emits the configured CSP
// policy on every response. The middleware reads the per-request nonce
// from r.Context (placed there by security.WithNonce) and folds it into
// script-src / style-src via Policy.WithNonce before serializing.
//
// Middleware chain ordering (canonical):
//
//	httpx.Recovery(logger),
//	httpx.RequestID(),
//	security.WithNonce(),        // <-- BEFORE csp.Middleware
//	csp.Middleware(policy, csp.Options{}),
//	security.Headers(security.PublicSite()),
//	httpx.Logger(logger),
//
// security.WithNonce MUST run before csp.Middleware. If you swap the
// order, NonceFromContext returns "" and the emitted CSP contains no
// 'nonce-…' source — inline nonced scripts will then be blocked by the
// browser. csp.Middleware does not detect or warn about this; the
// caller is responsible for chain hygiene. See doc.go for the wiring
// example.
//
// The Policy passed at construction time is treated as immutable —
// callers must NOT mutate it after Middleware returns. WithNonce
// clones the policy on each request so the underlying value is safe to
// share across goroutines.
//
// If p is nil the middleware is a passthrough — useful for tests that
// want to disable CSP entirely without changing the chain shape.
func Middleware(p *Policy, opts Options) httpx.Middleware {
	if p == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	header := opts.headerName()
	nonceFn := opts.NonceFromContext
	if nonceFn == nil {
		nonceFn = func(r *http.Request) string {
			return security.NonceFromContext(r.Context())
		}
	}

	// Fold Options.RequireTrustedTypes into the source Policy ONCE at
	// construction time. The merged policy is the new effective baseline
	// — per-request work only adds the nonce on top.
	effective := p
	if len(opts.RequireTrustedTypes) > 0 {
		effective = p.WithTrustedTypes(TrustedTypesOptions{
			Sinks:    []string{"script"},
			Policies: opts.RequireTrustedTypes,
		})
	}

	// Pre-serialize the policy WITHOUT a nonce so the no-nonce branch is
	// allocation-free on the hot path. The per-request branch must
	// build a fresh string because the nonce is unique per request.
	baseHeaderValue := effective.String()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nonce := nonceFn(r)
			if nonce == "" {
				if baseHeaderValue != "" {
					w.Header().Set(header, baseHeaderValue)
				}
				next.ServeHTTP(w, r)
				return
			}
			// Fold the per-request nonce into script-src / style-src and
			// emit. The Clone happens inside WithNonce so the source
			// Policy remains untouched.
			value := effective.WithNonce(nonce).String()
			if value != "" {
				w.Header().Set(header, value)
			}
			next.ServeHTTP(w, r)
		})
	}
}
