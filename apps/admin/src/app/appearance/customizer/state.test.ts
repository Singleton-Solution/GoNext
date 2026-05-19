/**
 * Tests for the customizer state helpers.
 *
 * The helpers are pure functions so each case is a small, fast
 * round trip — initialState, buildOverrides, previewUrl,
 * isOverrideEmpty.
 */
import { describe, expect, it } from 'vitest';
import {
  ADVANCED_DEFAULTS,
  buildOverrides,
  encodePreviewOverrides,
  initialState,
  isOverrideEmpty,
  previewUrl,
} from './state';
import type { ActiveResponse } from './types';

function makeActive(): ActiveResponse {
  return {
    themeSlug: 'gn-hello',
    theme: {
      version: 1,
      title: 'gn-hello',
      settings: {
        color: {
          palette: [
            { slug: 'ink', name: 'Ink', color: '#0f172a' },
            { slug: 'accent', name: 'Accent', color: '#2563eb' },
          ],
        },
        typography: {
          fontFamilies: [
            { slug: 'sans', name: 'Sans', fontFamily: 'system-ui' },
          ],
          fontSizes: [{ slug: 'md', name: 'Medium', size: '1rem' }],
        },
        layout: { contentSize: '720px', wideSize: '1180px' },
      },
    },
    overrides: {},
  };
}

describe('initialState', () => {
  it('hydrates from the theme manifest when no overrides exist', () => {
    const state = initialState(makeActive());
    expect(state.palette).toHaveLength(2);
    expect(state.palette[0]?.color).toBe('#0f172a');
    expect(state.layout.contentSize).toBe('720px');
  });

  it('prefers persisted overrides when they exist', () => {
    const active = makeActive();
    active.overrides = {
      settings: {
        layout: { contentSize: '800px', wideSize: '1200px' },
      },
    };
    const state = initialState(active);
    expect(state.layout.contentSize).toBe('800px');
    expect(state.layout.wideSize).toBe('1200px');
  });

  it('produces a fresh deep copy independent of the source', () => {
    const active = makeActive();
    const state = initialState(active);
    if (state.palette[0]) state.palette[0].color = '#fff';
    // Mutating the state must not bleed into the upstream manifest.
    expect(active.theme.settings.color?.palette?.[0]?.color).toBe('#0f172a');
  });
});

describe('buildOverrides', () => {
  it('omits sections that match the theme defaults', () => {
    const active = makeActive();
    const state = initialState(active);
    const overrides = buildOverrides(state, active.theme);
    expect(overrides.settings).toEqual({});
  });

  it('includes the palette when a color changes', () => {
    const active = makeActive();
    const state = initialState(active);
    if (state.palette[1]) state.palette[1].color = '#ff0066';
    const overrides = buildOverrides(state, active.theme);
    expect(overrides.settings?.color?.palette).toHaveLength(2);
    expect(overrides.settings?.color?.palette?.[1]?.color).toBe('#ff0066');
  });

  it('includes layout when a width changes', () => {
    const active = makeActive();
    const state = initialState(active);
    state.layout.contentSize = '800px';
    const overrides = buildOverrides(state, active.theme);
    expect(overrides.settings?.layout?.contentSize).toBe('800px');
  });
});

describe('isOverrideEmpty', () => {
  it('returns true when no fields differ from defaults', () => {
    const overrides = buildOverrides(initialState(makeActive()), makeActive().theme);
    expect(isOverrideEmpty(overrides)).toBe(true);
  });

  it('returns false when any field differs', () => {
    const active = makeActive();
    const state = initialState(active);
    state.layout.contentSize = '999px';
    expect(isOverrideEmpty(buildOverrides(state, active.theme))).toBe(false);
  });
});

describe('previewUrl', () => {
  it('appends customizer=preview and a base64 overrides param', () => {
    const url = previewUrl('http://localhost:3000/', {
      settings: { layout: { contentSize: '800px' } },
    });
    expect(url).toContain('customizer=preview');
    expect(url).toContain('overrides=');
    // The encoded payload must decode back to the input — guards against
    // accidental URL-encoding changes.
    const overrides = url.split('overrides=')[1] ?? '';
    const decoded = JSON.parse(base64UrlDecode(overrides));
    expect(decoded.settings.layout.contentSize).toBe('800px');
  });

  it('encodes empty overrides predictably', () => {
    const url = previewUrl('http://localhost:3000', {});
    const overrides = url.split('overrides=')[1] ?? '';
    const decoded = JSON.parse(base64UrlDecode(overrides));
    expect(decoded).toEqual({});
  });

  it('uses & when the base URL already has a query string', () => {
    const url = previewUrl('http://localhost:3000/?lang=en', {});
    expect(url).toContain('?lang=en&customizer=preview');
  });
});

describe('encodePreviewOverrides', () => {
  it('round-trips through base64', () => {
    const encoded = encodePreviewOverrides({ settings: { layout: { contentSize: '999px' } } });
    expect(JSON.parse(base64UrlDecode(encoded))).toEqual({
      settings: { layout: { contentSize: '999px' } },
    });
  });
});

