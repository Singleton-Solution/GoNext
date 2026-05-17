package security

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// Options configures the Headers middleware. Each field controls a single
// response header; the zero value of each field has a documented meaning.
//
// To DISABLE a header entirely, set its Disable* sibling to true. A blank
// value with Disable=false falls back to the canonical default for that
// header (see DefaultOptions).
//
// To OVERRIDE a header's value, set the corresponding string field.
// Validation is minimal — the middleware trusts the caller. Bad header
// values fail at runtime in the browser, not here.
type Options struct {
	// HSTS controls Strict-Transport-Security. The default is the value
	// recommended for preload submission: 2 years, includeSubDomains, preload.
	HSTS        HSTSOptions
	DisableHSTS bool

	// ContentTypeOptions controls X-Content-Type-Options. The only useful
	// value is "nosniff"; the field exists for symmetry / override.
	ContentTypeOptions        string
	DisableContentTypeOptions bool

	// ReferrerPolicy controls Referrer-Policy. Default:
	// "strict-origin-when-cross-origin".
	ReferrerPolicy        string
	DisableReferrerPolicy bool

	// PermissionsPolicy controls Permissions-Policy. When empty (default),
	// the deny-by-default policy from docs/13-security-baseline.md §2.2 is
	// emitted. The PermissionsAllow map opts specific features back in
	// (key = feature, value = allowlist token, e.g. "self" or "*").
	//
	// If PermissionsPolicy is non-empty, it is used verbatim and
	// PermissionsAllow is ignored.
	PermissionsPolicy        string
	PermissionsAllow         map[string]string
	DisablePermissionsPolicy bool

	// COOP controls Cross-Origin-Opener-Policy. Default: "same-origin".
	COOP        string
	DisableCOOP bool

	// COEP controls Cross-Origin-Embedder-Policy. Default: "require-corp".
	// Setting COEP = "credentialless" relaxes the policy for public sites
	// that need to load third-party resources without credentials.
	COEP        string
	DisableCOEP bool

	// CORP controls Cross-Origin-Resource-Policy. Default: "same-site".
	// For media/asset responses meant to be embedded cross-origin, set
	// "cross-origin".
	CORP        string
	DisableCORP bool

	// FrameOptions controls X-Frame-Options. Default: "DENY". Override
	// with "SAMEORIGIN" for public pages that need to be embedded by the
	// same origin (rare; CSP frame-ancestors is the modern replacement).
	FrameOptions        string
	DisableFrameOptions bool

	// PermittedCrossDomainPolicies controls X-Permitted-Cross-Domain-Policies.
	// Default: "none". The header is legacy (Adobe Flash / Acrobat clients
	// honored it for cross-domain policy files), but it remains in the
	// canonical matrix as defense-in-depth against old plugin clients.
	PermittedCrossDomainPolicies        string
	DisablePermittedCrossDomainPolicies bool

	// OriginAgentCluster controls Origin-Agent-Cluster. Default: "?1".
	// This is a structured-headers boolean that requests origin-keyed
	// agent clusters from supporting browsers (perf + isolation hint).
	OriginAgentCluster        string
	DisableOriginAgentCluster bool

	// StripIdentifyingHeaders, when true, removes the Server and
	// X-Powered-By response headers before the inner handler runs.
	// Reverse proxies and frameworks frequently set these; stripping them
	// removes a cheap fingerprinting vector. Defaults to true in every
	// preset (DefaultOptions, PublicSite, Admin, RESTAPI). Set the
	// Disable* sibling to opt out explicitly.
	StripIdentifyingHeaders        bool
	DisableStripIdentifyingHeaders bool
}

// HSTSOptions models the Strict-Transport-Security directive components
// individually so callers can compose them without string surgery.
//
// The zero value produces no header (use DisableHSTS for explicit intent;
// MaxAgeSeconds=0 also yields no header to avoid accidentally serving a
// header that browsers interpret as "clear pin"). DefaultOptions sets the
// preload-list-eligible recommendation.
type HSTSOptions struct {
	MaxAgeSeconds     int
	IncludeSubDomains bool
	Preload           bool
}

// String renders the HSTSOptions as a header value, or empty string if
// MaxAgeSeconds <= 0 (signalling the header should be omitted).
func (h HSTSOptions) String() string {
	if h.MaxAgeSeconds <= 0 {
		return ""
	}
	parts := []string{"max-age=" + strconv.Itoa(h.MaxAgeSeconds)}
	if h.IncludeSubDomains {
		parts = append(parts, "includeSubDomains")
	}
	if h.Preload {
		parts = append(parts, "preload")
	}
	return strings.Join(parts, "; ")
}

