package earlyhints

import (
	"net/http"
	"strconv"
	"strings"
)

// Hint is one preload directive emitted as a single Link response
// header value on the 103 Early Hints response.
//
// The serialized form follows RFC 8288 (Web Linking):
//
//	<URL>; rel=preload; as=<As>[; crossorigin[=<CrossOrigin>]][; fetchpriority=<FetchPriority>]
//
// URL is the only required field. As, CrossOrigin, and FetchPriority
// map to the HTML preload attributes of the same name; they are
// emitted only when non-empty so we don't bloat the header with
// defaults the browser would have inferred anyway.
type Hint struct {
	// URL is the asset path or absolute URL to preload. Required.
	// Same-origin asset paths should start with "/" so the browser
	// resolves them against the document's origin (which is what
	// 103 Link headers do by default).
	URL string

	// As is the preload destination ("style", "script", "font",
	// "image", "fetch", …). Browsers REQUIRE this for any preload
	// other than a few legacy fallbacks; omit it only if you really
	// know what you're doing. Empty = no `as=` parameter emitted.
	As string

	// CrossOrigin is the CORS mode for the fetch. Allowed values:
	//   ""           → no crossorigin parameter (same-origin default).
	//   "anonymous"  → crossorigin (no value) per the HTML spec; used
	//                  for fonts and crossorigin assets without
	//                  credentials.
	//   "use-credentials" → crossorigin=use-credentials.
	//
	// For fonts, CORS is mandatory even on same-origin URLs (browser
	// quirk) — set this to "anonymous" for any preload-as-font hint.
	CrossOrigin string

	// FetchPriority maps to the fetchpriority attribute. Allowed
	// values are "high", "low", "auto", or empty. Use "high" sparingly
	// — only for the LCP image or critical above-the-fold CSS.
	FetchPriority string
}

// linkHeader serializes the hint as a single Link header VALUE (no
// header name). Multiple hints are joined with ", " when written via
// http.Header.Add — we add one entry per Hint so net/http does the
// joining (it uses the same comma+space separator).
//
// The function returns "" when the hint is unusable (empty URL).
// Callers must skip empty values; net/http accepts empty header
// values but the browser would just ignore them and we'd waste
// bytes on the wire.
func (h Hint) linkHeader() string {
	if h.URL == "" {
		return ""
	}
	// rough sizing: <url>; rel=preload; as=style; crossorigin=anonymous; fetchpriority=high
	// ~ url + 70 chars of decorators in the worst case
	var b strings.Builder
	b.Grow(len(h.URL) + 80)
	b.WriteByte('<')
	b.WriteString(h.URL)
	b.WriteString(">; rel=preload")
	if h.As != "" {
		b.WriteString("; as=")
		b.WriteString(h.As)
	}
	switch h.CrossOrigin {
	case "":
		// no-op
	case "anonymous":
		// Per HTML spec the bare token IS the anonymous mode. We
		// emit the explicit form because some intermediaries strip
		// bare attributes that have no value.
		b.WriteString("; crossorigin")
	default:
		b.WriteString("; crossorigin=")
		b.WriteString(h.CrossOrigin)
	}
	if h.FetchPriority != "" {
		b.WriteString("; fetchpriority=")
		b.WriteString(h.FetchPriority)
	}
	return b.String()
}

// HintsProvider returns the preload hints for a given request. It is
// called once per request, on the hot path, after the middleware has
// already verified the client supports 103 (HTTP/1.1+). Providers
// MUST be safe for concurrent use.
//
// Returning (nil, nil) means "no hints for this route" and is the
// expected outcome for any URL outside the provider's mapping.
// Returning (nil, err) means "tried and failed"; the middleware logs
// at WARN and falls through.
type HintsProvider interface {
	HintsFor(r *http.Request) ([]Hint, error)
}

// HintsProviderFunc is a function adapter so callers can plug in
// closures without a wrapper type.
type HintsProviderFunc func(r *http.Request) ([]Hint, error)

// HintsFor satisfies HintsProvider.
func (f HintsProviderFunc) HintsFor(r *http.Request) ([]Hint, error) {
	return f(r)
}

// StaticProvider serves a fixed path → []Hint mapping. The map is
// frozen at construction time; subsequent calls to HintsFor are
// allocation-free lookups. Suitable for the homepage / landing pages
// where the critical assets are known at boot.
type StaticProvider struct {
	byPath map[string][]Hint
}

// NewStaticProvider returns a provider that returns hints[r.URL.Path]
// for every request, copying the input map so subsequent mutations
// by the caller do not leak into the running middleware.
//
// Match is exact on r.URL.Path. For pattern-based matching, embed a
// StaticProvider inside a custom HintsProvider that classifies the
// request first.
func NewStaticProvider(hints map[string][]Hint) *StaticProvider {
	if len(hints) == 0 {
		return &StaticProvider{byPath: nil}
	}
	cp := make(map[string][]Hint, len(hints))
	for k, v := range hints {
		// copy the slice so the caller's mutation cannot affect us
		dst := make([]Hint, len(v))
		copy(dst, v)
		cp[k] = dst
	}
	return &StaticProvider{byPath: cp}
}

// HintsFor satisfies HintsProvider. The returned slice MUST NOT be
// mutated by the caller — it is shared across requests.
func (p *StaticProvider) HintsFor(r *http.Request) ([]Hint, error) {
	if p == nil || len(p.byPath) == 0 {
		return nil, nil
	}
	return p.byPath[r.URL.Path], nil
}

// validatePreloadAs returns true if the given `as` value is one of
// the destinations the HTML preload spec defines. Unknown values
// are passed through (forward-compat with future destinations);
// this helper exists for the ThemeAwareProvider's own sanity
// checks rather than as a gatekeeper.
func validatePreloadAs(as string) bool {
	switch as {
	case "", "audio", "document", "embed", "fetch", "font", "image",
		"object", "script", "style", "track", "video", "worker":
		return true
	default:
		return false
	}
}

// budgetReached returns true if adding one more hint would push the
// serialized Link header sum above an arbitrary upper bound. We do
// not enforce this on the provider side, but theme-aware code uses
// it to cap how many entries a misconfigured theme can pile on.
//
// Default budget is 8 KiB. Reverse proxies and edge runtimes
// commonly cap response-header totals around 16-32 KiB, but Link
// headers compete with cookies, CSP, security headers, and Set-Cookie
// for that budget. 8 KiB leaves comfortable room.
func budgetReached(currentBytes, addBytes, maxBytes int) bool {
	if maxBytes <= 0 {
		maxBytes = 8 * 1024
	}
	return currentBytes+addBytes > maxBytes
}

// _ unused helper to silence "declared and not used" if strconv import
// becomes unused; keeping it lets future hints surface byte counts
// in logs without re-adding the import.
var _ = strconv.Itoa
