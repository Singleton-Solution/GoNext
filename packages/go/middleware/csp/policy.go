package csp

import (
	"sort"
	"strings"
)

// SourceExpr is a typed CSP source expression. Each constructor below
// (Self, None, UnsafeInline, …) returns a SourceExpr that knows how to
// render itself into the CSP header's source-list syntax.
//
// Keeping source expressions typed (rather than `[]string`) prevents the
// caller-side mistakes that have historically caused CSP outages:
//
//   - mixing kw-tokens (must be single-quoted: 'self', 'none') with
//     host-source values (must NOT be quoted: https://example.com)
//   - forgetting to wrap a nonce in the 'nonce-…' form
//   - emitting empty directives because a slice element was the empty
//     string
//
// The String method is the single rendering chokepoint. SourceExprs are
// value types, safe to share across requests.
type SourceExpr struct {
	// kind disambiguates rendering. Kept private so callers must use the
	// constructors below.
	kind sourceKind
	// value is the raw payload — nonce bytes, sha256 digest, host string,
	// scheme prefix. Unused for keyword sources (self, none, …).
	value string
}

// sourceKind enumerates the supported CSP source-expression shapes.
type sourceKind uint8

const (
	srcSelf sourceKind = iota + 1
	srcNone
	srcUnsafeInline
	srcUnsafeEval
	srcWasmUnsafeEval
	srcStrictDynamic
	srcReportSample
	srcUnsafeHashes
	srcNonce
	srcSha256
	srcSha384
	srcSha512
	srcHost
	srcScheme
	srcRaw
)

// String renders the source expression in the canonical CSP token form.
//
// Keyword sources are emitted with their single-quote envelope; nonce
// and hash sources are emitted with their `nonce-…` / `sha…-` envelope;
// host and scheme sources are emitted verbatim (callers are expected to
// supply RFC-shaped values such as "https://example.com" or "https:").
//
// Invalid SourceExpr values (e.g. the zero value) render as the empty
// string so they can be skipped by the directive serializer.
func (s SourceExpr) String() string {
	switch s.kind {
	case srcSelf:
		return "'self'"
	case srcNone:
		return "'none'"
	case srcUnsafeInline:
		return "'unsafe-inline'"
	case srcUnsafeEval:
		return "'unsafe-eval'"
	case srcWasmUnsafeEval:
		return "'wasm-unsafe-eval'"
	case srcStrictDynamic:
		return "'strict-dynamic'"
	case srcReportSample:
		return "'report-sample'"
	case srcUnsafeHashes:
		return "'unsafe-hashes'"
	case srcNonce:
		return "'nonce-" + s.value + "'"
	case srcSha256:
		return "'sha256-" + s.value + "'"
	case srcSha384:
		return "'sha384-" + s.value + "'"
	case srcSha512:
		return "'sha512-" + s.value + "'"
	case srcHost, srcScheme, srcRaw:
		return s.value
	}
	return ""
}

// Self returns the keyword source 'self'. Matches the origin from which
// the protected document was served.
func Self() SourceExpr { return SourceExpr{kind: srcSelf} }

// None returns the keyword source 'none'. When 'none' is present in a
// directive, the directive matches nothing — regardless of any other
// expression in the list.
func None() SourceExpr { return SourceExpr{kind: srcNone} }

// UnsafeInline returns the keyword source 'unsafe-inline'. Allows
// inline script/style blocks. AVOID in new code — prefer Nonce() or
// Sha256() instead.
func UnsafeInline() SourceExpr { return SourceExpr{kind: srcUnsafeInline} }

// UnsafeEval returns the keyword source 'unsafe-eval'. Allows eval(),
// Function(), setTimeout(string), setInterval(string). AVOID in new code.
func UnsafeEval() SourceExpr { return SourceExpr{kind: srcUnsafeEval} }

// WasmUnsafeEval returns the keyword source 'wasm-unsafe-eval'. Allows
// WebAssembly compilation/instantiation without unlocking JS eval().
func WasmUnsafeEval() SourceExpr { return SourceExpr{kind: srcWasmUnsafeEval} }

// StrictDynamic returns the keyword source 'strict-dynamic'. Combined
// with a nonce or hash source, allows the explicitly-trusted script to
// load further scripts without per-host allowlisting. The recommended
// modern script-src shape.
func StrictDynamic() SourceExpr { return SourceExpr{kind: srcStrictDynamic} }

// ReportSample returns the keyword source 'report-sample'. When set on
// a directive, violation reports include a 40-char sample of the
// offending content (useful for debugging blocked inline scripts).
func ReportSample() SourceExpr { return SourceExpr{kind: srcReportSample} }

