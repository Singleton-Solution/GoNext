package html2blocks

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// gutenbergOpen matches the opening Gutenberg block comment, capturing
// the block name and the optional JSON attribute payload.
//
// Examples it should match:
//
//	<!-- wp:paragraph -->
//	<!-- wp:image {"id":42,"sizeSlug":"large"} -->
//	<!-- wp:core/heading {"level":3} -->
//	<!-- wp:my-plugin/special-block /-->   (void form)
//
// We accept the void self-closing form (`/-->`) for blocks that have no
// inner content (separator, spacer, void images in some plugins).
var gutenbergOpen = regexp.MustCompile(
	`<!--\s*wp:([a-z0-9_/-]+)(?:\s+(\{.*?\}))?\s*(/)?-->`,
)

// gutenbergComment is a relaxed form used to scan whether *any* block
// comment is present. If no match, we never enter the comment-aware
// path and the DOM walker runs.
var gutenbergComment = regexp.MustCompile(`<!--\s*wp:[a-z0-9_/-]+`)

// parseGutenbergComments interprets the original input bytes as a
// Gutenberg comment stream. Returns ok=false when no `wp:` comment is
// present, in which case the caller falls back to the DOM walker.
//
// The implementation slides over the bytes with regex/substring
// matching rather than walking the DOM. Comment delimiters behave
// like a tape — the DOM tree between them is content. Walking the
// DOM and trying to thread the comments through siblings is fragile
// because `golang.org/x/net/html` will silently move leading
// `<!-- … -->` comments outside the synthesised `<html>` element.
// Sticking with the byte stream matches how the WP upstream parser
// is written and avoids those re-parenting surprises.
func parseGutenbergComments(htmlBytes []byte) ([]Block, bool) {
	raw := string(htmlBytes)
	if !gutenbergComment.MatchString(raw) {
		return nil, false
	}
	blocks, _ := parseBlockStream(raw)
	if len(blocks) == 0 {
		// We saw a `wp:` token but couldn't form a single block — fall
		// through to the DOM walker rather than emit an empty tree.
		return nil, false
	}
	return blocks, true
}

// parseBlockStream walks `raw` and returns the top-level blocks plus
// the number of bytes consumed. Recursive — nested blocks are parsed
// as InnerBlocks of their parent.
func parseBlockStream(raw string) ([]Block, int) {
	var out []Block
	i := 0
	for i < len(raw) {
		loc := gutenbergOpen.FindStringSubmatchIndex(raw[i:])
		if loc == nil {
			// No more block markers; leftover bytes are stray HTML
			// outside any block. WP's own parser drops these silently
			// (they're typically whitespace between blocks); we do
			// the same.
			break
		}
		// Adjust the indices to be absolute within `raw`.
		for k := range loc {
			if loc[k] >= 0 {
				loc[k] += i
			}
		}
		name := raw[loc[2]:loc[3]]
		var attrsJSON string
		if loc[4] >= 0 {
			attrsJSON = raw[loc[4]:loc[5]]
		}
		voidForm := loc[6] >= 0
		openEnd := loc[1]

		blockName := normaliseBlockName(name)

		if voidForm {
			out = append(out, finaliseBlock(blockName, attrsJSON, "", nil))
			i = openEnd
			continue
		}

		// Find the matching closing comment, accounting for nesting.
		bodyStart := openEnd
		bodyEnd, closeEnd := findMatchingClose(raw, name, bodyStart)
		if bodyEnd < 0 {
			// Unclosed block — treat the remainder as content of the
			// open block, which mirrors the WP parser's behaviour
			// when the editor saved a malformed post.
			bodyEnd = len(raw)
			closeEnd = len(raw)
		}
		inner := raw[bodyStart:bodyEnd]

		// Recurse for nested blocks. If none are found, `innerBlocks`
		// stays empty and `inner` is treated as inner HTML.
		nested, _ := parseBlockStream(inner)
		out = append(out, finaliseBlock(blockName, attrsJSON, inner, nested))

		i = closeEnd
	}
	return out, i
}

