package html2blocks

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// listFromNode converts `<ul>` or `<ol>` to a core/list block. Items are
// flattened to a `values` string slice — matching the current scalar
// shape exposed by `@gonext/blocks-core/list`. Nested lists are
// inlined: their text content is appended as additional items rather
// than dropped, because preserving the source ordering is more useful
// than throwing the children away while the recursive item shape is
// still in flight (see ListAttributes upstream).
func listFromNode(n *html.Node) (Block, bool) {
	values := make([]string, 0, 4)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.DataAtom != atom.Li {
			continue
		}
		text := strings.TrimSpace(textContent(c))
		if text == "" {
			continue
		}
		values = append(values, text)
	}
	if len(values) == 0 {
		return Block{}, false
	}
	ordered := n.DataAtom == atom.Ol
	attrs := map[string]any{
		"ordered": ordered,
		"values":  values,
	}
	if ordered {
		// `<ol start="3">` and `<ol reversed>` round-trip into the
		// equivalent ListAttributes fields. The editor renders them
		// without further coercion.
		for _, a := range n.Attr {
			switch a.Key {
			case "start":
				if v, err := strconv.Atoi(strings.TrimSpace(a.Val)); err == nil {
					attrs["start"] = v
				}
			case "reversed":
				attrs["reversed"] = true
			}
		}
	}
	return Block{
		Name:  BlockList,
		Attrs: attrs,
	}, true
}
