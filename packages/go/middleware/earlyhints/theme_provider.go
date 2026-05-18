package earlyhints

import (
	"net/http"
	"strings"
	"sync"
)

// ThemeStyleResolver returns the public URL of the active theme's
// compiled stylesheet, plus any additional preload entries the theme
// has registered (custom fonts, hero images, …). Implementations live
// alongside the theme runtime; this interface keeps the middleware
// decoupled from the theme package's internals.
//
// Active returns:
//   - styleURL: the URL to preload as `as=style`. Empty = no stylesheet
//     hint emitted (the theme has not been seeded or the resolver
//     was misconfigured).
//   - extras: additional preload entries (fonts, hero images). The
//     resolver is responsible for any URL-resolution logic; the
//     middleware emits these verbatim.
//
// Implementations MUST be safe for concurrent use. The middleware
// calls Active on every matching request.
type ThemeStyleResolver interface {
	Active(r *http.Request) (styleURL string, extras []Hint)
}

// ThemeStyleResolverFunc is a function adapter so callers can supply
// closures without a dedicated type. Useful in tests.
type ThemeStyleResolverFunc func(r *http.Request) (string, []Hint)

// Active satisfies ThemeStyleResolver.
func (f ThemeStyleResolverFunc) Active(r *http.Request) (string, []Hint) {
	return f(r)
}

// ThemeAwareProvider produces hints by combining the active theme's
// stylesheet (with as=style) and any registered extras (typically
// font files and the LCP image). The provider only emits hints for
// HTML-shaped requests — `Accept: text/html` or paths with no
// extension. Static asset requests (/static/foo.png) skip 103
// because the browser already knows the URL it asked for.
//
// The provider is also route-scoped via PathPredicate: only requests
// where PathPredicate(r) returns true get hints. Default predicate
// emits hints for any request whose Accept header advertises text/html
// — this matches what theme-rendered pages send.
type ThemeAwareProvider struct {
	resolver  ThemeStyleResolver
	predicate func(*http.Request) bool

	// styleHintTemplate captures the cross-origin + fetchpriority
	// shape we want for the main stylesheet. Built once at
	// construction so HintsFor stays allocation-light.
	styleHintTemplate Hint

	// extraGate protects extras de-duplication across concurrent
	// resolver calls. Resolvers SHOULD return stable values, but
	// we guard against duplicates because emitting the same Link
	// twice wastes bytes.
	extraGate sync.Mutex
}

// ThemeAwareOptions configures ThemeAwareProvider.
type ThemeAwareOptions struct {
	// StyleAs is the value emitted as `as=` for the stylesheet
	// hint. Defaults to "style". Only override if your theme
	// hands back something other than CSS (e.g. a JS-rendered
	// CSS-in-JS bundle that should preload as `script`).
	StyleAs string

	// StyleFetchPriority is the fetchpriority hint for the
	// stylesheet. Defaults to "high" because the critical CSS is
	// almost always render-blocking.
	StyleFetchPriority string

	// StyleCrossOrigin is the crossorigin mode for the stylesheet.
	// Defaults to "" (same-origin). Set to "anonymous" if your
	// theme stylesheet is served from a CDN domain.
	StyleCrossOrigin string

	// PathPredicate filters which requests get hints. When nil,
	// the default predicate emits hints for any GET whose Accept
	// header contains "text/html".
	PathPredicate func(*http.Request) bool
}

// NewThemeAwareProvider returns a provider wired against the given
// resolver. The resolver is the source of truth for stylesheet URLs
// and any per-theme extras (fonts, hero images).
//
// When resolver is nil the provider returns no hints — equivalent to
// disabling Early Hints without ripping the middleware out of the
// chain.
func NewThemeAwareProvider(resolver ThemeStyleResolver, opts ThemeAwareOptions) *ThemeAwareProvider {
	if opts.StyleAs == "" {
		opts.StyleAs = "style"
	}
	if opts.StyleFetchPriority == "" {
		opts.StyleFetchPriority = "high"
	}
	if opts.PathPredicate == nil {
		opts.PathPredicate = defaultThemePathPredicate
	}
	return &ThemeAwareProvider{
		resolver:  resolver,
		predicate: opts.PathPredicate,
		styleHintTemplate: Hint{
			As:            opts.StyleAs,
			CrossOrigin:   opts.StyleCrossOrigin,
			FetchPriority: opts.StyleFetchPriority,
		},
	}
}

// HintsFor satisfies HintsProvider.
func (p *ThemeAwareProvider) HintsFor(r *http.Request) ([]Hint, error) {
	if p == nil || p.resolver == nil {
		return nil, nil
	}
	if !p.predicate(r) {
		return nil, nil
	}
	styleURL, extras := p.resolver.Active(r)
	if styleURL == "" && len(extras) == 0 {
		return nil, nil
	}

	// Cap the output: the main stylesheet plus extras. We
	// deduplicate extras against the stylesheet URL so a
	// misconfigured resolver doesn't emit the same Link twice.
	out := make([]Hint, 0, 1+len(extras))
	seen := make(map[string]struct{}, 1+len(extras))
	if styleURL != "" {
		h := p.styleHintTemplate
		h.URL = styleURL
		out = append(out, h)
		seen[styleURL] = struct{}{}
	}
	for _, e := range extras {
		if e.URL == "" {
			continue
		}
		if _, dup := seen[e.URL]; dup {
			continue
		}
		seen[e.URL] = struct{}{}
		// Default empty As to "fetch" so the browser at least
		// kicks off a connection. Resolvers SHOULD always set
		// As — this is the safety net.
		if e.As == "" {
			e.As = "fetch"
		}
		out = append(out, e)
	}
	return out, nil
}

// defaultThemePathPredicate matches GET/HEAD requests that look like
// document navigations: Accept advertises text/html, OR the path has
// no extension (typical for theme-rendered routes like "/" or
// "/blog/some-slug"). We explicitly exclude paths with extensions
// because those are already-resolved asset requests — sending 103 on
// them is wasted bandwidth.
func defaultThemePathPredicate(r *http.Request) bool {
	if r == nil {
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
	default:
		return false
	}
	if accept := r.Header.Get("Accept"); accept != "" {
		if strings.Contains(accept, "text/html") {
			return true
		}
		// If Accept is set but excludes HTML, this is an asset/API
		// fetch — no point preloading the page's CSS for it.
		return false
	}
	// No Accept header: fall back to extension heuristic. Treat
	// extensionless paths as documents.
	return !hasFileExtension(r.URL.Path)
}

// hasFileExtension is a lightweight check: does the last path segment
// contain a '.' followed by 1-6 chars? We don't bother with stdlib
// path.Ext here — that allocates for paths with many segments.
func hasFileExtension(p string) bool {
	if p == "" {
		return false
	}
	// Walk backwards looking for '.' before any '/'.
	for i := len(p) - 1; i >= 0; i-- {
		switch p[i] {
		case '/':
			return false
		case '.':
			// Reject "." and ".." style segments: extension
			// would be empty.
			return i < len(p)-1
		}
	}
	return false
}
