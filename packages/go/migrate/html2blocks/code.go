package html2blocks

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// codeFromNode converts `<pre>` (optionally wrapping `<code>`) to a
// core/code block. The body is captured verbatim — we do not trim
// internal whitespace because indentation is meaningful in code.
//
// Language hints come from the conventional `language-xxx` className on
// the inner `<code>` element, matching the Prism/Shiki/highlight.js
// ecosystem. If multiple classes are present we pick the first that
// starts with `language-`.
func codeFromNode(n *html.Node) (Block, bool) {
	// Locate the inner `<code>` if present. If absent, the pre's own
	// text is the body — pre-Gutenberg WP frequently emitted bare
	// `<pre>` for code samples.
	codeNode := findDescendant(n, atom.Code)
	target := n
	if codeNode != nil {
		target = codeNode
	}

	content := preserveText(target)
	if strings.TrimSpace(content) == "" {
		return Block{}, false
	}

	attrs := map[string]any{
		"content": content,
	}
	if codeNode != nil {
		if lang := languageFromClass(codeNode); lang != "" {
			attrs["language"] = lang
		}
	}
	return Block{
		Name:  BlockCode,
		Attrs: attrs,
	}, true
}

// preserveText is like textContent but does not trim — used by codeFromNode
// to keep leading whitespace and trailing newlines intact.
func preserveText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// languageFromClass returns the trailing identifier of the first
// `language-xxx` className on the node, or "" if none is present.
func languageFromClass(n *html.Node) string {
	for _, a := range n.Attr {
		if a.Key != "class" {
			continue
		}
		for _, tok := range strings.Fields(a.Val) {
			if strings.HasPrefix(tok, "language-") {
				return strings.TrimPrefix(tok, "language-")
			}
		}
	}
	return ""
}