// findMatchingClose returns (bodyEnd, closeEnd) where bodyEnd is the
// index of the start of the closing comment for `name` and closeEnd is
// the index just past it. Returns (-1, -1) when no matching close is
// found. Handles nested blocks of the same name by depth counting.
func findMatchingClose(raw, name string, from int) (int, int) {
	depth := 1
	pos := from
	openTok := []byte("wp:" + name)
	closeTok := []byte("/wp:" + name)
	for pos < len(raw) {
		// Look for the next `<!--` comment header.
		hdr := strings.Index(raw[pos:], "<!--")
		if hdr < 0 {
			return -1, -1
		}
		commentStart := pos + hdr
		commentEnd := strings.Index(raw[commentStart:], "-->")
		if commentEnd < 0 {
			return -1, -1
		}
		commentBody := raw[commentStart : commentStart+commentEnd+3]
		switch {
		case bytes.Contains([]byte(commentBody), closeTok):
			depth--
			if depth == 0 {
				return commentStart, commentStart + commentEnd + 3
			}
		case bytes.Contains([]byte(commentBody), openTok):
			// Nested open of the same block name. Make sure we're not
			// confusing `wp:paragraph` with the closing `/wp:paragraph`
			// — `closeTok` already covers that ambiguity above so this
			// branch is safe.
			//
			// Also check for void form which doesn't increment depth.
			if !strings.Contains(commentBody, "/-->") {
				depth++
			}
		}
		pos = commentStart + commentEnd + 3
	}
	return -1, -1
}

// finaliseBlock assembles a Block from the components extracted by the
// comment-stream parser. When `nested` is non-empty the block is
// treated as a container; otherwise `innerHTML` is parsed as a fragment
// and its top-level node (if any) is consulted for extra attributes
// (e.g. the `<img>` inside `<!-- wp:image -->`).
func finaliseBlock(name, attrsJSON, innerHTML string, nested []Block) Block {
	attrs := map[string]any{}
	if attrsJSON != "" {
		_ = json.Unmarshal([]byte(attrsJSON), &attrs)
	}

	switch name {
	case BlockParagraph:
		fillFromInner(attrs, innerHTML, "content", false)
	case BlockHeading:
		fillFromInner(attrs, innerHTML, "content", false)
		if _, ok := attrs["level"]; !ok {
			// The level usually rides on the comment's JSON. If
			// missing, infer from the tag inside innerHTML.
			if lvl := inferHeadingLevel(innerHTML); lvl > 0 {
				attrs["level"] = lvl
			}
		}
	case BlockImage:
		hydrateImageAttrs(attrs, innerHTML)
	case BlockQuote:
		// Quotes can carry either inline content or nested blocks.
		// If the comment delimiters wrapped real blocks we keep them
		// as InnerBlocks; the `value` mirror is filled from the
		// concatenated text content.
		fillFromInner(attrs, innerHTML, "value", true)
	case BlockCode:
		// Code's inner HTML is the verbatim source. Strip the
		// `<pre><code>` wrappers if present.
		attrs["content"] = unwrapCodeContent(innerHTML)
	case BlockList:
		hydrateListAttrs(attrs, innerHTML)
	}

	return Block{
		Name:        name,
		Attrs:       attrs,
		InnerBlocks: nested,
	}
}

// normaliseBlockName ensures the `core/` namespace prefix is present.
// WordPress allows the `core/` prefix to be elided (e.g. `wp:paragraph`
// rather than `wp:core/paragraph`); the registry expects the long form.
func normaliseBlockName(name string) string {
	if strings.Contains(name, "/") {
		return name
	}
	return "core/" + name
}

