package theme

import (
	"fmt"
	"strings"
)

// cssPropertyPrefix is the namespace prefix for every theme-derived
// CSS custom property. Issue #5 specifies the WordPress-style
// "--wp-preset--" namespace; we keep that to ease migration of theme
// authors moving over from WP. The renderer (see
// docs/03-theme-system.md §3.2) injects the emitted block into the
// document head verbatim.
const cssPropertyPrefix = "--wp-preset"

// EmitCSSCustomProperties renders the manifest's tokens into a
// ":root { … }" block of CSS custom properties.
//
// Output is deterministic: palette/gradient/font/size entries appear
// in declaration order. Empty manifests produce an empty string (no
// ":root {}" wrapper), so callers can concatenate the result
// straight into HTML without a stylesheet placeholder shim.
//
// The property naming follows the WordPress convention so theme
// authors moving from WP find familiar names:
//
//	--wp-preset--color--ink:          #0f172a;
//	--wp-preset--color--accent:       #2563eb;
//	--wp-preset--gradient--sunset:    linear-gradient(...);
//	--wp-preset--font-family--sans:   Inter, ui-sans-serif, system-ui;
//	--wp-preset--font-size--md:       1rem;
//	--wp-preset--spacing--unit:       rem;
//	--wp-preset--layout--content:     720px;
//	--wp-preset--layout--wide:        1180px;
//	--wp-preset--shadow--soft:        0 1px 2px rgba(0,0,0,.05);
//
// Fluid font sizes emit clamp(min, base, max) so the renderer needs no
// post-processing.
func (t *ThemeJSON) EmitCSSCustomProperties() string {
	// Pre-count to decide whether to emit at all.
	if t == nil {
		return ""
	}
	if !t.hasAnyToken() {
		return ""
	}

	var b strings.Builder
	b.WriteString(":root {\n")

	for _, c := range t.Settings.Color.Palette {
		fmt.Fprintf(&b, "  %s--color--%s: %s;\n", cssPropertyPrefix, c.Slug, c.Color)
	}
	for _, g := range t.Settings.Color.Gradients {
		fmt.Fprintf(&b, "  %s--gradient--%s: %s;\n", cssPropertyPrefix, g.Slug, g.Gradient)
	}
	for _, f := range t.Settings.Typography.FontFamilies {
		fmt.Fprintf(&b, "  %s--font-family--%s: %s;\n", cssPropertyPrefix, f.Slug, f.FontFamily)
	}
	for _, s := range t.Settings.Typography.FontSizes {
		val := s.Size
		if s.Fluid != nil {
			// clamp(min, base, max) — the renderer trusts that the
			// validator already verified min/max are CSS lengths.
			val = fmt.Sprintf("clamp(%s, %s, %s)", s.Fluid.Min, s.Size, s.Fluid.Max)
		}
		fmt.Fprintf(&b, "  %s--font-size--%s: %s;\n", cssPropertyPrefix, s.Slug, val)
	}
	for _, sh := range t.Settings.Shadow.Presets {
		fmt.Fprintf(&b, "  %s--shadow--%s: %s;\n", cssPropertyPrefix, sh.Slug, sh.Shadow)
	}
	if t.Settings.Layout.ContentSize != "" {
		fmt.Fprintf(&b, "  %s--layout--content: %s;\n", cssPropertyPrefix, t.Settings.Layout.ContentSize)
	}
	if t.Settings.Layout.WideSize != "" {
		fmt.Fprintf(&b, "  %s--layout--wide: %s;\n", cssPropertyPrefix, t.Settings.Layout.WideSize)
	}

	b.WriteString("}\n")
	return b.String()
}

// hasAnyToken reports whether the manifest declares at least one
// emittable token. It is used by EmitCSSCustomProperties to decide
// whether to skip the wrapper block entirely.
func (t *ThemeJSON) hasAnyToken() bool {
	if len(t.Settings.Color.Palette) > 0 {
		return true
	}
	if len(t.Settings.Color.Gradients) > 0 {
		return true
	}
	if len(t.Settings.Typography.FontFamilies) > 0 {
		return true
	}
	if len(t.Settings.Typography.FontSizes) > 0 {
		return true
	}
	if len(t.Settings.Shadow.Presets) > 0 {
		return true
	}
	if t.Settings.Layout.ContentSize != "" || t.Settings.Layout.WideSize != "" {
		return true
	}
	return false
}
