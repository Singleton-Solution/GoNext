/**
 * HTML-escape helpers — a private copy of the same routine that lives in
 * `@gonext/blocks-core/src/internal/escape.ts`. We duplicate (rather than
 * re-export) because this package sits *below* blocks-core in the
 * dependency graph: the core blocks pull `<RichText/>` from here, not the
 * other way around. The set of characters and replacement order match
 * byte-for-byte so the strings produced here can flow straight into
 * `paragraph.save()` (etc.) without surprising the round-trip tests.
 */

/**
 * Escape a string for safe interpolation into HTML text or attribute
 * values. Order matters — escaping `&` first prevents double-encoding the
 * entities introduced by the later replacements.
 */
export function escapeHtml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/**
 * Inverse of `escapeHtml`. Used by `deserialize` to turn the canonical
 * persisted HTML back into raw text before re-inserting into the editor.
 * Decoding `&amp;` last mirrors why encoding handles it first: any
 * `&lt;`/`&gt;`/etc. *inside* the encoded payload should stay encoded
 * until their parent ampersand is resolved.
 */
export function decodeHtml(value: string): string {
  return value
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&amp;/g, '&');
}
