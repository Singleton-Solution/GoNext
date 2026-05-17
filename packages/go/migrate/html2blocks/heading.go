package html2blocks

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// headingFromNode maps `<h1>..<h6>` to core/heading. The level is taken
// from the tag; the anchor, if any, comes from the `id` attribute.
func headingFromNode(n *html.Node) (Block, bool) {
	content := strings.TrimSpace(textContent(n))
	if content == "" {
		return Block{}, false
	}
	attrs := map[string]any{
		"content": content,
		"level":   headingLevel(n.DataAtom),
	}
	for _, a := range n.Attr {
		if a.Key == "id" && a.Val != "" {
			attrs["anchor"] = a.Val
		}
	}
	return Block{
		Name:  BlockHeading,
		Attrs: attrs,
	}, true
}

// headingLevel returns the numeric heading rank for the given tag atom.
// We map h1..h6 directly; anything else (which the dispatcher already
// filters out) defaults to 2, matching the @gonext/blocks-core default.
func headingLevel(a atom.Atom) int {
	switch a {
	case atom.H1:
		return 1
	case atom.H2:
		return 2
	case atom.H3:
		return 3
	case atom.H4:
		return 4
	case atom.H5:
		return 5
	case atom.H6:
		return 6
	}
	return 2
}
