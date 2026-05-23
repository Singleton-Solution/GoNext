package redirects

import (
	"net/http"
	"strconv"
)

// MaxHops is the upper bound on in-process redirect chain length. Once
// a request has traversed this many redirect rules in the same
// middleware pass, we respond with 508 Loop Detected rather than
// continuing — a chain that long is almost certainly a misconfigured
// rule pointing back to itself transitively.
//
// WordPress folklore quotes "five hops" as the canonical safe ceiling
// because that's what most browsers tolerate before they give up on
// the response. We match it.
const MaxHops = 5

// HeaderHopCount is the in-process header the middleware uses to
// thread the hop counter across re-entry. It is stripped from the
// response so external callers never see it.
//
// This is NOT a public protocol — it only matters when a redirect's
// destination is a path that the same server serves and the
// middleware re-enters via a follow-up internal request. In the
// common case (the middleware writes a 3xx to the wire and the
// browser does the follow-up) this header never appears.
const HeaderHopCount = "X-GoNext-Redirect-Hops"

// LoopDetectedBody is the response body served on 508. We make it a
// fixed string (rather than templating an error code) so operators
// can grep it out of nginx logs without ambiguity. Cache-Control:
// no-store keeps a misbehaving CDN from pinning the failure mode.
const LoopDetectedBody = "Redirect loop detected.\n"

// matcher is the subset of *Engine that the middleware uses. Exposed
// so tests can inject a stub matcher without spinning up a real
// engine + store pair.
type matcher interface {
	Match(path string) (Match, bool)
}

// Middleware returns an http.Handler middleware that consults the
// engine before passing the request downstream. On hit, the response
// is the 3xx + Location; the downstream handler is NOT invoked.
//
// Loop protection: each redirect increments the hop counter (sent on
// the in-process X-GoNext-Redirect-Hops header). If the counter
// reaches MaxHops, the middleware responds with 508 Loop Detected
// instead of writing yet another Location. The header is stripped
// from outgoing responses so clients never see it.
func Middleware(engine matcher) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Strip the in-process hop header from the outgoing
			// response no matter what we do below. The header is an
			// implementation detail; clients never see it.
			defer w.Header().Del(HeaderHopCount)

			hops := 0
			if v := r.Header.Get(HeaderHopCount); v != "" {
				if n, err := strconv.Atoi(v); err == nil && n > 0 {
					hops = n
				}
			}

			match, ok := engine.Match(r.URL.Path)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			// We have a match. Bump the hop counter; if it crosses
			// MaxHops, refuse with 508 instead of writing yet another
			// Location header.
			hops++
			if hops > MaxHops {
				writeLoopDetected(w)
				return
			}

			// Forward the hop count on the response too — when a
			// follow-up internal request is made (e.g. the renderer
			// re-enters this middleware), the request that produces
			// it can copy the header forward. External browsers don't
			// see this header because of the deferred Del above.
			w.Header().Set(HeaderHopCount, strconv.Itoa(hops))
			w.Header().Set("Location", match.Destination)

			// Cache-Control: permanent redirects (301/308) are
			// cacheable by default. We want the browser/CDN to cache
			// them — the table is operator-curated and stable. Note
			// we don't set cache headers for 302/307 (temporary).
			if match.Status == http.StatusMovedPermanently ||
				match.Status == http.StatusPermanentRedirect {
				if w.Header().Get("Cache-Control") == "" {
					w.Header().Set("Cache-Control", "public, max-age=3600")
				}
			}
			w.WriteHeader(match.Status)
			// No body needed for redirects — modern clients ignore it
			// and the Content-Length: 0 keeps proxies happy.
		})
	}
}

// writeLoopDetected serves the 508 response. Centralized so tests can
// assert the exact body string + headers.
func writeLoopDetected(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(LoopDetectedBody)))
	w.WriteHeader(http.StatusLoopDetected)
	_, _ = w.Write([]byte(LoopDetectedBody))
}
