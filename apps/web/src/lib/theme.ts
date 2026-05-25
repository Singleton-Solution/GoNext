/**
 * Theme helpers for @gonext/web.
 *
 * The Go side ships the active-theme summary already block-walked (the
 * header/footer parts come back as HTML strings, and the design-token
 * surface is emitted as a single `:root { ... }` CSS block). This
 * module wraps the fetch + provides a `defaultActiveTheme()` fallback
 * the renderer can lean on when the API endpoint isn't reachable, so
 * dev / test paths still produce a sensible site.
 *
 * Keeping the fallback inline (rather than re-implementing
 * `EmitCSSCustomProperties` in TypeScript) avoids a second source of
 * truth for the WordPress-style `--wp-preset--*` namespace. The Go
 * package owns that decision; we just consume the output.
 */

import { fetchActiveTheme, type ActiveTheme } from './api.ts';

/**
 * Minimal hand-written fallback. Mirrors the Living-Systems brand
 * tokens (cream paper + forest ink + emerald accent) closely enough
 * that the public site is visually coherent when the API is offline.
 *
 * The HTML strings are deliberately empty — the brand chrome wired in
 * `PublicShell` already paints the marketing nav + footer, so the
 * fallback shouldn't double-stack a second header / footer. When a
 * real theme is installed it ships richer header / footer HTML that
 * the renderer happily forwards.
 */
export function defaultActiveTheme(): ActiveTheme {
  return {
    slug: 'gn-hello',
    title: 'gn-hello',
    cssCustomProperties: [
      ':root {',
      '  --wp-preset--color--ink: #0E1A14;',
      '  --wp-preset--color--paper: #F5F2EA;',
      '  --wp-preset--color--muted: #4A5C52;',
      '  --wp-preset--color--accent: #047857;',
      '  --wp-preset--font-family--sans: var(--font-sans, Geist), system-ui, sans-serif;',
      '  --wp-preset--font-family--serif: var(--font-serif, Instrument Serif), Georgia, serif;',
      '  --wp-preset--font-size--sm: 0.875rem;',
      '  --wp-preset--font-size--md: 1rem;',
      '  --wp-preset--font-size--lg: 1.25rem;',
      '  --wp-preset--font-size--xl: 1.75rem;',
      '  --wp-preset--layout--content: 720px;',
      '  --wp-preset--layout--wide: 1180px;',
      '}',
    ].join('\n'),
    // Minimal theme-level header / footer for the fallback path. The
    // brand chrome in `PublicShell` paints the marketing nav + footer
    // around these, so the theme parts here can be tiny — they act as
    // a thin "section header" / "site colophon" within the chrome.
    headerHtml:
      '<header class="gn-site-header"><span class="wordmark"><span class="wm-go">Go</span><span class="wm-next">Next</span></span></header>',
    footerHtml:
      '<footer class="gn-site-footer"><p>Built with <a href="https://github.com/Singleton-Solution/GoNext">GoNext</a>.</p></footer>',
  };
}

/**
 * Resolve the active theme. Prefer the API response; fall back to the
 * inline default when the Go side isn't reachable. The fallback is
 * documented so the renderer never throws on a dev machine that's
 * running just the Next.js app.
 */
export async function resolveActiveTheme(
  options: { revalidate?: number } = {},
): Promise<ActiveTheme> {
  const fromApi = await fetchActiveTheme(options);
  return fromApi ?? defaultActiveTheme();
}
