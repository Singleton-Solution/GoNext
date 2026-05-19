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
 * Minimal hand-written fallback. Matches the gn-hello seed theme
 * (`packages/go/theme/seed/gn-hello/theme.json`) closely enough that
 * the public site is visually coherent when the API is offline.
 *
 * The HTML strings here are deliberately tiny — the real theme parts
 * are rich block trees that the Go walker resolves. This fallback is
 * the "lights on" minimum.
 */
export function defaultActiveTheme(): ActiveTheme {
  return {
    slug: 'gn-hello',
    title: 'gn-hello',
    cssCustomProperties: [
      ':root {',
      '  --wp-preset--color--ink: #0f172a;',
      '  --wp-preset--color--paper: #ffffff;',
      '  --wp-preset--color--muted: #64748b;',
      '  --wp-preset--color--accent: #2563eb;',
      '  --wp-preset--font-family--sans: system-ui, -apple-system, Segoe UI, Helvetica, Arial, sans-serif;',
      '  --wp-preset--font-family--serif: Iowan Old Style, Apple Garamond, Baskerville, Georgia, serif;',
      '  --wp-preset--font-size--sm: 0.875rem;',
      '  --wp-preset--font-size--md: 1rem;',
      '  --wp-preset--font-size--lg: 1.25rem;',
      '  --wp-preset--font-size--xl: 1.75rem;',
      '  --wp-preset--layout--content: 720px;',
      '  --wp-preset--layout--wide: 1180px;',
      '}',
    ].join('\n'),
    headerHtml:
      '<header class="gn-site-header"><a href="/" class="gn-site-title">gn-hello</a></header>',
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
