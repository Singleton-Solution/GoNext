package html2blocks

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// imageFromNode converts a bare `<img>` to a core/image block.
func imageFromNode(n *html.Node) (Block, bool) {
	attrs := imageAttrsFromImg(n)
	if attrs["url"] == "" {
		// An img without a src is useless — drop it.
		return Block{}, false
	}
	return Block{Name: BlockImage, Attrs: attrs}, true
}

// imageFromFigure handles `<figure><img/></figure>` (and `<figure><a><img/></a></figure>`)
// — the canonical Gutenberg shape. The optional `<figcaption>` becomes
// the `caption` attribute.
func imageFromFigure(n *html.Node) (Block, bool) {
	img := findDescendant(n, atom.Img)
	if img == nil {
		// A figure without an image (e.g. a video figure) is treated as
		// unknown HTML and routed to the fallback. Returning false would
		// silently drop it.
		return fallbackBlock(n), true
	}
	attrs := imageAttrsFromImg(img)
	if attrs["url"] == "" {
		return fallbackBlock(n), true
	}

	// If the image sits inside an `<a>`, capture the href so we can
	// round-trip the link wrapping that core/image supports.
	if a := findAnchorAncestor(n, img); a != nil {
		for _, at := range a.Attr {
			if at.Key == "href" && at.Val != "" {
				attrs["href"] = at.Val
			}
		}
	}

	// `<figcaption>` becomes the caption. We pull text content rather
	// than inner HTML because the current ImageAttributes.caption is
	// a plain string — rich runs land with the inline model upgrade.
	if cap := findDescendant(n, atom.Figcaption); cap != nil {
		if text := strings.TrimSpace(textContent(cap)); text != "" {
			attrs["caption"] = text
		}
	}

	// Figure-level alignment, when WP emitted it as a class.
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, want := range []string{"left", "right", "center", "wide", "full"} {
				if strings.Contains(a.Val, "align"+want) {
					attrs["align"] = want
				}
			}
		}
	}

	return Block{Name: BlockImage, Attrs: attrs}, true
}

// imageAttrsFromImg pulls the standard attributes off an `<img>` node.
// Shared between the bare-`<img>` and figure-wrapped paths.
func imageAttrsFromImg(img *html.Node) map[string]any {
	attrs := map[string]any{
		"url": "",
		"alt": "",
	}
	for _, a := range img.Attr {
		switch a.Key {
		case "src":
			attrs["url"] = a.Val
		case "alt":
			attrs["alt"] = a.Val
		case "width":
			if v, err := strconv.Atoi(strings.TrimSpace(a.Val)); err == nil {
				attrs["width"] = v
			}
		case "height":
			if v, err := strconv.Atoi(strings.TrimSpace(a.Val)); err == nil {
				attrs["height"] = v
			}
		}
	}
	return attrs
}

// findDescendant returns the first descendant of n whose tag matches t.
// Depth-first, document-order. Returns nil when nothing matches.
func findDescendant(n *html.Node, t atom.Atom) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == t {
			return c
		}
		if found := findDescendant(c, t); found != nil {
			return found
		}
	}
	return nil
}

// findAnchorAncestor walks up from `child` looking for the nearest <a>
// ancestor that is still a descendant of `root`. Returns nil when the
// image isn't wrapped in a link.
func findAnchorAncestor(root, child *html.Node) *html.Node {
	for p := child.Parent; p != nil && p != root.Parent; p = p.Parent {
		if p.Type == html.ElementNode && p.DataAtom == atom.A {
			return p
		}
	}
	return nil
}