describe('advanced surface — initialState defaults', () => {
  it('seeds the six spacing tokens, four shadow presets, four breakpoints', () => {
    const state = initialState(makeActive());
    expect(state.spacingTokens.map((t) => t.slug)).toEqual([
      'xs',
      'sm',
      'md',
      'lg',
      'xl',
      '2xl',
    ]);
    expect(state.shadowPresets.map((p) => p.slug)).toEqual(['sm', 'md', 'lg', 'xl']);
    expect(state.breakpoints.map((b) => b.slug)).toEqual(['sm', 'md', 'lg', 'xl']);
  });

  it('prefers theme-declared custom tokens over the built-in defaults', () => {
    const active = makeActive();
    active.theme.settings.custom = {
      gonext: {
        breakpoints: [
          { slug: 'sm', name: 'Small', width: 600 },
          { slug: 'md', name: 'Medium', width: 800 },
          { slug: 'lg', name: 'Large', width: 1000 },
          { slug: 'xl', name: 'Extra large', width: 1400 },
        ],
      },
    };
    const state = initialState(active);
    expect(state.breakpoints[3]?.width).toBe(1400);
  });

  it('prefers persisted overrides over theme defaults', () => {
    const active = makeActive();
    active.overrides = {
      settings: {
        custom: {
          gonext: {
            spacingTokens: [
              { slug: 'xs', name: 'Extra small', size: '0.125rem' },
              { slug: 'sm', name: 'Small', size: '0.25rem' },
              { slug: 'md', name: 'Medium', size: '0.5rem' },
              { slug: 'lg', name: 'Large', size: '1rem' },
              { slug: 'xl', name: 'Extra large', size: '2rem' },
              { slug: '2xl', name: '2× extra large', size: '4rem' },
            ],
          },
        },
      },
    };
    const state = initialState(active);
    expect(state.spacingTokens[0]?.size).toBe('0.125rem');
    expect(state.spacingTokens[5]?.size).toBe('4rem');
  });
});

describe('advanced surface — buildOverrides diffing', () => {
  it('omits custom block when every token matches the defaults', () => {
    const active = makeActive();
    const state = initialState(active);
    const overrides = buildOverrides(state, active.theme);
    expect(overrides.settings?.custom).toBeUndefined();
  });

  it('includes spacingTokens when one token changes', () => {
    const active = makeActive();
    const state = initialState(active);
    const mdIndex = state.spacingTokens.findIndex((t) => t.slug === 'md');
    if (state.spacingTokens[mdIndex]) {
      state.spacingTokens[mdIndex] = {
        ...state.spacingTokens[mdIndex],
        size: '1.5rem',
      };
    }
    const overrides = buildOverrides(state, active.theme);
    expect(overrides.settings?.custom?.gonext?.spacingTokens).toHaveLength(6);
    const md = overrides.settings?.custom?.gonext?.spacingTokens?.find(
      (t) => t.slug === 'md',
    );
    expect(md?.size).toBe('1.5rem');
  });

  it('includes shadowPresets when one slider value moves', () => {
    const active = makeActive();
    const state = initialState(active);
    const md = state.shadowPresets[1];
    if (md) state.shadowPresets[1] = { ...md, blur: 99 };
    const overrides = buildOverrides(state, active.theme);
    const updated = overrides.settings?.custom?.gonext?.shadowPresets?.find(
      (p) => p.slug === 'md',
    );
    expect(updated?.blur).toBe(99);
  });

  it('includes breakpoints when a width changes', () => {
    const active = makeActive();
    const state = initialState(active);
    const lg = state.breakpoints[2];
    if (lg) state.breakpoints[2] = { ...lg, width: 1100 };
    const overrides = buildOverrides(state, active.theme);
    expect(
      overrides.settings?.custom?.gonext?.breakpoints?.find((b) => b.slug === 'lg')
        ?.width,
    ).toBe(1100);
  });

  it('isOverrideEmpty returns false once a custom token changes', () => {
    const active = makeActive();
    const state = initialState(active);
    const sm = state.breakpoints[0];
    if (sm) state.breakpoints[0] = { ...sm, width: 555 };
    const overrides = buildOverrides(state, active.theme);
    expect(isOverrideEmpty(overrides)).toBe(false);
  });
});

describe('ADVANCED_DEFAULTS', () => {
  it('exposes a stable shape', () => {
    expect(ADVANCED_DEFAULTS.spacingTokens.length).toBe(6);
    expect(ADVANCED_DEFAULTS.shadowPresets.length).toBe(4);
    expect(ADVANCED_DEFAULTS.breakpoints.length).toBe(4);
  });
});

function base64UrlDecode(s: string): string {
  let padded = s.replace(/-/g, '+').replace(/_/g, '/');
  while (padded.length % 4) padded += '=';
  if (typeof atob === 'function') return atob(padded);
  return Buffer.from(padded, 'base64').toString('utf8');
}
