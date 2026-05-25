/**
 * i18n.t / i18n.load tests.
 *
 * Catalogue fetch is mocked; we exercise the fallback, the
 * interpolation, the cache reuse, and the malformed-input
 * resilience.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { i18n } from './i18n';
import { __resetSlugCache, setSlug } from './slug';

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  __resetSlugCache();
  setSlug('demo');
  i18n.__reset();
  fetchMock = vi.fn();
  globalThis.fetch = fetchMock as unknown as typeof fetch;
  document.documentElement.setAttribute('lang', 'en');
});

afterEach(() => {
  i18n.__reset();
  __resetSlugCache();
});

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  });
}

describe('i18n.load', () => {
  it('fetches the catalogue and caches the result', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ 'hello.world': 'Hello!' }));
    const cat = await i18n.load();
    expect(cat['hello.world']).toBe('Hello!');
    // Second call: served from cache, no second fetch.
    await i18n.load();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0]![0]).toBe('/api/plugins/demo/i18n/en.json');
  });

  it('uses an explicit locale when provided', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ 'hi': 'Bonjour' }));
    await i18n.load('fr-FR');
    expect(fetchMock.mock.calls[0]![0]).toBe('/api/plugins/demo/i18n/fr-FR.json');
  });

  it('resolves to an empty catalogue on fetch failure', async () => {
    fetchMock.mockRejectedValueOnce(new Error('boom'));
    const cat = await i18n.load();
    expect(cat).toEqual({});
  });

  it('filters non-string catalogue entries', async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ valid: 'ok', invalid: 42, also: null }),
    );
    const cat = await i18n.load();
    expect(cat).toEqual({ valid: 'ok' });
  });

  it('shares the in-flight promise', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ k: 'v' }));
    const [a, b] = await Promise.all([i18n.load(), i18n.load()]);
    expect(a).toEqual({ k: 'v' });
    expect(b).toEqual({ k: 'v' });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

describe('i18n.t', () => {
  it('returns the key while the catalogue is loading', () => {
    fetchMock.mockResolvedValue(jsonResponse({ greeting: 'Hello' }));
    // Synchronous call — first t() kicks off async load.
    const out = i18n.t('greeting');
    expect(out).toBe('greeting');
  });

  it('returns the translation once the catalogue resolves', async () => {
    fetchMock.mockResolvedValue(jsonResponse({ greeting: 'Hello' }));
    await i18n.load();
    expect(i18n.t('greeting')).toBe('Hello');
  });

  it('falls back to the key when the entry is missing', async () => {
    fetchMock.mockResolvedValue(jsonResponse({ other: 'present' }));
    await i18n.load();
    expect(i18n.t('missing.key')).toBe('missing.key');
  });

  it('interpolates {name} placeholders', async () => {
    fetchMock.mockResolvedValue(jsonResponse({ welcome: 'Hi, {name}!' }));
    await i18n.load();
    expect(i18n.t('welcome', { name: 'Ada' })).toBe('Hi, Ada!');
  });

  it('leaves unknown placeholders intact', async () => {
    fetchMock.mockResolvedValue(jsonResponse({ tpl: 'Hi {name} and {other}' }));
    await i18n.load();
    expect(i18n.t('tpl', { name: 'A' })).toBe('Hi A and {other}');
  });

  it('returns the key when no slug is detected', () => {
    setSlug(null);
    // No fetch should be issued because we have no slug to address.
    expect(i18n.t('any.key')).toBe('any.key');
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
