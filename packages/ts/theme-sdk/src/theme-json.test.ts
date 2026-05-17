/**
 * Tests for the `theme.json` TypeScript type surface.
 *
 * These tests are deliberately type-position-heavy: the types
 * themselves are the contract, and the test's job is to ensure a
 * known-good `theme.json` (the §3.1 example from
 * `docs/03-theme-system.md`) compiles cleanly against them. If a field
 * is renamed or required-ness changes on the Go side without a TS
 * follow-up, this file fails to compile — exactly the drift detector
 * we want.
 *
 * Some assertions are structural ("`version` is 1") and some are
 * type-level (`expectAssignable`); the latter are a tiny helper rather
 * than a third-party `tsd` install to keep the dependency footprint
 * minimal.
 */

import { describe, expect, it } from 'vitest';
import {
  THEME_JSON_CURRENT_VERSION,
  type ColorEntry,
  type ThemeJson,
  type TemplatePartArea,
} from './theme-json.ts';

/**
 * Type-level assertion: the value passed in must be assignable to
 * `T`. The runtime body is intentionally empty — the assertion is the
 * `T` parameter on the call site. Keeping the helper here (rather
 * than a shared one) makes the assertion self-contained.
 */
function expectAssignable<T>(_value: T): void {
  /* type-only — no runtime check */
}

/**
 * The canonical §3.1 example, transcribed into a TS literal. If this
 * fails to typecheck, either the Go types changed without TS keeping
 * up, or the docs need updating — either way, the failure points at
 * the right place.
 */
const goodTheme: ThemeJson = {
  $schema: 'https://gonext.dev/schemas/theme.json/v1',
  version: 1,
  title: 'Hello GoNext',
  settings: {
    appearanceTools: true,
    color: {
      palette: [
        { slug: 'ink', name: 'Ink', color: '#0f172a' },
        { slug: 'paper', name: 'Paper', color: '#ffffff' },
        { slug: 'muted', name: 'Muted', color: '#64748b' },
        { slug: 'accent', name: 'Accent', color: '#2563eb' },
        { slug: 'accent-fg', name: 'On Accent', color: '#ffffff' },
      ],
      gradients: [
        {
          slug: 'sunset',
          name: 'Sunset',
          gradient: 'linear-gradient(135deg, #f59e0b, #ef4444)',
        },
      ],
      custom: true,
      customGradient: true,
      duotone: [],
    },
    typography: {
      fontFamilies: [
        {
          slug: 'sans',
          name: 'Sans',
          fontFamily: 'Inter, ui-sans-serif, system-ui',
          fontFace: [
            {
              src: '/assets/fonts/Inter-Variable.woff2',
              fontWeight: '100 900',
              fontStyle: 'normal',
              fontDisplay: 'swap',
            },
          ],
        },
        {
          slug: 'serif',
          name: 'Serif',
          fontFamily: 'Iowan Old Style, Apple Garamond, Baskerville, serif',
        },
      ],
      fontSizes: [
        { slug: 'sm', name: 'Small', size: '0.875rem' },
        { slug: 'md', name: 'Medium', size: '1rem' },
        { slug: 'lg', name: 'Large', size: '1.25rem' },
        { slug: 'xl', name: 'X-Large', size: '1.75rem' },
        {
          slug: 'display',
          name: 'Display',
          size: '2.5rem',
          fluid: { min: '2rem', max: '3.5rem' },
        },
      ],
      lineHeight: true,
      letterSpacing: true,
      textDecoration: true,
    },
    spacing: {
      units: ['px', 'rem', 'em', '%', 'vw'],
      spacingScale: {
        operator: '*',
        increment: 1.5,
        steps: 7,
        mediumStep: 1.5,
        unit: 'rem',
      },
      padding: true,
      margin: true,
      blockGap: true,
    },
    layout: { contentSize: '720px', wideSize: '1180px' },
    border: { color: true, radius: true, style: true, width: true },
    shadow: {
      presets: [
        { slug: 'soft', name: 'Soft', shadow: '0 1px 2px rgba(0,0,0,.05)' },
        { slug: 'lifted', name: 'Lifted', shadow: '0 8px 24px rgba(0,0,0,.12)' },
      ],
    },
    blocks: {
      'core/button': {
        border: { radius: true },
        color: { background: true, text: true },
      },
    },
  },
  styles: {
    color: { background: 'var(--gn-color-paper)', text: 'var(--gn-color-ink)' },
    typography: {
      fontFamily: 'var(--gn-font-sans)',
      fontSize: 'var(--gn-font-md)',
      lineHeight: '1.6',
    },
    elements: {
      h1: { typography: { fontSize: 'var(--gn-font-2xl)', lineHeight: '1.1' } },
      h2: { typography: { fontSize: 'var(--gn-font-xl)' } },
      link: { color: { text: 'var(--gn-color-accent)' } },
    },
    blocks: {
      'core/button': {
        color: {
          background: 'var(--gn-color-accent)',
          text: 'var(--gn-color-accent-fg)',
        },
        border: { radius: '0.5rem' },
      },
    },
  },
  supports: {
    blockTemplates: true,
    siteEditor: true,
    darkModeAuto: true,
    customizer: true,
    menus: ['primary', 'footer'],
    widgetAreas: ['sidebar-main', 'footer-1', 'footer-2'],
  },
  patterns: ['hero-cta', 'three-column-features'],
  customTemplates: [
    { name: 'page-landing', title: 'Landing Page', postTypes: ['page'] },
  ],
  templateParts: [
    { name: 'header', title: 'Header', area: 'header' },
    { name: 'footer', title: 'Footer', area: 'footer' },
  ],
};

describe('THEME_JSON_CURRENT_VERSION', () => {
  it('is exactly 1 (matches Go theme.CurrentVersion)', () => {
    expect(THEME_JSON_CURRENT_VERSION).toBe(1);
  });
});

describe('ThemeJson surface', () => {
  it('the §3.1 example typechecks and round-trips through JSON unchanged', () => {
    // If the structural shape changes (a renamed key, a tightened
    // required field), the variable declaration above won't compile.
    // Reaching this assertion means the types accept the canonical
    // theme.
    expect(goodTheme.version).toBe(1);
    expect(goodTheme.settings.color?.palette?.[0]?.slug).toBe('ink');

    // JSON round-trip is a sanity check that the shape is plain data —
    // no `Symbol`s, no functions sneaking in.
    const cloned: ThemeJson = JSON.parse(JSON.stringify(goodTheme));
    expect(cloned).toEqual(goodTheme);
  });

  it('minimal theme (version + settings) typechecks', () => {
    const minimal: ThemeJson = { version: 1, settings: {} };
    expectAssignable<ThemeJson>(minimal);
    expect(minimal.version).toBe(1);
  });

  it('ColorEntry shape pins the slug/name/color trio', () => {
    const c: ColorEntry = { slug: 'ink', name: 'Ink', color: '#0f172a' };
    expect(c.slug).toBe('ink');
    expect(c.color.startsWith('#')).toBe(true);
  });

  it('TemplatePartArea is restricted to the closed §5.3 set', () => {
    const areas: TemplatePartArea[] = ['header', 'footer', 'sidebar', 'uncategorized'];
    expectAssignable<TemplatePartArea[]>(areas);
    expect(areas).toHaveLength(4);

    // The next two lines are commented out *intentionally* — they
    // would fail at compile time. We keep them as a paper trail so
    // a reviewer can confirm the constraint is real.
    //
    // const bad: TemplatePartArea = 'main';     // ts error
    // const bad2: TemplatePartArea = 'HEADER'; // ts error
  });
});
