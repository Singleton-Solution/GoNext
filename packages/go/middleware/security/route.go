package security

import (
	"net/http"
	"sort"
	"strings"
	"sync"
)

// RouteClass enumerates the high-level categories of HTTP routes that
// receive distinct security-header treatment. The classifier maps each
// incoming request to one of these classes; the Headers middleware then
// picks the appropriate Options preset for the class.
//
// The set is intentionally small. New classes should be added only when a
// route's header requirements genuinely diverge from every existing
// preset — otherwise a callsite override on Options is the right
// vehicle.
type RouteClass int

const (
	// RouteClassPublic identifies public, anonymously reachable HTML
	// pages. Maps to PublicSite() (loosened COEP for embeds).
	RouteClassPublic RouteClass = iota

	// RouteClassAdmin identifies authenticated administrative UIs. Maps
	// to Admin() (strictest framing/embedder rules; same-origin
	// referrer).
	RouteClassAdmin

	// RouteClassRESTAPI identifies JSON APIs intended for programmatic
	// consumption from other origins. Maps to RESTAPI() (drops
	// document-only headers, relaxes CORP to cross-origin).
	RouteClassRESTAPI

	// RouteClassPluginFrontend identifies plugin-frontend asset routes
	// (the host application embeds these scripts/HTML islands). Maps to
	// a PublicSite-derived preset; isolated from RouteClassPublic so it
	// can drift independently in future.
	RouteClassPluginFrontend

	// RouteClassMedia identifies media / static asset routes that must
	// be embeddable cross-origin (images, fonts, video). CORP relaxed to
	// "cross-origin"; document-only opener/embedder headers dropped.
	RouteClassMedia
)

// String renders the RouteClass for logging and error messages.
// Not part of the API contract — values are stable but the formatting is
// not guaranteed across releases.
func (c RouteClass) String() string {
	switch c {
	case RouteClassPublic:
		return "public"
	case RouteClassAdmin:
		return "admin"
	case RouteClassRESTAPI:
		return "rest-api"
	case RouteClassPluginFrontend:
		return "plugin-frontend"
	case RouteClassMedia:
		return "media"
	default:
		return "unknown"
	}
}

// Classifier maps an *http.Request to a RouteClass. The default
// Classify uses a path-prefix-based classifier configured via
// SetClassifierPrefixes; callers who need request-aware logic (header
// sniffing, method-based routing) can provide a custom function via
// SetClassifier.
//
// Classifier must be safe for concurrent use; it is invoked on the hot
// path of every request.
type Classifier func(r *http.Request) RouteClass

// prefixMap holds the path-prefix → RouteClass table used by the default
// classifier. Longest prefix wins so that "/admin/api" maps to REST API,
// not Admin, when configured.
type prefixMap struct {
	prefixes []prefixEntry // sorted by len(prefix) descending
	fallback RouteClass
}

type prefixEntry struct {
	prefix string
	class  RouteClass
}

// defaultPrefixes is the out-of-the-box prefix table. It is conservative:
// nothing matches except the bare /api and /admin namespaces.  Callers
// running real applications are expected to call SetClassifierPrefixes
// once at startup with the prefixes that match their mux.
var defaultPrefixes = []prefixEntry{
	{"/admin/api/", RouteClassRESTAPI},
	{"/admin/", RouteClassAdmin},
	{"/api/", RouteClassRESTAPI},
	{"/media/", RouteClassMedia},
	{"/static/", RouteClassMedia},
	{"/assets/", RouteClassMedia},
	{"/plugins/", RouteClassPluginFrontend},
}

// classifierMu guards the package-level classifier and prefix table.
// Reads happen on every request; writes happen at startup.  We use a
// RWMutex so the hot path is read-locked.
var (
	classifierMu sync.RWMutex
	classifierFn Classifier
	prefixTable  = newPrefixMap(defaultPrefixes, RouteClassPublic)
)

// newPrefixMap builds a prefixMap from the given entries, sorting by
// prefix length descending so the longest-match wins during lookup.
func newPrefixMap(entries []prefixEntry, fallback RouteClass) *prefixMap {
	sorted := make([]prefixEntry, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(i, j int) bool {
		return len(sorted[i].prefix) > len(sorted[j].prefix)
	})
	return &prefixMap{prefixes: sorted, fallback: fallback}
}

// classify walks the prefix table and returns the first (longest) match.
// If nothing matches, the configured fallback is returned.
func (p *prefixMap) classify(path string) RouteClass {
	for _, e := range p.prefixes {
		if strings.HasPrefix(path, e.prefix) {
			return e.class
		}
	}
	return p.fallback
}

