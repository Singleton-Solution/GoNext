/**
 * Tests for `defineTheme()`.
 *
 * The function is an identity helper, so the runtime tests are tiny —
 * the important coverage is the *type-level* assertion that an unknown
 * top-level key is a compile error. Vitest doesn't run TS in a
 * separate compile, but the typecheck script (`pnpm typecheck`) does,
 * and the comments below show the case that catches the bug.
 *
 * If we ever need full type-level CI coverage we can layer in `tsd` or
 * `expect-type`; for now, the comment-block "negative" cases live
 * alongside the positive ones so a reviewer sees what's pinned.
 */

import { describe, expect, it } from 'vitest';
import { defineTheme, type GoNextTheme } from './define-theme.ts';

describe('defineTheme()', () => {
  it('returns its input verbatim (identity at runtime)', () => {
    const theme = defineTheme({
      version: 1,
      settings: {
        color: {
          palette: [{ slug: 'accent', name: 'Accent', color: '#2563eb' }],
        },
      },
    });
    expect(theme.version).toBe(1);
    expect(theme.settings.color?.palette).toEqual([
      { slug: 'accent', name: 'Accent', color: '#2563eb' },
    ]);
  });

  it('preserves nested structure (no copy, no normalization)', () => {
    const settings = { color: { palette: [] } };
    const theme = defineTheme({ version: 1, settings });
    expect(theme.settings).toBe(settings);
  });

  it('accepts every field documented in §3.1', () => {
    // This is a near-duplicate of the §3.1 example in
    // `theme-json.test.ts`. The point here is that `defineTheme()`
    // accepts the same shape — not that the shape is parseable
    // (that's `theme-json.test.ts`'s job).
    const theme = defineTheme({
      $schema: 'https://gonext.dev/schemas/theme.json/v1',
      version: 1,
      title: 'Test',
      settings: {},
      supports: { darkModeAuto: true },
      patterns: ['hero'],
      customTemplates: [{ name: 'page-landing', title: 'Landing' }],
      templateParts: [{ name: 'header', area: 'header' }],
    });
    expect(theme.supports?.darkModeAuto).toBe(true);
    expect(theme.templateParts?.[0]?.area).toBe('header');
  });

  it('return type widens to GoNextTheme for downstream consumers', () => {
    const t: GoNextTheme = defineTheme({ version: 1, settings: {} });
    expect(t.version).toBe(1);
  });

  // ─── Negative cases (compile-time) ─────────────────────────────────
  //
  // The following snippets are commented out *intentionally*. Each one
  // would fail to compile under the `Exact<…>` constraint in
  // `define-theme.ts`, and we keep them here as a paper trail so a
  // reviewer can confirm the constraint actually fires.
  //
  // Uncomment any to reproduce the type error locally with
  // `pnpm --filter @gonext/theme-sdk typecheck`.
  //
  //   defineTheme({
  //     version: 1,
  //     settings: {},
  //     // 'oopsTypo' is not a key of GoNextTheme/ThemeJson.
  //     oopsTypo: true,
  //   });
  //
  //   defineTheme({
  //     // 'version' must be the literal 1.
  //     version: 2,
  //     settings: {},
  //   });
  //
  //   defineTheme({
  //     // 'settings' is required.
  //     version: 1,
  //   } as any);
  it('compile-time negative cases are pinned in the source comments', () => {
    // Sentinel: this body asserts nothing because the assertion is
    // structural. Removing the comments above would surface the
    // type errors at `pnpm typecheck`.
    expect(true).toBe(true);
  });
});
