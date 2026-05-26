// Package shortcode parses WordPress shortcodes embedded in post
// content and translates them into one of three target shapes,
// selected by the operator at migration time:
//
//   - ModeMap: translate to a typed GoNext Block (e.g. [caption] →
//     core/image with caption attr). The registered translator
//     decides the BlockType and attribute mapping.
//   - ModePreserve: leave the shortcode text verbatim inside a
//     core/html block. Useful when a plugin will continue to render
//     the shortcode on the GoNext side (rare but supported).
//   - ModeStrip: remove the shortcode opening/closing tags but keep
//     any inner content as plain text. The lossy fallback for sites
//     that have decided to abandon a particular shortcode.
//
// The parser is a single-pass scanner that handles both self-closing
// shortcodes ([name attr="v" /]) and the enclosing form
// ([name attr="v"]inner content[/name]). Attribute quoting follows
// WP's loose rules: double-quoted, single-quoted, and bare values
// are all accepted; positional values (no key) end up under the
// numeric keys "0", "1", … in the parsed Attrs map.
//
// Typical usage:
//
//	reg := shortcode.NewRegistry()
//	reg.RegisterDefaults()
//	out, _ := shortcode.Process(content, shortcode.Options{
//	    Mode:     shortcode.ModeMap,
//	    Registry: reg,
//	})
//	// out is a []html2blocks.Block ready to be marshalled.
//
// The package is concurrency-safe for reads after Registry setup is
// complete; Register* methods are not safe to call from multiple
// goroutines.
//
// See issue #174.
package shortcode
