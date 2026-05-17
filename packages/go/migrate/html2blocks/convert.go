package html2blocks

import (
	"bytes"
	"fmt"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Convert parses htmlBytes as an HTML fragment and emits a flat list of
// top-level blocks. The walker first looks for Gutenberg-style
// `<!-- wp:foo --> ... <!-- /wp:foo -->` comment delimiters and uses
// them as the authoritative block boundary when present. If no
// delimiters are found, it falls back to a tag-by-tag DOM walk that
// covers classic Gutenberg and pre-Gutenberg WordPress content.
//
// The function never returns a nil slice on success — an empty input
// yields an empty (non-nil) slice. Errors are reserved for genuine HTML
// parser failures, which in practice the tolerant html package almost
// never produces; we still surface them so callers can log the offending
// post id.
func Convert(htmlBytes []byte) ([]Block, error) {
	// `html.Parse` always wraps the input in `<html><head/><body>…`
	// even when the input is a bare fragment. We parse, then locate
	// the synthetic `<body>` so the walker operates on the real
	// content. `html.ParseFragment` would also work but forces us to
	// hand-pick a context node; using Parse keeps the path simple.
	doc, err := html.Parse(bytes.NewReader(htmlBytes))
	if err != nil {
		return nil, fmt.Errorf("html2blocks: parse: %w", err)
	}
	body := findBody(doc)
	if body == nil {
		// Defensive: html.Parse always synthesises a body, so this is
		// theoretically unreachable. Return an empty slice rather than
		// nil so JSON callers don't have to special-case it.
		return []Block{}, nil
	}

	// First pass: try the Gutenberg comment-delimiter route. We pass
	// the original bytes rather than re-render the body because the
	// `golang.org/x/net/html` parser moves leading comments outside
	// the synthesised `<html>` element, which scrambles ordering. If
	// it finds at least one delimiter pair it owns the entire body —
	// we trust the editor's persisted shape over our DOM heuristic.
	if blocks, ok := parseGutenbergComments(htmlBytes); ok {
		return blocks, nil
	}

	// Fall back to the tag-by-tag walker. Each direct child of <body>
	// becomes (at most) one top-level block. Whitespace-only text
	// nodes are skipped silently so we don't emit empty paragraphs
	// between tags.
	out := make([]Block, 0, 8)
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if block, ok := convertNode(c); ok {
			out = append(out, block)
		}
	}
	return out, nil
}

// convertNode dispatches a single DOM node to its tag-specific handler.
// Returning ok=false means the node produced nothing (whitespace-only
// text, an empty paragraph, a comment, etc.) and should be skipped.
func convertNode(n *html.Node) (Block, bool) {
	switch n.Type {
	case html.TextNode:
		// Stray text at the body level. WordPress emits a lot of
		// loose newlines between top-level paragraphs; treating them
		// as paragraphs would double the block count for every post.
		if strings.TrimSpace(n.Data) == "" {
			return Block{}, false
		}
		return Block{
			Name: BlockParagraph,
			Attrs: map[string]any{
				"content": strings.TrimSpace(n.Data),
			},
		}, true

	case html.ElementNode:
		switch n.DataAtom {
		case atom.P:
			return paragraphFromNode(n)
		case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
			return headingFromNode(n)
		case atom.Ul, atom.Ol:
			return listFromNode(n)
		case atom.Img:
			return imageFromNode(n)
		case atom.Figure:
			// Bare `<figure><img/></figure>` (no surrounding wp: comment)
			// — pull the image out and treat the figure as an image
			// block. Caption inside `<figcaption>` is captured.
			return imageFromFigure(n)
		case atom.Blockquote:
			return quoteFromNode(n)
		case atom.Pre:
			return codeFromNode(n)
		case atom.Hr:
			return separatorBlock(), true
		default:
			return fallbackBlock(n), true
		}

	case html.CommentNode:
		// Stray comments outside a wp: delimiter pair are ignored.
		// The Gutenberg-comment path handles real block markers.
		return Block{}, false
	}
	return Block{}, false
}

// findBody walks down a parsed document to locate the `<body>` element.
// Returns nil only if the parser somehow produced a document without one,
// which shouldn't happen in practice but keeps us defensive.
func findBody(doc *html.Node) *html.Node {
	for n := doc.FirstChild; n != nil; n = n.NextSibling {
		if n.Type == html.ElementNode && n.DataAtom == atom.Html {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && c.DataAtom == atom.Body {
					return c
				}
			}
		}
	}
	return nil
}

// renderInner serialises every child of `n` as HTML and returns the
// concatenation. Used when a block attribute wants the original inner
// markup verbatim (the paragraph fallback, the html-inside-quote case).
func renderInner(n *html.Node) string {
	var buf bytes.Buffer
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&buf, c)
	}
	return strings.TrimSpace(buf.String())
}

// textContent returns the concatenated text of every descendant of n.
// Used for leaf blocks (heading, paragraph) where we only need a string.
func textContent(n *html.Node) string {
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

// renderOuter serialises a single node (including its tag) as HTML. Used
// by the fallback for unknown elements so the raw bytes survive a round
// trip through the importer.
func renderOuter(n *html.Node) string {
	var buf bytes.Buffer
	_ = html.Render(&buf, n)
	return strings.TrimSpace(buf.String())
}
