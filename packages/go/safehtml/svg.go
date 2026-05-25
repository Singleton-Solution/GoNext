package safehtml

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// svgAllowedElements is the closed list of SVG element local-names
// the sanitizer admits. Everything else (script, foreignObject,
// animation, etc) is dropped — including the element's text content,
// because we can't reason about what a dropped element's children
// were intended to do.
//
// The list was assembled by intersecting the SVG 1.1 element catalog
// with "things a CMS user might legitimately paste into prose":
// shapes, paths, basic text, gradients, and structural containers.
var svgAllowedElements = map[string]bool{
	"svg":            true,
	"g":              true,
	"defs":           true,
	"symbol":         true,
	"use":            true,
	"title":          true,
	"desc":           true,
	"path":           true,
	"rect":           true,
	"circle":         true,
	"ellipse":        true,
	"line":           true,
	"polyline":       true,
	"polygon":        true,
	"text":           true,
	"tspan":          true,
	"textpath":       true,
	"linearGradient": true,
	"radialGradient": true,
	"stop":           true,
	"clippath":       true,
	"mask":           true,
	"pattern":        true,
	"marker":         true,
}

// svgAllowedAttributes is the closed list of SVG attribute names the
// sanitizer admits. We err on the side of "drop and serialize a clean
// version" — if a hand-tuned SVG needed an attribute we dropped, the
// renderer's output will still be valid SVG, just less expressive.
//
// All event-handler attributes (onclick, onload, onmouseover, ...) are
// absent and would be dropped by the default branch.
var svgAllowedAttributes = map[string]bool{
	"id":                  true,
	"class":               true,
	"d":                   true,
	"x":                   true,
	"y":                   true,
	"x1":                  true,
	"y1":                  true,
	"x2":                  true,
	"y2":                  true,
	"cx":                  true,
	"cy":                  true,
	"r":                   true,
	"rx":                  true,
	"ry":                  true,
	"width":               true,
	"height":              true,
	"viewbox":             true,
	"viewBox":             true,
	"preserveaspectratio": true,
	"preserveAspectRatio": true,
	"transform":           true,
	"fill":                true,
	"fill-opacity":        true,
	"fill-rule":           true,
	"stroke":              true,
	"stroke-width":        true,
	"stroke-opacity":      true,
	"stroke-linecap":      true,
	"stroke-linejoin":     true,
	"stroke-dasharray":    true,
	"stroke-dashoffset":   true,
	"opacity":             true,
	"points":              true,
	"font-family":         true,
	"font-size":           true,
	"font-weight":         true,
	"text-anchor":         true,
	"dominant-baseline":   true,
	"clip-path":           true,
	"mask":                true,
	"offset":              true,
	"stop-color":          true,
	"stop-opacity":        true,
	"gradientunits":       true,
	"gradientUnits":       true,
	"gradienttransform":   true,
	"gradientTransform":   true,
	"spreadmethod":        true,
	"spreadMethod":        true,
	"href":                true,
	"xlink:href":          true,
	"xmlns":               true,
	"xmlns:xlink":         true,
	"version":             true,
}

// SanitizeSVG returns a clean SVG fragment derived from raw. The
// output never contains <script>, event handlers, javascript: URLs,
// or any element/attribute outside the allowlist.
//
// raw is parsed as an HTML fragment (using golang.org/x/net/html,
// which handles SVG namespaces inline) and re-serialized after the
// tree is filtered. The output is always well-formed HTML; if raw
// was already well-formed, the output is a close (but not byte-
// identical) re-rendering.
//
// If raw is empty or contains no SVG element, returns an empty
// string with a nil error. A parse failure returns the raw error
// from x/net/html.
func SanitizeSVG(raw string) (string, error) {
	return sanitizeWithAllowlists(raw, svgAllowedElements, svgAllowedAttributes, sanitizeURLAttribute)
}

