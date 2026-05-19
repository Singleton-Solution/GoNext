/**
 * Tests for the theme resolver helpers.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { resolveActiveTheme, defaultActiveTheme } from './theme.ts';

afterEach(() => {
  vi.restoreAllMocks();
});

describe('defaultActiveTheme', () => {
  it('matches the gn-hello slug', () => {
    const t = defaultActiveTheme();
    expect(t.slug).toBe('gn-hello');
    expect(t.title).toBe('gn-hello');
  });

  it('emits the canonical --wp-preset-- namespace tokens', () => {
    const t = defaultActiveTheme();
    expect(t.cssCustomProperties).toContain('--wp-preset--color--ink');
    expect(t.cssCustomProperties).toContain(':root');
  });

  it('ships a header and a footer', () => {
    const t = defaultActiveTheme();
    expect(t.headerHtml).toContain('<header');
    expect(t.footerHtml).toContain('<footer');
  });
});

describe('resolveActiveTheme', () => {
  it('returns the API theme when available', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          slug: 'gn-pro',
          title: 'gn-pro',
          cssCustomProperties: ':root{--y:1}',
          headerHtml: '<header>pro</header>',
          footerHtml: '<footer>pro</footer>',
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    );
    const theme = await resolveActiveTheme();
    expect(theme.slug).toBe('gn-pro');
  });

  it('falls back to the default theme when the API is unreachable', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('down'));
    const theme = await resolveActiveTheme();
    expect(theme.slug).toBe('gn-hello');
  });
});