// DefaultOptions returns the production-recommended baseline: HSTS preload
// for 2 years, nosniff, strict-origin-when-cross-origin, deny-by-default
// Permissions-Policy, same-origin COOP, require-corp COEP, same-site CORP,
// and X-Frame-Options DENY.
//
// These defaults assume an HTML-serving origin behind TLS. JSON APIs
// should prefer RESTAPI(); see that constructor for the rationale.
func DefaultOptions() Options {
	return Options{
		HSTS: HSTSOptions{
			MaxAgeSeconds:     63072000, // 2 years, preload-list eligible
			IncludeSubDomains: true,
			Preload:           true,
		},
		ContentTypeOptions: "nosniff",
		ReferrerPolicy:     "strict-origin-when-cross-origin",
		// PermissionsPolicy left blank → deny-by-default emitted at runtime.
		COOP:                         "same-origin",
		COEP:                         "require-corp",
		CORP:                         "same-site",
		FrameOptions:                 "DENY",
		PermittedCrossDomainPolicies: "none",
		OriginAgentCluster:           "?1",
		StripIdentifyingHeaders:      true,
	}
}

// PublicSite returns Options tuned for a public HTML site that may embed
// third-party content (oEmbed, ads, fonts). COEP is loosened to
// "credentialless" so cross-origin resources load without credentials but
// still respect CORP.
//
// Use this on themes' rendered pages where embed-friendliness matters.
func PublicSite() Options {
	o := DefaultOptions()
	o.COEP = "credentialless"
	return o
}

// Admin returns Options tuned for admin UIs: strictest framing and
// embedder rules. Equivalent to DefaultOptions but named to make
// intent explicit at the call site.
//
// Admin pages should never be framed and should never embed third-party
// content; COEP=require-corp and X-Frame-Options=DENY are non-negotiable.
func Admin() Options {
	o := DefaultOptions()
	o.COEP = "require-corp"
	o.FrameOptions = "DENY"
	o.ReferrerPolicy = "same-origin"
	return o
}

// RESTAPI returns Options tuned for JSON APIs consumed by other origins.
// The opener/embedder policies are dropped (they apply to documents, not
// JSON responses); CORP is relaxed to "cross-origin" so browser fetches
// from approved origins succeed. CORS allow-listing is handled by a
// separate CORS middleware — this only sets the headers that don't
// depend on the request's Origin.
func RESTAPI() Options {
	o := DefaultOptions()
	o.DisableCOOP = true
	o.DisableCOEP = true
	o.CORP = "cross-origin"
	o.ReferrerPolicy = "no-referrer"
	// Permissions-Policy on REST is just an interest-cohort opt-out;
	// emitted verbatim so it doesn't grow with the public-site default.
	o.PermissionsPolicy = "interest-cohort=()"
	// XFO is meaningless for JSON; keep it for defense-in-depth in case
	// of HTML-typed mis-served responses.
	o.FrameOptions = "DENY"
	return o
}

