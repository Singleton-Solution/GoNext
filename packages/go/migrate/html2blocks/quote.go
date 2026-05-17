package html2blocks

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// quoteFromNode converts a `<blockquote>` to a core/quote block.
//
// Two shapes are recognised:
//
//   - Flat: `<blockquote>Some text<cite>Author</cite></blockquote>` — the
//     text becomes the `value` attribute, the optional `<cite>` becomes
//     the `citation` attribute, and InnerBlocks is empty.
//   - Nested: `<blockquote><p>x</p><p>y</p></blockquote>` — each child
//     element is converted to its own block and stored as an
//     InnerBlock. The quote's `value` is then the concatenated text of
//     those paragraphs, which keeps the editor's plain-string surface
//     working until the rich inline model lands.
func quoteFromNode(n *html.Node) (Block, bool) {
	attrs := map[string]any{}
	var citation string
	var inner []Block
	var valueParts []string

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			if t := strings.TrimSpace(c.Data); t != "" {
				valueParts = append(valueParts, t)
			}
		case html.ElementNode:
			if c.DataAtom == atom.Cite {
				if t := strings.TrimSpace(textContent(c)); t != "" {
					citation = t
				}
				continue
			}
			// Convert each non-cite child to its own block. The
			// dispatcher already knows how to handle paragraphs,
			// lists, nested headings, etc., which is exactly what
			// we want for a quote that wraps real markup.
			if b, ok := convertNode(c); ok {
				inner = append(inner, b)
				// Mirror the textual content up into `value` so
				// renderers that still consume the scalar string
				// stay readable.
				if content, ok := b.Attrs["content"].(string); ok && content != "" {
					valueParts = append(valueParts, content)
				}
			}
		}
	}

	if len(inner) == 0 && len(valueParts) == 0 && citation == "" {
		return Block{}, false
	}
	attrs["value"] = strings.Join(valueParts, "\n")
	if citation != "" {
		attrs["citation"] = citation
	}
	return Block{
		Name:        BlockQuote,
		Attrs:       attrs,
		InnerBlocks: inner,
	}, true
}