// UnsafeHashes returns the keyword source 'unsafe-hashes'. Allows the
// listed sha-* hashes to match inline event handlers (onclick="…") and
// javascript: navigation. Use sparingly.
func UnsafeHashes() SourceExpr { return SourceExpr{kind: srcUnsafeHashes} }

// Nonce returns a 'nonce-<value>' source. The caller is responsible for
// generating a cryptographically random nonce per request (see
// security.WithNonce in this monorepo). The middleware's WithNonce
// helper folds a nonce into script-src and style-src so callers do not
// usually construct Nonce() directly.
func Nonce(value string) SourceExpr { return SourceExpr{kind: srcNonce, value: value} }

// Sha256 returns a 'sha256-<digest>' source. The digest must be the
// base64-encoded SHA-256 of the inline script/style content (no
// prefix; the SourceExpr adds 'sha256-…').
func Sha256(digest string) SourceExpr { return SourceExpr{kind: srcSha256, value: digest} }

// Sha384 returns a 'sha384-<digest>' source. Same shape as Sha256.
func Sha384(digest string) SourceExpr { return SourceExpr{kind: srcSha384, value: digest} }

// Sha512 returns a 'sha512-<digest>' source. Same shape as Sha256.
func Sha512(digest string) SourceExpr { return SourceExpr{kind: srcSha512, value: digest} }

// Host returns a host-source expression such as "https://example.com",
// "*.cdn.example.com", or "example.com:443". The value is emitted
// verbatim; the caller is responsible for spec-shaped input.
func Host(host string) SourceExpr { return SourceExpr{kind: srcHost, value: host} }

// Scheme returns a scheme-source such as "https:", "data:", "blob:",
// "filesystem:", "mediastream:". The trailing colon is required.
func Scheme(scheme string) SourceExpr { return SourceExpr{kind: srcScheme, value: scheme} }

// Raw returns a passthrough source expression for tokens not modeled by
// the typed constructors above (e.g. experimental keywords). Use sparingly.
func Raw(value string) SourceExpr { return SourceExpr{kind: srcRaw, value: value} }

// IsZero reports whether s is the zero value (and thus should be
// skipped by the directive serializer).
func (s SourceExpr) IsZero() bool { return s.kind == 0 }

// Policy is a typed Content-Security-Policy. Each directive slice holds
// the source expressions to emit for that directive; an empty slice
// causes the directive to be omitted from the serialized header.
//
// Fields are ordered to match the canonical CSP3 directive order so
// that Policy literals read top-to-bottom in the same shape as the
// emitted header.
//
// All slice fields are independently nilable: leaving ScriptSrc nil and
// only setting DefaultSrc is valid (browsers fall back to default-src).
//
// Policy values are designed to be immutable in the hot path. Mutating
// methods (WithNonce) return a copy.
type Policy struct {
	// DefaultSrc is the fallback for fetch directives that are not
	// otherwise specified. Per CSP3, default-src does NOT cover
	// frame-ancestors, base-uri, form-action, or report-uri.
	DefaultSrc []SourceExpr

	// ScriptSrc governs <script> elements and inline event handlers.
	ScriptSrc []SourceExpr

	// ScriptSrcElem narrows ScriptSrc to <script> elements only,
	// excluding inline event handlers (which fall under
	// script-src-attr).
	ScriptSrcElem []SourceExpr

	// StyleSrc governs <style> elements and style="…" attributes.
	StyleSrc []SourceExpr

	// StyleSrcElem narrows StyleSrc to <style> / <link rel="stylesheet">
	// elements only, excluding inline style attributes.
	StyleSrcElem []SourceExpr

	// ImgSrc governs <img>, <picture>, favicon, etc.
	ImgSrc []SourceExpr

	// FontSrc governs @font-face / <link rel="preload" as="font">.
	FontSrc []SourceExpr

	// ConnectSrc governs fetch(), XHR, WebSocket, EventSource, etc.
	ConnectSrc []SourceExpr

	// FrameSrc governs <iframe>, <frame>.
	FrameSrc []SourceExpr

	// FrameAncestors governs which origins may frame THIS document
	// (the modern X-Frame-Options replacement). Not inherited from
	// default-src.
	FrameAncestors []SourceExpr

	// FormAction governs the URL targets of <form action="…"> /
	// formaction="…". Not inherited from default-src.
	FormAction []SourceExpr

	// BaseURI governs the URLs that may appear in <base href="…">.
	// Not inherited from default-src.
	BaseURI []SourceExpr

	// ObjectSrc governs <object>, <embed>, <applet>. Recommended to
	// pin to 'none' for all modern apps.
	ObjectSrc []SourceExpr

	// MediaSrc governs <audio>, <video>, <track>.
	MediaSrc []SourceExpr

	// ManifestSrc governs <link rel="manifest"> targets.
	ManifestSrc []SourceExpr

	// WorkerSrc governs Worker, SharedWorker, and ServiceWorker source
	// URLs. Note: ServiceWorker registration also requires it be in
	// script-src on legacy browsers.
	WorkerSrc []SourceExpr

	// UpgradeInsecureRequests, when true, emits the
	// upgrade-insecure-requests directive — instructing browsers to
	// auto-upgrade http: → https: requests for this document.
	UpgradeInsecureRequests bool

	// BlockAllMixedContent, when true, emits the (deprecated, but still
	// widely honored) block-all-mixed-content directive. Modern code
	// should rely on upgrade-insecure-requests instead.
	BlockAllMixedContent bool

	// Sandbox, when non-nil, emits the sandbox directive with the listed
	// flags ("allow-scripts", "allow-same-origin", …). An EMPTY non-nil
	// slice emits a bare `sandbox` directive (no allow tokens),
	// equivalent to the strictest sandbox.
	Sandbox []string

	// RequireTrustedTypesFor enables Trusted Types enforcement for the
	// listed sinks. The only currently-spec'd value is "script".
	RequireTrustedTypesFor []string

	// TrustedTypes names the Trusted Types policies allowed by this
	// document. The token "default" refers to the default policy; the
	// token "'allow-duplicates'" permits multiple policy creations with
	// the same name. Other tokens are policy names (e.g.
	// "nextjs#bundler", "dompurify").
	TrustedTypes []string

	// ReportURI, when non-empty, emits the (deprecated but
	// widely-supported) report-uri directive. Path-relative values
	// (e.g. "/_/csp-report") are accepted.
	ReportURI string

	// ReportTo, when non-empty, emits the (CSP3) report-to directive
	// naming an endpoint group declared via the Reporting-Endpoints or
	// Report-To response header.
	ReportTo string
}

