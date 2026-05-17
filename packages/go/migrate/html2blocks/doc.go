// Package html2blocks converts a WordPress post body's HTML into a flat
// list of GoNext Block Tree nodes.
//
// The package is the bridge between the WXR importer (#153) and the
// canonical block tree consumed by every downstream renderer. The
// translation is intentionally lossy-but-honest: tags we recognise become
// the equivalent core block; tags we don't get wrapped in a paragraph
// fallback that preserves the raw HTML so a human can rescue the content
// later. The walker never throws away bytes silently.
//
// Three styles of input are supported:
//
//   - Gutenberg-era HTML carrying `<!-- wp:foo {...} -->...<!-- /wp:foo -->`
//     comment delimiters. The delimiter pair is authoritative — we trust
//     the block name and JSON attributes embedded in the comment and only
//     fall back to the DOM walker when no delimiter is present.
//   - Classic Gutenberg HTML — semantic markup without comment delimiters,
//     mapped tag-by-tag to the closest core block.
//   - Pre-Gutenberg classic content — long-running `<p>` flows, occasional
//     `<img>`, `<blockquote>` etc. Handled the same way as classic
//     Gutenberg; we never assume comments are present.
//
// The 10 canonical core blocks (see @gonext/blocks-core) are:
// paragraph, heading, list, image, quote, code, separator, spacer,
// columns, group. There is no `core/html` block in the canonical set,
// so anything we can't classify is emitted as a `core/paragraph` with
// the original HTML stored verbatim in the `content` attribute. The
// rendering pipeline already treats paragraph content as opaque, so
// this round-trips cleanly until a dedicated `core/html` lands.
//
// Typical usage:
//
//	blocks, err := html2blocks.Convert([]byte(post.ContentEncoded))
//	if err != nil {
//	    return fmt.Errorf("convert post %d: %w", post.ID, err)
//	}
//	if err := store.Save(ctx, post.ID, blocks); err != nil {
//	    return err
//	}
//
// See issue #170 for the original spec.
package html2blocks
