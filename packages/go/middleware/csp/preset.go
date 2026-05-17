package csp

// PolicyOptions configures the preset Policy builders. Each preset
// (PublicSitePolicy, AdminPolicy) accepts a PolicyOptions so callers can
// supply environment-specific hosts (media CDN, oEmbed providers, …) and
// reporting endpoints without hand-writing a Policy literal.
//
// The zero value of PolicyOptions yields a sensible production-shaped
// policy; every field is optional.
type PolicyOptions struct {
	// MediaHosts is the list of host-source expressions allowed for
	// media-src and img-src (e.g. "https://media.example.com",
	// "https://cdn.example.com"). The hosts are emitted verbatim.
	MediaHosts []string

	// ConnectHosts adds additional host-source values to connect-src
	// beyond 'self'. Useful for API origins or analytics endpoints
	// the page legitimately reaches.
	ConnectHosts []string

	// OEmbedHosts adds host-source values to frame-src. The presets'
	// public policy includes these so themes can embed YouTube, Vimeo,
	// X/Twitter, etc., previews. Ignored by AdminPolicy.
	OEmbedHosts []string

	// ScriptHosts adds host-source values to script-src. Use sparingly —
	// 'strict-dynamic' (in PublicSitePolicy) makes host allowlisting
	// largely unnecessary, but some legacy embeds still require it.
	ScriptHosts []string

	// StyleHosts adds host-source values to style-src and style-src-elem.
	// Use for hosted CSS files (e.g. Google Fonts CSS).
	StyleHosts []string

	// FontHosts adds host-source values to font-src.
	FontHosts []string

	// FrameAncestors overrides the default frame-ancestors source list.
	// If nil, PublicSitePolicy defaults to ['self'] and AdminPolicy to
	// ['none'].
	FrameAncestors []SourceExpr

	// ExtraImgSchemes appends scheme sources (e.g. Scheme("blob:")) to
	// img-src. The presets already include "data:".
	ExtraImgSchemes []SourceExpr

	// ExtraMediaSchemes appends scheme sources to media-src.
	ExtraMediaSchemes []SourceExpr

	// ReportURI is emitted as the report-uri directive. The convention
	// in this monorepo is "/_/csp-report".
	ReportURI string

	// ReportTo is emitted as the report-to directive (CSP3 Reporting
	// API). Must match an endpoint group declared via the
	// Reporting-Endpoints or Report-To response header.
	ReportTo string

	// IncludeStrictDynamic, when set, forces or suppresses
	// 'strict-dynamic' in script-src. The pointer shape distinguishes
	// "unset" (use the preset's default) from "false" (suppress).
	IncludeStrictDynamic *bool

	// IncludeUpgradeInsecureRequests, when set, forces or suppresses the
	// upgrade-insecure-requests directive. Default: true for both
	// presets.
	IncludeUpgradeInsecureRequests *bool

	// TrustedTypePolicies overrides the default trusted-types policy
	// list emitted by AdminPolicy. Ignored by PublicSitePolicy.
	TrustedTypePolicies []string
}

// boolPtr returns a *bool pointing to v. Convenience for callers building
// PolicyOptions in literal form.
func boolPtr(v bool) *bool { return &v }

// PublicSitePolicy returns the public-site CSP from
// docs/13-security-baseline.md §3.1.
//
// The policy is strict-by-default:
//
//   - default-src 'self'
//   - script-src  'self' 'strict-dynamic' (per-request nonce folded in
//     by Middleware via WithNonce)
//   - style-src   'self' (per-request nonce folded in by Middleware)
//   - img-src     'self' data: + opts.MediaHosts
//   - font-src    'self' data:
//   - connect-src 'self' + opts.MediaHosts + opts.ConnectHosts
//   - frame-src   'self' + opts.OEmbedHosts
//   - media-src   'self' + opts.MediaHosts
//   - object-src  'none'
//   - base-uri    'self'
//   - form-action 'self'
//   - frame-ancestors 'self' (override via opts.FrameAncestors)
//   - worker-src  'self'
//   - manifest-src 'self'
//   - upgrade-insecure-requests
//   - report-uri / report-to per opts
//
// The returned Policy is intended to be passed to Middleware; do not
// mutate the returned value across requests.
func PublicSitePolicy(opts PolicyOptions) *Policy {
	p := &Policy{
		DefaultSrc:     []SourceExpr{Self()},
		ScriptSrc:      []SourceExpr{Self()},
		StyleSrc:       []SourceExpr{Self()},
		ImgSrc:         append([]SourceExpr{Self(), Scheme("data:")}, hostsToSources(opts.MediaHosts)...),
		FontSrc:        []SourceExpr{Self(), Scheme("data:")},
		ConnectSrc:     append([]SourceExpr{Self()}, hostsToSources(append(append([]string{}, opts.MediaHosts...), opts.ConnectHosts...))...),
		FrameSrc:       append([]SourceExpr{Self()}, hostsToSources(opts.OEmbedHosts)...),
		MediaSrc:       append([]SourceExpr{Self()}, hostsToSources(opts.MediaHosts)...),
		ObjectSrc:      []SourceExpr{None()},
		BaseURI:        []SourceExpr{Self()},
		FormAction:     []SourceExpr{Self()},
		FrameAncestors: []SourceExpr{Self()},
		WorkerSrc:      []SourceExpr{Self()},
		ManifestSrc:    []SourceExpr{Self()},
	}

	// Caller-supplied extra hosts.
	if len(opts.ScriptHosts) > 0 {
		p.ScriptSrc = append(p.ScriptSrc, hostsToSources(opts.ScriptHosts)...)
	}
	if len(opts.StyleHosts) > 0 {
		p.StyleSrc = append(p.StyleSrc, hostsToSources(opts.StyleHosts)...)
	}
	if len(opts.FontHosts) > 0 {
		p.FontSrc = append(p.FontSrc, hostsToSources(opts.FontHosts)...)
	}
	if len(opts.ExtraImgSchemes) > 0 {
		p.ImgSrc = append(p.ImgSrc, opts.ExtraImgSchemes...)
	}
	if len(opts.ExtraMediaSchemes) > 0 {
		p.MediaSrc = append(p.MediaSrc, opts.ExtraMediaSchemes...)
	}
	if opts.FrameAncestors != nil {
		p.FrameAncestors = append([]SourceExpr(nil), opts.FrameAncestors...)
	}

	// strict-dynamic: default ON for public site (per §3.1).
	includeStrictDynamic := true
	if opts.IncludeStrictDynamic != nil {
		includeStrictDynamic = *opts.IncludeStrictDynamic
	}
	if includeStrictDynamic {
		p.ScriptSrc = append(p.ScriptSrc, StrictDynamic())
	}

	// upgrade-insecure-requests: default ON.
	p.UpgradeInsecureRequests = true
	if opts.IncludeUpgradeInsecureRequests != nil {
		p.UpgradeInsecureRequests = *opts.IncludeUpgradeInsecureRequests
	}

	p.ReportURI = opts.ReportURI
	p.ReportTo = opts.ReportTo

	return p
}