// SetClassifierPrefixes replaces the path-prefix table used by the
// default Classify function. Prefixes are matched longest-first.
// fallback is the RouteClass returned when no prefix matches; pass
// RouteClassPublic for the typical "everything else is a public page"
// shape.
//
// Safe to call at startup. Concurrent calls are serialized; a request
// in flight at the moment of replacement sees either the old or the new
// table atomically.
func SetClassifierPrefixes(entries map[string]RouteClass, fallback RouteClass) {
	list := make([]prefixEntry, 0, len(entries))
	for prefix, class := range entries {
		list = append(list, prefixEntry{prefix: prefix, class: class})
	}
	pm := newPrefixMap(list, fallback)

	classifierMu.Lock()
	prefixTable = pm
	classifierMu.Unlock()
}

// SetClassifier installs a custom request-aware classifier. Passing nil
// restores the default path-prefix classifier.
//
// Use this when prefix matching is not expressive enough — e.g. when the
// route class depends on the Accept header, an auth claim, or a method.
func SetClassifier(fn Classifier) {
	classifierMu.Lock()
	classifierFn = fn
	classifierMu.Unlock()
}

// Classify returns the RouteClass for r. If a custom classifier was
// installed via SetClassifier, it is invoked; otherwise the default
// path-prefix classifier is used.
//
// Never panics. A nil request maps to the fallback class (public).
func Classify(r *http.Request) RouteClass {
	if r == nil {
		return RouteClassPublic
	}
	classifierMu.RLock()
	fn := classifierFn
	pm := prefixTable
	classifierMu.RUnlock()

	if fn != nil {
		return fn(r)
	}
	if r.URL == nil {
		return pm.fallback
	}
	return pm.classify(r.URL.Path)
}

// OptionsFor returns the Options preset that corresponds to the given
// route class. Exposed so callers can build a Headers middleware
// statically when they already know the class (e.g. on a dedicated
// /api/* mux). For per-request dispatch, prefer ClassifiedHeaders.
func OptionsFor(class RouteClass) Options {
	switch class {
	case RouteClassAdmin:
		return Admin()
	case RouteClassRESTAPI:
		return RESTAPI()
	case RouteClassPluginFrontend:
		// Plugin frontends are public-style HTML islands; keep the
		// public preset but be explicit so future divergence is a
		// one-line change.
		return PublicSite()
	case RouteClassMedia:
		// Media must be cross-origin-embeddable. Drop document-only
		// headers; relax CORP.
		o := PublicSite()
		o.DisableCOOP = true
		o.DisableCOEP = true
		o.CORP = "cross-origin"
		o.FrameOptions = "DENY"
		return o
	case RouteClassPublic:
		fallthrough
	default:
		return PublicSite()
	}
}

// ClassifiedHeaders returns a middleware that classifies each request
// via Classify and then applies the Options preset returned by
// OptionsFor.  Header values for every class are resolved once at
// construction time so the hot path performs no allocation.
//
// Use this when one mux serves multiple route classes (e.g. a monolith
// that mixes /admin, /api, and /static under a single handler chain).
// For homogeneous muxes, prefer Headers(OptionsFor(class)) directly —
// it avoids the per-request classification cost.
func ClassifiedHeaders() func(http.Handler) http.Handler {
	// Pre-build one Headers middleware per class. The map lookup on the
	// hot path is O(1); each lookup returns a pre-resolved handler.
	mws := map[RouteClass]func(http.Handler) http.Handler{
		RouteClassPublic:         Headers(OptionsFor(RouteClassPublic)),
		RouteClassAdmin:          Headers(OptionsFor(RouteClassAdmin)),
		RouteClassRESTAPI:        Headers(OptionsFor(RouteClassRESTAPI)),
		RouteClassPluginFrontend: Headers(OptionsFor(RouteClassPluginFrontend)),
		RouteClassMedia:          Headers(OptionsFor(RouteClassMedia)),
	}

	return func(next http.Handler) http.Handler {
		// Wrap once per class up-front. Each wrapped handler shares the
		// same `next` and is selected per-request.
		wrapped := make(map[RouteClass]http.Handler, len(mws))
		for c, mw := range mws {
			wrapped[c] = mw(next)
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h, ok := wrapped[Classify(r)]
			if !ok {
				h = wrapped[RouteClassPublic]
			}
			h.ServeHTTP(w, r)
		})
	}
}
