package render

import (
	"fmt"
	"html/template"
	"strings"
)

// RegisterCoreBlocks registers the renderers for the sixteen core
// block types onto reg. Order matches the inserter order in
// packages/ts/blocks-core/src/index.ts::CORE_BLOCKS so a
// dump of Registry.Names lines up with the editor side's listing.
//
// Returns the first registration error encountered, which by
// construction can only be ErrDuplicateBlockType: a freshly-built
// registry seeded with RegisterCoreBlocks never fails. Callers that
// want a panic-on-error semantics (init() chains) can wrap with
// MustRegisterCoreBlocks below.
//
// Each renderer mirrors the matching `serverRender` hint in
// packages/ts/blocks-core/src/*/save.ts, byte-for-byte for the
// fixed-string cases. The TS save tests are the source of truth for
// the exact HTML shape; the Go renderer follows.
func RegisterCoreBlocks(reg *Registry) error {
	entries := []struct {
		name string
		spec BlockSpec
	}{
		{"core/paragraph", BlockSpec{Render: renderParagraph}},
		{"core/heading", BlockSpec{Render: renderHeading}},
		{"core/list", BlockSpec{Render: renderList}},
		{"core/image", BlockSpec{Render: renderImage}},
		{"core/quote", BlockSpec{Render: renderQuote}},
		{"core/code", BlockSpec{Render: renderCode}},
		{"core/separator", BlockSpec{Render: renderSeparator}},
		{"core/spacer", BlockSpec{Render: renderSpacer}},
		{"core/columns", BlockSpec{Render: renderColumns}},
		{"core/group", BlockSpec{Render: renderGroup}},
		{"core/table", BlockSpec{Render: renderTable}},
		{"core/gallery", BlockSpec{Render: renderGallery}},
		{"core/video", BlockSpec{Render: renderVideo}},
		{"core/button", BlockSpec{Render: renderButton}},
		{"core/file", BlockSpec{Render: renderFile}},
		{"core/embed", BlockSpec{Render: renderEmbed}},
	}
	for _, e := range entries {
		if err := reg.Register(e.name, e.spec); err != nil {
			return err
		}
	}
	return nil
}

// MustRegisterCoreBlocks is RegisterCoreBlocks with a panic-on-error
// semantics. Useful in package init() chains.
func MustRegisterCoreBlocks(reg *Registry) {
	if err := RegisterCoreBlocks(reg); err != nil {
		panic(err)
	}
}

// ---------------------------------------------------------------------
// Per-block renderers. Each mirrors the matching `save()` /
// `serverRender()` hint in packages/ts/blocks-core/src/*/save.ts.
// ---------------------------------------------------------------------

// renderParagraph emits the canonical `<p>` form for core/paragraph.
// Mirrors packages/ts/blocks-core/src/paragraph/save.ts.
func renderParagraph(block Block, _ template.HTML, _ Context) (template.HTML, error) {
	attrs := block.Attributes
	classes := []string{"gn-block-paragraph"}
	if a := attrString(attrs, "align", ""); a != "" {
		classes = append(classes, "has-text-align-"+a)
	}
	if attrBool(attrs, "dropCap", false) {
		classes = append(classes, "has-drop-cap")
	}
	content := escapeText(attrString(attrs, "content", ""))
	return template.HTML(fmt.Sprintf("<p%s>%s</p>", classAttr(classes), content)), nil
}

// renderHeading emits <hN> for core/heading. Level defaults to 2,
// matching the TS spec. Anchor is rendered as the id attribute.
func renderHeading(block Block, _ template.HTML, _ Context) (template.HTML, error) {
	attrs := block.Attributes
	level := attrInt(attrs, "level", 2)
	if level < 1 || level > 6 {
		// Defensive: the validator should have rejected this; we
		// degrade rather than panic.
		level = 2
	}
	classes := []string{"gn-block-heading", fmt.Sprintf("gn-block-heading--level-%d", level)}
	if a := attrString(attrs, "align", ""); a != "" {
		classes = append(classes, "has-text-align-"+a)
	}
	tag := fmt.Sprintf("h%d", level)
	id := idAttr(attrString(attrs, "anchor", ""))
	content := escapeText(attrString(attrs, "content", ""))
	return template.HTML(fmt.Sprintf("<%s%s%s>%s</%s>",
		tag, id, classAttr(classes), content, tag,
	)), nil
}

// renderList emits <ul> / <ol> for core/list. The TS definition
// stores the items as a flat string array under the "values"
// attribute (one row per entry); we mirror that shape.
func renderList(block Block, _ template.HTML, _ Context) (template.HTML, error) {
	attrs := block.Attributes
	tag := "ul"
	if attrBool(attrs, "ordered", false) {
		tag = "ol"
	}
	classes := []string{"gn-block-list"}
	var items strings.Builder
	if values, ok := attrs["values"].([]any); ok {
		for _, v := range values {
			if s, ok := v.(string); ok {
				items.WriteString("<li>")
				items.WriteString(escapeText(s))
				items.WriteString("</li>")
			}
		}
	}
	return template.HTML(fmt.Sprintf("<%s%s>%s</%s>",
		tag, classAttr(classes), items.String(), tag,
	)), nil
}