// fillFromInner sets `attrs[key]` to a sensible textual representation
// of `innerHTML`. When `keepHTML` is true we strip the outer tag (e.g.
// the wrapping `<blockquote>`) but keep nested markup; otherwise we
// fall back to text content. Used by paragraph/heading/quote so the
// editor's plain-string fields are populated.
func fillFromInner(attrs map[string]any, innerHTML, key string, keepHTML bool) {
	if _, ok := attrs[key]; ok {
		return
	}
	frag, err := parseFragment(innerHTML)
	if err != nil || frag == nil {
		attrs[key] = strings.TrimSpace(innerHTML)
		return
	}
	if keepHTML {
		// Use the inner of the first element child as the value, so
		// `<blockquote><p>x</p></blockquote>` collapses to `<p>x</p>`.
		if first := firstElementChild(frag); first != nil {
			attrs[key] = renderInner(first)
			return
		}
	}
	attrs[key] = strings.TrimSpace(textContent(frag))
}

// hydrateImageAttrs reads the `<img>` from the inner HTML and folds its
// attributes (src, alt, dimensions) into the comment-level attrs map.
// The comment's `{"id":42}` typically holds the media id only; the
// rest comes from the figure.
func hydrateImageAttrs(attrs map[string]any, innerHTML string) {
	frag, err := parseFragment(innerHTML)
	if err != nil || frag == nil {
		return
	}
	img := findDescendant(frag, atom.Img)
	if img == nil {
		return
	}
	imgAttrs := imageAttrsFromImg(img)
	for k, v := range imgAttrs {
		if _, ok := attrs[k]; !ok {
			attrs[k] = v
		}
	}
}

// hydrateListAttrs fills in `ordered` + `values` from the inner HTML so
// that `<!-- wp:list --><ul>…</ul><!-- /wp:list -->` ends up with the
// same shape as the DOM-walker path.
func hydrateListAttrs(attrs map[string]any, innerHTML string) {
	frag, err := parseFragment(innerHTML)
	if err != nil || frag == nil {
		return
	}
	list := findDescendant(frag, atom.Ul)
	ordered := false
	if list == nil {
		list = findDescendant(frag, atom.Ol)
		ordered = list != nil
	}
	if list == nil {
		return
	}
	if _, ok := attrs["ordered"]; !ok {
		attrs["ordered"] = ordered
	}
	if _, ok := attrs["values"]; !ok {
		b, _ := listFromNode(list)
		if v, ok2 := b.Attrs["values"]; ok2 {
			attrs["values"] = v
		}
	}
}

// inferHeadingLevel returns the heading rank of the first heading
// element inside innerHTML. Used when the wp: comment didn't include a
// `{"level":N}` JSON attribute.
func inferHeadingLevel(innerHTML string) int {
	for tag, lvl := range map[string]int{
		"<h1": 1, "<h2": 2, "<h3": 3, "<h4": 4, "<h5": 5, "<h6": 6,
	} {
		if strings.Contains(innerHTML, tag) {
			return lvl
		}
	}
	return 0
}

// unwrapCodeContent peels the `<pre><code>...</code></pre>` shell off a
// Gutenberg code block's inner HTML, decoding entities along the way.
func unwrapCodeContent(innerHTML string) string {
	frag, err := parseFragment(innerHTML)
	if err != nil || frag == nil {
		return innerHTML
	}
	if pre := findDescendant(frag, atom.Pre); pre != nil {
		if code := findDescendant(pre, atom.Code); code != nil {
			return preserveText(code)
		}
		return preserveText(pre)
	}
	return strings.TrimSpace(textContent(frag))
}

// parseFragment parses a fragment of HTML using the same body context
// as Convert so we get the body tree back.
func parseFragment(s string) (*html.Node, error) {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return nil, err
	}
	return findBody(doc), nil
}

// firstElementChild returns the first ElementNode child of n, skipping
// stray text nodes.
func firstElementChild(n *html.Node) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			return c
		}
	}
	return nil
}
