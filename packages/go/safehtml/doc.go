// Package safehtml provides HTML sanitization for three element
// families that need their own dedicated allowlists rather than the
// generic prose sanitizer used elsewhere:
//
//   - SVG: vector graphics; allowed in user content for diagrams
//     and decorative artwork but a notorious XSS vector via the
//     <script> child element, the various on* event handlers, and
//     javascript: URLs in <a>/<image>.
//
//   - MathML: mathematical notation; smaller attack surface than SVG
//     but still subject to event-handler attributes and the same
//     javascript: URL risks.
//
//   - Iframe: only allowed for explicit embed blocks; must carry a
//     sandbox attribute, must point at an https:// URL, and must
//     never carry srcdoc (which would let an attacker inline
//     arbitrary HTML).
//
// The package's design follows the principle of "allowlist what we
// can name, drop everything else". No regular expressions; no
// best-effort parsing — we use golang.org/x/net/html to tokenize and
// walk the input, build a clean tree, and serialize it back. That
// means malformed markup is normalized (always a closing tag for
// every opener), and any HTML construct we didn't explicitly allow
// is simply absent from the output.
//
// Surface:
//
//	clean, err := safehtml.SanitizeSVG(raw)
//	clean, err := safehtml.SanitizeMathML(raw)
//	clean, err := safehtml.SanitizeIframe(rawHTML, safehtml.IframeOptions{
//	    AllowedHosts: []string{"www.youtube.com", "player.vimeo.com"},
//	})
//
// Sanitization is best-effort: a token the package can't parse is
// dropped rather than reflected. The output is intended to be safe
// to inject into a rendered HTML document without further escaping.
package safehtml
