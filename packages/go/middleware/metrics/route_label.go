package metrics

import (
	"net/http"
)

// unknownRoute is the sentinel label used when no route template is
// available. Using a sentinel — rather than r.URL.Path — is the
// cardinality guard: a 1000-RPS scan over /aaaa, /bbbb, … all collapse
// to one "unknown" series instead of producing 1000 new ones.
//
// Exported so tests and operators can reference it by name when
// inspecting metrics output (e.g. when investigating why a route shows
// up unlabeled).
const unknownRoute = "unknown"

// RouteLabel extracts the route template that matched the request.
//
// Resolution order:
//
//  1. http.Request.Pattern (Go 1.22+ std-mux). When the request has
//     been routed by net/http.ServeMux's method-aware patterns, the
//     Pattern field carries the matched template (e.g. "GET /users/{id}").
//     We strip the leading method+space if present so the label is just
//     the path template.
//
//  2. Fallback to unknownRoute. This covers:
//     - The request never matched a route (404 from std-mux leaves
//     Pattern empty).
//     - The middleware ran before routing (which shouldn't happen with
//     the chain wiring documented in doc.go, but we don't trust it).
//     - The handler uses a router (chi, gorilla/mux) that doesn't set
//     Pattern. Adopters who want route templates from those frameworks
//     wire their own label-extraction; the cardinality guard means
//     they at worst lose label fidelity, not blow up the registry.
//
// Note that we deliberately do NOT fall back to r.URL.Path. Doing so
// would multiply cardinality with every unique URL — exactly the
// failure mode the cardinality guard is here to prevent.
func RouteLabel(r *http.Request) string {
	if r == nil {
		return unknownRoute
	}
	pat := r.Pattern
	if pat == "" {
		return unknownRoute
	}
	// std-mux stores patterns as "METHOD /path" or just "/path". Strip
	// the method prefix so the label is consistent regardless of whether
	// the caller registered methodful or methodless patterns.
	if i := indexSpace(pat); i >= 0 {
		// Verify the prefix really is an HTTP method (uppercase letters
		// before the first space) before stripping. Otherwise a pattern
		// like "/foo bar" (illegal but conceivable in a custom router
		// that abuses Pattern) would silently lose its first segment.
		if isUpperAlpha(pat[:i]) {
			return pat[i+1:]
		}
	}
	return pat
}

// indexSpace is strings.IndexByte(s, ' ') inlined to avoid the import
// — keeps the package's import graph minimal.
func indexSpace(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			return i
		}
	}
	return -1
}

// isUpperAlpha reports whether s is non-empty and contains only
// ASCII uppercase letters. Used to validate the method prefix before
// stripping it; HTTP methods are uppercase ASCII per RFC 9110.
func isUpperAlpha(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}
