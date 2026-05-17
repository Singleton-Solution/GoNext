/**
 * Minimal HTML-escape helpers used by every core block's `save` and
 * server-render template.
 *
 * Why hand-roll instead of pulling in `escape-html`?
 *
 *  - The set of characters we escape is small and the cost is dwarfed by the
 *    cost of rendering the page. A tiny inline function lets the package
 *    stay zero-runtime-dep.
 *  - Keeping the implementation here makes the test suite the single source
 *    of truth for what "safe" means in saved block markup. The Go render
 *    walker uses Go's `html/template` package, which applies an equivalent
 *    set of replacements — so the TS save output and the server-rendered
 *    output line up byte-for-byte for fixed strings.
 */

/**
 * Escape a string for safe interpolation into HTML text or attribute values.
 * Replaces `&`, `<`, `>`, `"`, and `'` with their entity references in that
 * exact order — escaping `&` first prevents double-encoding the entities
 * introduced by later replacements.
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
 * Conditional class-name attribute. Returns an empty string when the class
 * list is empty so the block output stays free of stray ` class=""`
 * fragments. Skips falsy entries so callers can inline ternaries.
 */
export function classAttr(classes: Array<string | false | null | undefined>): string {
  const cleaned = classes.filter(
    (c): c is string => typeof c === 'string' && c.length > 0,
  );
  if (cleaned.length === 0) {
    return '';
  }
  return ` class="${escapeHtml(cleaned.join(' '))}"`;
}
