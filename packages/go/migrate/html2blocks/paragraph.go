package html2blocks

import (
	"strings"

	"golang.org/x/net/html"
)

// paragraphFromNode converts a `<p>` element to a core/paragraph block.
// Returns ok=false for whitespace-only paragraphs (`<p> </p>`, `<p></p>`)
// so we don't pollute the tree with empty blocks. WordPress emits a lot
// of these from the visual editor's spacing handling.
func paragraphFromNode(n *html.Node) (Block, bool) {
	content := textContent(n)
	if strings.TrimSpace(content) == "" {
		return Block{}, false
	}
	attrs := map[string]any{
		"content": strings.TrimSpace(content),
	}
	// Pick up `align=...` from style="text-align: x" or class="has-text-align-x"
	// when WordPress emitted them. We don't deep-parse arbitrary CSS — only
	// the two shapes WP itself produces.
	if align := paragraphAlign(n); align != "" {
		attrs["align"] = align
	}
	return Block{
		Name:  BlockParagraph,
		Attrs: attrs,
	}, true
}

// paragraphAlign extracts an alignment from a paragraph element. WP
// historically used either an inline `style="text-align: center"` or a
// `has-text-align-center` className; we accept both.
func paragraphAlign(n *html.Node) string {
	for _, a := range n.Attr {
		switch a.Key {
		case "style":
			lower := strings.ToLower(a.Val)
			for _, want := range []string{"left", "center", "right"} {
				if strings.Contains(lower, "text-align: "+want) ||
					strings.Contains(lower, "text-align:"+want) {
					return want
				}
			}
		case "class":
			for _, want := range []string{"left", "center", "right"} {
				if strings.Contains(a.Val, "has-text-align-"+want) {
					return want
				}
			}
		}
	}
	return ""
}