// renderImage emits <figure><img/>(<figcaption/>)</figure>.
func renderImage(block Block, _ template.HTML, _ Context) (template.HTML, error) {
	attrs := block.Attributes
	src := attrString(attrs, "src", "")
	alt := attrString(attrs, "alt", "")
	caption := attrString(attrs, "caption", "")
	if src == "" {
		// An image without a src is meaningless; emit an empty
		// figure so theme CSS doesn't have to cope with a
		// half-rendered node.
		return template.HTML(`<figure class="gn-block-image gn-block-image--empty"></figure>`), nil
	}
	classes := []string{"gn-block-image"}
	if a := attrString(attrs, "align", ""); a != "" {
		classes = append(classes, "has-align-"+a)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<figure%s>", classAttr(classes))
	fmt.Fprintf(&b, "<img src=%q alt=%q/>",
		template.HTMLEscapeString(src),
		template.HTMLEscapeString(alt),
	)
	if caption != "" {
		fmt.Fprintf(&b, "<figcaption>%s</figcaption>", escapeText(caption))
	}
	b.WriteString("</figure>")
	return template.HTML(b.String()), nil
}

// renderQuote emits <blockquote> with optional <cite>.
func renderQuote(block Block, _ template.HTML, _ Context) (template.HTML, error) {
	attrs := block.Attributes
	content := escapeText(attrString(attrs, "content", ""))
	citation := attrString(attrs, "citation", "")
	classes := []string{"gn-block-quote"}
	var b strings.Builder
	fmt.Fprintf(&b, "<blockquote%s>", classAttr(classes))
	fmt.Fprintf(&b, "<p>%s</p>", content)
	if citation != "" {
		fmt.Fprintf(&b, "<cite>%s</cite>", escapeText(citation))
	}
	b.WriteString("</blockquote>")
	return template.HTML(b.String()), nil
}

// renderCode emits <pre><code>...</code></pre>.
func renderCode(block Block, _ template.HTML, _ Context) (template.HTML, error) {
	attrs := block.Attributes
	content := escapeText(attrString(attrs, "content", ""))
	lang := attrString(attrs, "language", "")
	classes := []string{"gn-block-code"}
	if lang != "" {
		classes = append(classes, "language-"+lang)
	}
	return template.HTML(fmt.Sprintf("<pre%s><code>%s</code></pre>",
		classAttr(classes), content,
	)), nil
}

// renderSeparator emits a styled <hr/>.
func renderSeparator(block Block, _ template.HTML, _ Context) (template.HTML, error) {
	attrs := block.Attributes
	style := attrString(attrs, "style", "default")
	classes := []string{"gn-block-separator", "gn-block-separator--" + style}
	return template.HTML(fmt.Sprintf("<hr%s/>", classAttr(classes))), nil
}

// renderSpacer emits a vertical spacer.
func renderSpacer(block Block, _ template.HTML, _ Context) (template.HTML, error) {
	attrs := block.Attributes
	height := attrInt(attrs, "height", 24)
	if height < 0 {
		height = 0
	}
	classes := []string{"gn-block-spacer"}
	return template.HTML(fmt.Sprintf(
		`<div%s style="height:%dpx" aria-hidden="true"></div>`,
		classAttr(classes), height,
	)), nil
}

// renderColumns wraps the inner HTML in the canonical columns div.
// Mirrors packages/ts/blocks-core/src/columns/save.ts::serverRender.
func renderColumns(block Block, inner template.HTML, _ Context) (template.HTML, error) {
	attrs := block.Attributes
	cols := attrInt(attrs, "columns", 2)
	classes := []string{
		"gn-block-columns",
		fmt.Sprintf("gn-block-columns--cols-%d", cols),
	}
	if attrBool(attrs, "isStackedOnMobile", true) {
		classes = append(classes, "is-stacked-on-mobile")
	}
	if a := attrString(attrs, "verticalAlignment", ""); a != "" {
		classes = append(classes, "is-vertically-aligned-"+a)
	}
	return template.HTML(fmt.Sprintf("<div%s>%s</div>",
		classAttr(classes), string(inner),
	)), nil
}

// renderGroup wraps inner HTML in a generic container.
func renderGroup(block Block, inner template.HTML, _ Context) (template.HTML, error) {
	attrs := block.Attributes
	tagName := attrString(attrs, "tagName", "div")
	// Defensive allowlist — only sane container tags.
	switch tagName {
	case "div", "section", "header", "footer", "article", "aside", "main", "nav":
	default:
		tagName = "div"
	}
	classes := []string{"gn-block-group"}
	if layout := attrString(attrs, "layout", ""); layout != "" {
		classes = append(classes, "gn-block-group--"+layout)
	}
	return template.HTML(fmt.Sprintf("<%s%s>%s</%s>",
		tagName, classAttr(classes), string(inner), tagName,
	)), nil
}

// renderTable emits a <table> with optional thead / tfoot / caption.
func renderTable(block Block, _ template.HTML, _ Context) (template.HTML, error) {
	attrs := block.Attributes
	caption := attrString(attrs, "caption", "")
	classes := []string{"gn-block-table"}
	if attrBool(attrs, "striped", false) {
		classes = append(classes, "is-style-stripes")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<table%s>", classAttr(classes))
	if caption != "" {
		fmt.Fprintf(&b, "<caption>%s</caption>", escapeText(caption))
	}
	if rows, ok := attrs["body"].([]any); ok {
		b.WriteString("<tbody>")
		for _, rowAny := range rows {
			row, ok := rowAny.([]any)
			if !ok {
				continue
			}
			b.WriteString("<tr>")
			for _, cellAny := range row {
				if s, ok := cellAny.(string); ok {
					b.WriteString("<td>")
					b.WriteString(escapeText(s))
					b.WriteString("</td>")
				}
			}
			b.WriteString("</tr>")
		}
		b.WriteString("</tbody>")
	}
	b.WriteString("</table>")
	return template.HTML(b.String()), nil
}

// renderGallery emits a list of <figure><img/></figure> items.
func renderGallery(block Block, _ template.HTML, _ Context) (template.HTML, error) {
	attrs := block.Attributes
	classes := []string{"gn-block-gallery"}
	cols := attrInt(attrs, "columns", 3)
	classes = append(classes, fmt.Sprintf("gn-block-gallery--cols-%d", cols))
	var b strings.Builder
	fmt.Fprintf(&b, "<div%s>", classAttr(classes))
	if images, ok := attrs["images"].([]any); ok {
		for _, imgAny := range images {
			img, ok := imgAny.(map[string]any)
			if !ok {
				continue
			}
			src := attrString(img, "src", "")
			alt := attrString(img, "alt", "")
			if src == "" {
				continue
			}
			fmt.Fprintf(&b,
				`<figure class="gn-block-gallery__item"><img src=%q alt=%q/></figure>`,
				template.HTMLEscapeString(src),
				template.HTMLEscapeString(alt),
			)
		}
	}
	b.WriteString("</div>")
	return template.HTML(b.String()), nil
}

// renderVideo emits a <figure><video/></figure>.
func renderVideo(block Block, _ template.HTML, _ Context) (template.HTML, error) {
	attrs := block.Attributes
	src := attrString(attrs, "src", "")
	if src == "" {
		return template.HTML(`<figure class="gn-block-video gn-block-video--empty"></figure>`), nil
	}
	classes := []string{"gn-block-video"}
	flags := []string{}
	if attrBool(attrs, "autoplay", false) {
		flags = append(flags, "autoplay")
	}
	if attrBool(attrs, "loop", false) {
		flags = append(flags, "loop")
	}
	if attrBool(attrs, "muted", false) {
		flags = append(flags, "muted")
	}
	if attrBool(attrs, "controls", true) {
		flags = append(flags, "controls")
	}
	if attrBool(attrs, "playsInline", false) {
		flags = append(flags, "playsinline")
	}
	flagsAttr := ""
	if len(flags) > 0 {
		flagsAttr = " " + strings.Join(flags, " ")
	}
	return template.HTML(fmt.Sprintf(
		`<figure%s><video src=%q%s></video></figure>`,
		classAttr(classes),
		template.HTMLEscapeString(src),
		flagsAttr,
	)), nil
}

// renderButton emits an anchor styled as a button.
func renderButton(block Block, _ template.HTML, _ Context) (template.HTML, error) {
	attrs := block.Attributes
	text := attrString(attrs, "text", "")
	url := attrString(attrs, "url", "")
	classes := []string{"gn-block-button"}
	if style := attrString(attrs, "style", ""); style != "" {
		classes = append(classes, "gn-block-button--"+style)
	}
	return template.HTML(fmt.Sprintf("<a%s%s>%s</a>",
		hrefAttr(url),
		classAttr(classes),
		escapeText(text),
	)), nil
}

// renderFile emits a file-download anchor with an optional button.
func renderFile(block Block, _ template.HTML, _ Context) (template.HTML, error) {
	attrs := block.Attributes
	url := attrString(attrs, "href", "")
	name := attrString(attrs, "fileName", "")
	classes := []string{"gn-block-file"}
	if url == "" {
		return template.HTML(`<div class="gn-block-file gn-block-file--empty"></div>`), nil
	}
	return template.HTML(fmt.Sprintf(
		`<div%s><a href=%q download>%s</a></div>`,
		classAttr(classes),
		template.HTMLEscapeString(url),
		escapeText(name),
	)), nil
}

// renderEmbed emits a generic embed wrapper. Specific provider
// expansion lives in the embed/provider table — at this layer we
// emit only the wrapping <figure>.
func renderEmbed(block Block, _ template.HTML, _ Context) (template.HTML, error) {
	attrs := block.Attributes
	provider := attrString(attrs, "provider", "")
	url := attrString(attrs, "url", "")
	classes := []string{"gn-block-embed"}
	if provider != "" {
		classes = append(classes, "gn-block-embed--"+provider)
	}
	return template.HTML(fmt.Sprintf(
		`<figure%s data-embed-url=%q></figure>`,
		classAttr(classes),
		template.HTMLEscapeString(url),
	)), nil
}
