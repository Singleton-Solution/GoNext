/**
 * CSS custom-property helpers for theme authors.
 *
 * The renderer emits one CSS variable per `theme.json` token at theme
 * load time and injects them into `:root` (see `docs/03-theme-system.md`
 * §3.2 for the canonical list). Authors then reference them in their
 * styles via `var(--gn-…)`. Writing those `var()` strings by hand is
 * error-prone — typos resolve to silent "no variable found" at runtime
 * — so this module ships a helper that builds the strings from typed
 * inputs.
 *
 * The prefix and segment rules below match the documented output (§3.2).
 * If the renderer ever changes the prefix, this is the one-line change.
 */

/**
 * Prefix every theme-derived CSS custom property carries in the
 * emitted token sheet. Documented in `docs/03-theme-system.md` §3.2;
 * mirrored here so callers don't hard-code `--gn-` strings throughout
 * their themes.
 *
 * The Go emitter currently uses `--wp-preset` for backwards
 * compatibility with WordPress muscle memory (see `emit.go`); the
 * §3.2 docs and the styles examples in §3.1 use `--gn-` as the
 * theme-facing public name. Authors should reference the `--gn-`
 * surface; the renderer is responsible for aliasing as needed.
 */
export const CSS_VAR_PREFIX = '--gn-';

/**
 * A theme token reference, expressed as a dot-delimited path
 * (`"color.accent"`) or as an ordered segment array
 * (`["color", "accent"]`). Both forms map to the same output —
 * the array form exists so callers building tokens programmatically
 * don't have to join strings themselves.
 *
 * Empty segments are rejected (`cssVar()` returns the empty
 * `var(--gn-)` so the typo is visible in the browser inspector
 * rather than silently swallowed).
 */
export type ThemeToken = string | readonly string[];

/**
 * Returns the `var()` reference for a theme token.
 *
 * The function joins the token's segments with `-` and prepends
 * `CSS_VAR_PREFIX`, matching the renderer's output. Dots in the
 * string form are treated as segment separators so `"color.accent"`
 * and `["color", "accent"]` produce the same string.
 *
 * @example
 *   cssVar('color.accent')             // → 'var(--gn-color-accent)'
 *   cssVar(['color', 'accent-fg'])     // → 'var(--gn-color-accent-fg)'
 *   cssVar('layout.content')           // → 'var(--gn-layout-content)'
 *   cssVar('font.md')                  // → 'var(--gn-font-md)'
 *
 * No fallback is interpolated — callers that need one can wrap the
 * result (`\`\${cssVar('color.accent')}, #2563eb\``). Keeping the
 * core helper single-purpose makes the output trivially auditable.
 */
export function cssVar(token: ThemeToken): string {
  const segments = Array.isArray(token)
    ? (token as readonly string[])
    : (token as string).split('.');

  // Filter only empty strings — we keep all real segments, including
  // ones that legitimately contain numbers ("2xl") or hyphens
  // ("accent-fg"). The Go validator already rejects slugs with
  // uppercase letters or underscores, so the corresponding `var()`
  // names will already be valid CSS identifiers.
  const clean = segments.filter((s) => s.length > 0);
  return `var(${CSS_VAR_PREFIX}${clean.join('-')})`;
}