// Headers returns a middleware that applies opts to every response.
// Headers are set before the inner handler runs; the inner handler may
// overwrite any of them by calling w.Header().Set after the middleware
// has run.
//
// The middleware is stateless and goroutine-safe: opts is read-only
// after construction.
func Headers(opts Options) func(http.Handler) http.Handler {
	// Resolve all header values once, at construction time. The hot path
	// then only copies strings into w.Header().
	type resolved struct {
		key, value string
	}
	var headers []resolved

	if !opts.DisableHSTS {
		if v := opts.HSTS.String(); v != "" {
			headers = append(headers, resolved{"Strict-Transport-Security", v})
		}
	}
	if !opts.DisableContentTypeOptions {
		v := opts.ContentTypeOptions
		if v == "" {
			v = "nosniff"
		}
		headers = append(headers, resolved{"X-Content-Type-Options", v})
	}
	if !opts.DisableReferrerPolicy {
		v := opts.ReferrerPolicy
		if v == "" {
			v = "strict-origin-when-cross-origin"
		}
		headers = append(headers, resolved{"Referrer-Policy", v})
	}
	if !opts.DisablePermissionsPolicy {
		v := opts.PermissionsPolicy
		if v == "" {
			v = buildPermissionsPolicy(opts.PermissionsAllow)
		}
		headers = append(headers, resolved{"Permissions-Policy", v})
	}
	if !opts.DisableCOOP {
		v := opts.COOP
		if v == "" {
			v = "same-origin"
		}
		headers = append(headers, resolved{"Cross-Origin-Opener-Policy", v})
	}
	if !opts.DisableCOEP {
		v := opts.COEP
		if v == "" {
			v = "require-corp"
		}
		headers = append(headers, resolved{"Cross-Origin-Embedder-Policy", v})
	}
	if !opts.DisableCORP {
		v := opts.CORP
		if v == "" {
			v = "same-site"
		}
		headers = append(headers, resolved{"Cross-Origin-Resource-Policy", v})
	}
	if !opts.DisableFrameOptions {
		v := opts.FrameOptions
		if v == "" {
			v = "DENY"
		}
		headers = append(headers, resolved{"X-Frame-Options", v})
	}
	if !opts.DisablePermittedCrossDomainPolicies {
		v := opts.PermittedCrossDomainPolicies
		if v == "" {
			v = "none"
		}
		headers = append(headers, resolved{"X-Permitted-Cross-Domain-Policies", v})
	}
	if !opts.DisableOriginAgentCluster {
		v := opts.OriginAgentCluster
		if v == "" {
			v = "?1"
		}
		headers = append(headers, resolved{"Origin-Agent-Cluster", v})
	}

	// stripIdentifying captures whether the middleware should delete the
	// Server / X-Powered-By headers before invoking the inner handler.
	// We resolve once at construction time so the hot path is branch-free.
	stripIdentifying := opts.StripIdentifyingHeaders && !opts.DisableStripIdentifyingHeaders

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			for _, hdr := range headers {
				h.Set(hdr.key, hdr.value)
			}
			if stripIdentifying {
				// Delete before next runs so a downstream handler that
				// re-sets them wins; this is the documented contract
				// ("the middleware writes, it does not lock"). Callers who
				// need a hard guarantee should wrap the response writer.
				h.Del("Server")
				h.Del("X-Powered-By")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// defaultPermissionsDenyList is the deny-by-default feature set from
// docs/13-security-baseline.md §2.2. Listed alphabetically (kept stable
// so test assertions don't churn).
//
// Each feature is emitted as `name=()` unless the allowlist overrides it.
// "self" and other allowlist tokens are wrapped in parentheses by
// buildPermissionsPolicy; raw "*" is also accepted.
var defaultPermissionsDenyList = []string{
	"accelerometer",
	"ambient-light-sensor",
	"autoplay",
	"battery",
	"camera",
	"cross-origin-isolated",
	"display-capture",
	"document-domain",
	"encrypted-media",
	"execution-while-not-rendered",
	"execution-while-out-of-viewport",
	"fullscreen",
	"geolocation",
	"gyroscope",
	"hid",
	"identity-credentials-get",
	"idle-detection",
	"interest-cohort",
	"keyboard-map",
	"magnetometer",
	"microphone",
	"midi",
	"navigation-override",
	"payment",
	"picture-in-picture",
	"publickey-credentials-create",
	"publickey-credentials-get",
	"screen-wake-lock",
	"serial",
	"storage-access",
	"sync-xhr",
	"usb",
	"web-share",
	"window-management",
	"xr-spatial-tracking",
}

// buildPermissionsPolicy renders the deny-by-default Permissions-Policy,
// applying any per-feature allowlist overrides. Output is deterministic
// (sorted) so tests can pin the value.
//
// allow values may be:
//   - "self"  → rendered as (self)
//   - "*"     → rendered as *
//   - "()"    → rendered as () (i.e. deny; equivalent to omission)
//   - any other string → rendered verbatim, wrapped in parens if missing
func buildPermissionsPolicy(allow map[string]string) string {
	// Build a feature → directive value map, starting with the deny list.
	features := make(map[string]string, len(defaultPermissionsDenyList)+len(allow))
	for _, name := range defaultPermissionsDenyList {
		features[name] = "()"
	}
	for name, val := range allow {
		features[name] = formatAllowValue(val)
	}

	// Stable output for testability.
	names := make([]string, 0, len(features))
	for name := range features {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	for i, name := range names {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(features[name])
	}
	return b.String()
}

// formatAllowValue normalizes the allowlist token to a valid
// Permissions-Policy directive value.
func formatAllowValue(v string) string {
	v = strings.TrimSpace(v)
	switch v {
	case "":
		return "()"
	case "*":
		return "*"
	}
	if strings.HasPrefix(v, "(") && strings.HasSuffix(v, ")") {
		return v
	}
	// Bare "self" or origin → wrap in parens. Quoting of origins is the
	// caller's responsibility per the spec; this function does not try to
	// re-quote already-quoted origins.
	return "(" + v + ")"
}