// Clone returns a deep copy of p. Mutating the returned Policy does not
// affect the original.
func (p *Policy) Clone() *Policy {
	if p == nil {
		return &Policy{}
	}
	c := *p
	c.DefaultSrc = cloneSources(p.DefaultSrc)
	c.ScriptSrc = cloneSources(p.ScriptSrc)
	c.ScriptSrcElem = cloneSources(p.ScriptSrcElem)
	c.StyleSrc = cloneSources(p.StyleSrc)
	c.StyleSrcElem = cloneSources(p.StyleSrcElem)
	c.ImgSrc = cloneSources(p.ImgSrc)
	c.FontSrc = cloneSources(p.FontSrc)
	c.ConnectSrc = cloneSources(p.ConnectSrc)
	c.FrameSrc = cloneSources(p.FrameSrc)
	c.FrameAncestors = cloneSources(p.FrameAncestors)
	c.FormAction = cloneSources(p.FormAction)
	c.BaseURI = cloneSources(p.BaseURI)
	c.ObjectSrc = cloneSources(p.ObjectSrc)
	c.MediaSrc = cloneSources(p.MediaSrc)
	c.ManifestSrc = cloneSources(p.ManifestSrc)
	c.WorkerSrc = cloneSources(p.WorkerSrc)
	c.Sandbox = append([]string(nil), p.Sandbox...)
	c.RequireTrustedTypesFor = append([]string(nil), p.RequireTrustedTypesFor...)
	c.TrustedTypes = append([]string(nil), p.TrustedTypes...)
	return &c
}

func cloneSources(s []SourceExpr) []SourceExpr {
	if s == nil {
		return nil
	}
	out := make([]SourceExpr, len(s))
	copy(out, s)
	return out
}

// WithNonce returns a NEW Policy with the given nonce appended to
// script-src and style-src. The receiver is not modified.
//
// If nonce is the empty string the receiver is returned unchanged (so
// that callers can call WithNonce unconditionally even when
// security.WithNonce has not been wired). Both script-src-elem and
// style-src-elem also receive the nonce when those slices were
// non-empty in the source policy — keeping the per-element forms in
// sync with their parent directives.
func (p *Policy) WithNonce(nonce string) *Policy {
	if p == nil {
		return (&Policy{}).WithNonce(nonce)
	}
	c := p.Clone()
	if nonce == "" {
		return c
	}
	n := Nonce(nonce)
	if len(c.ScriptSrc) > 0 {
		c.ScriptSrc = append(c.ScriptSrc, n)
	}
	if len(c.ScriptSrcElem) > 0 {
		c.ScriptSrcElem = append(c.ScriptSrcElem, n)
	}
	if len(c.StyleSrc) > 0 {
		c.StyleSrc = append(c.StyleSrc, n)
	}
	if len(c.StyleSrcElem) > 0 {
		c.StyleSrcElem = append(c.StyleSrcElem, n)
	}
	return c
}