// AdminPolicy returns the admin (block editor) CSP from
// docs/13-security-baseline.md §3.2.
//
// Stricter than PublicSitePolicy in three ways:
//
//   - script-src omits 'strict-dynamic' (admin scripts are a fixed set)
//   - frame-ancestors 'none' (admin must not be framed)
//   - require-trusted-types-for 'script' + trusted-types policies
//
// connect-src defaults to 'self' only; opts.ConnectHosts can append.
// worker-src includes blob: by default for the block editor's
// syntax-highlight workers.
func AdminPolicy(opts PolicyOptions) *Policy {
	p := &Policy{
		DefaultSrc:     []SourceExpr{Self()},
		ScriptSrc:      []SourceExpr{Self()},
		StyleSrc:       []SourceExpr{Self()},
		ImgSrc:         append([]SourceExpr{Self(), Scheme("data:"), Scheme("blob:")}, hostsToSources(opts.MediaHosts)...),
		FontSrc:        []SourceExpr{Self(), Scheme("data:")},
		ConnectSrc:     append([]SourceExpr{Self()}, hostsToSources(opts.ConnectHosts)...),
		FrameSrc:       []SourceExpr{Self()},
		MediaSrc:       append([]SourceExpr{Self(), Scheme("blob:")}, hostsToSources(opts.MediaHosts)...),
		ObjectSrc:      []SourceExpr{None()},
		BaseURI:        []SourceExpr{Self()},
		FormAction:     []SourceExpr{Self()},
		FrameAncestors: []SourceExpr{None()},
		WorkerSrc:      []SourceExpr{Self(), Scheme("blob:")},
		ManifestSrc:    []SourceExpr{Self()},

		RequireTrustedTypesFor: []string{"script"},
		TrustedTypes:           []string{"default", "nextjs#bundler", "dompurify"},
	}

	// Caller-supplied extras.
	if len(opts.ScriptHosts) > 0 {
		p.ScriptSrc = append(p.ScriptSrc, hostsToSources(opts.ScriptHosts)...)
	}
	if len(opts.StyleHosts) > 0 {
		p.StyleSrc = append(p.StyleSrc, hostsToSources(opts.StyleHosts)...)
	}
	if len(opts.FontHosts) > 0 {
		p.FontSrc = append(p.FontSrc, hostsToSources(opts.FontHosts)...)
	}
	if len(opts.ExtraImgSchemes) > 0 {
		p.ImgSrc = append(p.ImgSrc, opts.ExtraImgSchemes...)
	}
	if len(opts.ExtraMediaSchemes) > 0 {
		p.MediaSrc = append(p.MediaSrc, opts.ExtraMediaSchemes...)
	}
	if opts.FrameAncestors != nil {
		p.FrameAncestors = append([]SourceExpr(nil), opts.FrameAncestors...)
	}
	if len(opts.TrustedTypePolicies) > 0 {
		p.TrustedTypes = append([]string(nil), opts.TrustedTypePolicies...)
	}

	// strict-dynamic: default OFF for admin per §3.2; explicit opt-in.
	includeStrictDynamic := false
	if opts.IncludeStrictDynamic != nil {
		includeStrictDynamic = *opts.IncludeStrictDynamic
	}
	if includeStrictDynamic {
		p.ScriptSrc = append(p.ScriptSrc, StrictDynamic())
	}

	p.UpgradeInsecureRequests = true
	if opts.IncludeUpgradeInsecureRequests != nil {
		p.UpgradeInsecureRequests = *opts.IncludeUpgradeInsecureRequests
	}

	p.ReportURI = opts.ReportURI
	p.ReportTo = opts.ReportTo

	return p
}

// hostsToSources lifts a slice of host strings to SourceExpr values
// using Host(). Empty / whitespace-only entries are skipped so callers
// can freely concatenate optional lists.
func hostsToSources(hosts []string) []SourceExpr {
	if len(hosts) == 0 {
		return nil
	}
	out := make([]SourceExpr, 0, len(hosts))
	for _, h := range hosts {
		if h == "" {
			continue
		}
		out = append(out, Host(h))
	}
	return out
}