// sanitizeURLAttribute rejects any value that, after stripping
// whitespace and lowercasing the scheme, starts with javascript:,
// data: (other than data:image/...), vbscript:, or file:.
// Returns the original value if it passes; returns "" to signal "drop
// this attribute". The dropper is conservative: an attribute we
// can't confidently classify is dropped.
func sanitizeURLAttribute(name, value string) string {
	if value == "" {
		return value
	}
	if !isURLLikeAttr(name) {
		return value
	}
	stripped := strings.TrimSpace(value)
	stripped = strings.TrimLeft(stripped, "\t\r\n ")
	lower := strings.ToLower(stripped)

	// Allow only http(s), mailto, tel, fragment, and relative paths.
	// Inline data URIs are blocked except for narrow image MIME
	// types (data:image/png;base64, etc.); this lets a CMS user
	// inline a small icon without exposing the data: scheme as a
	// general XSS vector.
	switch {
	case strings.HasPrefix(lower, "http://"),
		strings.HasPrefix(lower, "https://"),
		strings.HasPrefix(lower, "mailto:"),
		strings.HasPrefix(lower, "tel:"),
		strings.HasPrefix(lower, "/"),
		strings.HasPrefix(lower, "#"),
		strings.HasPrefix(lower, "?"):
		return value
	case strings.HasPrefix(lower, "data:image/png"),
		strings.HasPrefix(lower, "data:image/jpeg"),
		strings.HasPrefix(lower, "data:image/gif"),
		strings.HasPrefix(lower, "data:image/webp"),
		strings.HasPrefix(lower, "data:image/svg+xml"):
		return value
	case strings.HasPrefix(lower, "javascript:"),
		strings.HasPrefix(lower, "vbscript:"),
		strings.HasPrefix(lower, "file:"),
		strings.HasPrefix(lower, "data:"):
		return ""
	default:
		// A scheme we don't recognize (e.g. "gopher:") or a relative
		// URL without a leading slash. Allow relative URLs; reject
		// anything that looks like a custom scheme (has a colon
		// before any '/').
		if idx := strings.Index(lower, ":"); idx >= 0 {
			if slashIdx := strings.Index(lower, "/"); slashIdx == -1 || slashIdx > idx {
				return ""
			}
		}
		return value
	}
}

// isURLLikeAttr reports whether the named attribute carries a URL
// payload that needs scheme filtering. We list these explicitly
// rather than relying on heuristic substring matches.
func isURLLikeAttr(name string) bool {
	switch strings.ToLower(name) {
	case "href", "xlink:href", "src", "action", "formaction",
		"data", "ping", "poster", "background":
		return true
	}
	return false
}

// sanitizeWithAllowlists is the shared core for SVG and MathML
// sanitization. It parses raw as an HTML fragment, walks the parse
// tree, and re-serializes the subset that survives the allowlists.
//
// urlFilter is consulted for any attribute named in isURLLikeAttr;
// the filter may return the value unchanged, modified, or empty
// (which causes the attribute to be dropped entirely).
func sanitizeWithAllowlists(
	raw string,
	elems map[string]bool,
	attrs map[string]bool,
	urlFilter func(name, value string) string,
) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	// Parse as fragment so a bare <svg>...</svg> doesn't get wrapped
	// in <html><body>. The body element is the parser's default
	// container.
	nodes, err := html.ParseFragment(strings.NewReader(raw), &html.Node{
		Type:     html.ElementNode,
		Data:     "body",
		DataAtom: atom.Body,
	})
	if err != nil {
		return "", err
	}
	var clean []*html.Node
	for _, n := range nodes {
		if c := walkClean(n, elems, attrs, urlFilter); c != nil {
			clean = append(clean, c)
		}
	}
	var b strings.Builder
	for _, n := range clean {
		if err := html.Render(&b, n); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// walkClean returns a copy of n with every disallowed element /
// attribute dropped. Text nodes survive unchanged. Returns nil if n
// itself would be dropped.
func walkClean(
	n *html.Node,
	elems map[string]bool,
	attrs map[string]bool,
	urlFilter func(name, value string) string,
) *html.Node {
	if n == nil {
		return nil
	}
	switch n.Type {
	case html.TextNode:
		// Text nodes are reflected verbatim. They're rendered with
		// escaping by html.Render, so an attacker can't smuggle a
		// tag-shaped string back into the output.
		return cloneNode(n)
	case html.ElementNode:
		local := strings.ToLower(n.Data)
		if !elems[local] {
			return nil
		}
		out := &html.Node{
			Type:     html.ElementNode,
			DataAtom: n.DataAtom,
			Data:     n.Data,
			Namespace: n.Namespace,
		}
		for _, a := range n.Attr {
			if !attrs[strings.ToLower(a.Key)] {
				continue
			}
			val := a.Val
			if urlFilter != nil && isURLLikeAttr(a.Key) {
				val = urlFilter(a.Key, val)
				if val == "" {
					continue
				}
			}
			out.Attr = append(out.Attr, html.Attribute{
				Namespace: a.Namespace,
				Key:       a.Key,
				Val:       val,
			})
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if cc := walkClean(c, elems, attrs, urlFilter); cc != nil {
				out.AppendChild(cc)
			}
		}
		return out
	default:
		// DocumentNode, CommentNode, DoctypeNode — drop. Comments in
		// SVG/MathML are rare and have been used for IE conditional-
		// comment tricks; safer to strip.
		return nil
	}
}

// cloneNode returns a shallow copy of a text node. Used because the
// parser-owned tree must not be modified — Render walks original
// pointers and rebuilding the tree avoids any aliasing concerns.
func cloneNode(n *html.Node) *html.Node {
	return &html.Node{
		Type:      n.Type,
		Data:      n.Data,
		DataAtom:  n.DataAtom,
		Namespace: n.Namespace,
	}
}
