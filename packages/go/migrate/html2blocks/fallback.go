package html2blocks

import (
	"strings"

	"golang.org/x/net/html"
)

// fallbackBlock wraps an unrecognised element in a core/paragraph block
// whose `content` attribute carries the original HTML verbatim.
//
// There is no `core/html` in the 10 canonical core blocks today; the
// paragraph fallback is the agreed lossy-but-honest landing zone so a
// human can rescue the content from the editor later. The renderer
// already treats paragraph content as opaque, so this round-trips
// cleanly through the import → edit → publish path.
//
// TODO(#170/html-block): once a dedicated `core/html` block is
// registered, route unknown elements there instead of paragraph and
// drop this comment.
func fallbackBlock(n *html.Node) Block {
	raw := renderOuter(n)
	if raw == "" {
		// Empty unknown element — fall back to text content so we
		// don't emit a literal empty paragraph.
		raw = strings.TrimSpace(textContent(n))
	}
	return Block{
		Name: BlockParagraph,
		Attrs: map[string]any{
			"content": raw,
		},
	}
}