// String serializes the policy into a single CSP header value. The
// canonical CSP3 directive order is used so test assertions can pin
// the output.
//
// Empty directives are omitted entirely (no trailing "; directive ;"
// artifacts). Within each directive, source expressions are emitted in
// the slice order supplied by the caller — this matters because, for
// example, 'none' is only respected when it stands alone, and policy
// builders may rely on a specific ordering for readability.
//
// Duplicate sources within a single directive are NOT deduplicated:
// callers may legitimately emit a source twice (e.g. 'self' + nonce)
// and the browser tolerates duplicates. Doing dedup here would risk
// silently reordering tokens in a way that breaks 'strict-dynamic'
// semantics.
func (p *Policy) String() string {
	if p == nil {
		return ""
	}
	var b strings.Builder

	writeDirective(&b, "default-src", p.DefaultSrc)
	writeDirective(&b, "script-src", p.ScriptSrc)
	writeDirective(&b, "script-src-elem", p.ScriptSrcElem)
	writeDirective(&b, "style-src", p.StyleSrc)
	writeDirective(&b, "style-src-elem", p.StyleSrcElem)
	writeDirective(&b, "img-src", p.ImgSrc)
	writeDirective(&b, "font-src", p.FontSrc)
	writeDirective(&b, "connect-src", p.ConnectSrc)
	writeDirective(&b, "frame-src", p.FrameSrc)
	writeDirective(&b, "frame-ancestors", p.FrameAncestors)
	writeDirective(&b, "form-action", p.FormAction)
	writeDirective(&b, "base-uri", p.BaseURI)
	writeDirective(&b, "object-src", p.ObjectSrc)
	writeDirective(&b, "media-src", p.MediaSrc)
	writeDirective(&b, "manifest-src", p.ManifestSrc)
	writeDirective(&b, "worker-src", p.WorkerSrc)

	if p.UpgradeInsecureRequests {
		writeBareDirective(&b, "upgrade-insecure-requests")
	}
	if p.BlockAllMixedContent {
		writeBareDirective(&b, "block-all-mixed-content")
	}
	if p.Sandbox != nil {
		if len(p.Sandbox) == 0 {
			writeBareDirective(&b, "sandbox")
		} else {
			writeStringDirective(&b, "sandbox", p.Sandbox)
		}
	}
	if len(p.RequireTrustedTypesFor) > 0 {
		// Spec requires each sink name be single-quoted: 'script'.
		quoted := make([]string, len(p.RequireTrustedTypesFor))
		for i, s := range p.RequireTrustedTypesFor {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if !strings.HasPrefix(s, "'") {
				s = "'" + s + "'"
			}
			quoted[i] = s
		}
		writeStringDirective(&b, "require-trusted-types-for", quoted)
	}
	if len(p.TrustedTypes) > 0 {
		writeStringDirective(&b, "trusted-types", p.TrustedTypes)
	}
	if p.ReportURI != "" {
		writeStringDirective(&b, "report-uri", []string{p.ReportURI})
	}
	if p.ReportTo != "" {
		writeStringDirective(&b, "report-to", []string{p.ReportTo})
	}

	return b.String()
}

// writeDirective appends "name s1 s2 …; " to b when src is non-empty.
func writeDirective(b *strings.Builder, name string, src []SourceExpr) {
	if len(src) == 0 {
		return
	}
	first := true
	startedDirective := false
	for _, s := range src {
		v := s.String()
		if v == "" {
			continue
		}
		if !startedDirective {
			if b.Len() > 0 {
				b.WriteString("; ")
			}
			b.WriteString(name)
			startedDirective = true
			first = true
		}
		if first {
			b.WriteByte(' ')
			first = false
		} else {
			b.WriteByte(' ')
		}
		b.WriteString(v)
	}
}

// writeStringDirective appends "name v1 v2 …; " for raw string values.
// Empty / whitespace-only values are skipped.
func writeStringDirective(b *strings.Builder, name string, values []string) {
	startedDirective := false
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if !startedDirective {
			if b.Len() > 0 {
				b.WriteString("; ")
			}
			b.WriteString(name)
			startedDirective = true
		}
		b.WriteByte(' ')
		b.WriteString(v)
	}
}

// writeBareDirective appends "name" (no values).
func writeBareDirective(b *strings.Builder, name string) {
	if b.Len() > 0 {
		b.WriteString("; ")
	}
	b.WriteString(name)
}

// sortedClone returns a sorted copy of s. Used by tests when the order
// of caller-supplied hosts is unimportant.
func sortedClone(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}
