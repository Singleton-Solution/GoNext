/**
 * Slug auto-detection tests.
 *
 * Each strategy gets its own table-driven block. We reset the
 * module-scoped cache in `beforeEach` so detection re-runs against
 * the fixture state for the current test.
 */
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  __resetSlugCache,
  SlugRequiredError,
  getSlug,
  requireSlug,
  setSlug,
} from './slug';

beforeEach(() => {
  __resetSlugCache();
  // Clear any global from a prior test.
  delete (globalThis as { __GN_PLUGIN_SLUG__?: unknown }).__GN_PLUGIN_SLUG__;
  // Strip any plugin <script> tags the previous test inserted.
  if (typeof document !== 'undefined') {
    const stragglers = document.querySelectorAll('script[data-gn-test]');
    stragglers.forEach((s) => s.remove());
    const ctx = document.getElementById('gn-plugin-context');
    if (ctx !== null) ctx.remove();
  }
});

afterEach(() => {
  __resetSlugCache();
});

describe('getSlug', () => {
  it('returns null when no signal is present', () => {
    expect(getSlug()).toBeNull();
  });

  describe('strategy: <script src> on the page', () => {
    it('extracts the slug from /api/plugins/<slug>/web/...', () => {
      const tag = document.createElement('script');
      tag.src = 'https://cms.example.com/api/plugins/seo-helper/web/main.mjs';
      tag.setAttribute('data-gn-test', '1');
      document.head.appendChild(tag);
      expect(getSlug()).toBe('seo-helper');
    });

    it('rejects a malformed slug', () => {
      const tag = document.createElement('script');
      // Capital letters violate the slug pattern.
      tag.src = 'https://cms.example.com/api/plugins/Bad-Slug/web/main.mjs';
      tag.setAttribute('data-gn-test', '1');
      document.head.appendChild(tag);
      expect(getSlug()).toBeNull();
    });

    it('ignores scripts not under /api/plugins/.../web/', () => {
      const tag = document.createElement('script');
      tag.src = 'https://cdn.example.com/static/random.mjs';
      tag.setAttribute('data-gn-test', '1');
      document.head.appendChild(tag);
      expect(getSlug()).toBeNull();
    });

    it('takes the first matching script', () => {
      const a = document.createElement('script');
      a.src = '/api/plugins/first/web/index.mjs';
      a.setAttribute('data-gn-test', '1');
      const b = document.createElement('script');
      b.src = '/api/plugins/second/web/index.mjs';
      b.setAttribute('data-gn-test', '1');
      document.head.append(a, b);
      expect(getSlug()).toBe('first');
    });
  });

  describe('strategy: <script type="application/json"> context block', () => {
    it('reads slug from gn-plugin-context', () => {
      const el = document.createElement('script');
      el.type = 'application/json';
      el.id = 'gn-plugin-context';
      el.textContent = JSON.stringify({ slug: 'context-plugin' });
      document.head.appendChild(el);
      expect(getSlug()).toBe('context-plugin');
    });

    it('returns null on malformed JSON', () => {
      const el = document.createElement('script');
      el.type = 'application/json';
      el.id = 'gn-plugin-context';
      el.textContent = '{not json';
      document.head.appendChild(el);
      expect(getSlug()).toBeNull();
    });

    it('returns null when slug fails the pattern check', () => {
      const el = document.createElement('script');
      el.type = 'application/json';
      el.id = 'gn-plugin-context';
      el.textContent = JSON.stringify({ slug: '..%2Fadmin' });
      document.head.appendChild(el);
      expect(getSlug()).toBeNull();
    });
  });

  describe('strategy: window.__GN_PLUGIN_SLUG__ override', () => {
    it('reads from the global', () => {
      (globalThis as { __GN_PLUGIN_SLUG__?: unknown }).__GN_PLUGIN_SLUG__ =
        'dev-plugin';
      expect(getSlug()).toBe('dev-plugin');
    });

    it('rejects a non-string', () => {
      (globalThis as { __GN_PLUGIN_SLUG__?: unknown }).__GN_PLUGIN_SLUG__ = 42;
      expect(getSlug()).toBeNull();
    });

    it('rejects a slug that fails the pattern', () => {
      (globalThis as { __GN_PLUGIN_SLUG__?: unknown }).__GN_PLUGIN_SLUG__ =
        'WithCapitals';
      expect(getSlug()).toBeNull();
    });
  });

  describe('priority', () => {
    it('prefers a <script src> over the global', () => {
      (globalThis as { __GN_PLUGIN_SLUG__?: unknown }).__GN_PLUGIN_SLUG__ =
        'fallback';
      const tag = document.createElement('script');
      tag.src = '/api/plugins/winner/web/main.mjs';
      tag.setAttribute('data-gn-test', '1');
      document.head.appendChild(tag);
      expect(getSlug()).toBe('winner');
    });
  });

  it('memoizes the detection', () => {
    (globalThis as { __GN_PLUGIN_SLUG__?: unknown }).__GN_PLUGIN_SLUG__ =
      'cached';
    expect(getSlug()).toBe('cached');
    // Mutating the global after first detection has no effect:
    (globalThis as { __GN_PLUGIN_SLUG__?: unknown }).__GN_PLUGIN_SLUG__ =
      'something-else';
    expect(getSlug()).toBe('cached');
  });
});

describe('requireSlug', () => {
  it('returns the slug when present', () => {
    setSlug('present');
    expect(requireSlug()).toBe('present');
  });

  it('throws SlugRequiredError when missing', () => {
    expect(() => requireSlug()).toThrow(SlugRequiredError);
  });
});

describe('setSlug', () => {
  it('accepts a valid slug', () => {
    setSlug('valid-slug-1');
    expect(getSlug()).toBe('valid-slug-1');
  });

  it('clears the cache on null', () => {
    setSlug('first');
    setSlug(null);
    expect(getSlug()).toBeNull();
  });

  it('rejects a malformed slug', () => {
    expect(() => setSlug('Bad Slug')).toThrow(TypeError);
  });

  it('rejects an empty slug', () => {
    expect(() => setSlug('')).toThrow(TypeError);
  });
});
