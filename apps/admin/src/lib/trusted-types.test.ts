/**
 * Tests for the admin Trusted Types policies + DOM helpers
 * (apps/admin/src/lib/trusted-types.ts).
 *
 * Coverage focus:
 *
 *   - `installAdminPolicy` / `installEditorPolicy` register the spec-shaped
 *     policy when `window.trustedTypes.createPolicy` is available; both are
 *     idempotent.
 *   - In SSR / jsdom (no `trustedTypes` global) the helpers fall back to a
 *     shim that STILL sanitizes via DOMPurify — the security guarantee is
 *     preserved even before the browser policy lands.
 *   - `setHTML` writes sanitized output into `el.innerHTML`. A
 *     `<script>onerror=…>` payload is stripped before assignment.
 *   - `setHTML` is a no-op when `el === null` so call sites can guard on a
 *     `useRef` without explicit null checks.
 *   - `setURL` mints a TrustedScriptURL only on the script-loading
 *     attributes (`<script src>`, `<link href>`); other attributes pass
 *     through. Pseudo-schemes (`javascript:`, `data:`, `vbscript:`) are
 *     rejected.
 *   - `sanitizedHTML` returns a shape suitable for the legacy
 *     `dangerouslySetInnerHTML` escape-hatch.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  GN_ADMIN_POLICY_NAME,
  GN_EDITOR_POLICY_NAME,
  __resetPoliciesForTests,
  installAdminPolicy,
  installEditorPolicy,
  sanitizedHTML,
  setHTML,
  setURL,
} from './trusted-types';

afterEach(() => {
  __resetPoliciesForTests();
  // Remove any test-installed trustedTypes mock.
  delete (globalThis as { trustedTypes?: unknown }).trustedTypes;
  vi.restoreAllMocks();
});

describe('installAdminPolicy / installEditorPolicy — registration', () => {
  it('returns a shim when trustedTypes is unavailable (SSR / older browser)', () => {
    // No trustedTypes stub installed → fallback path.
    const policy = installAdminPolicy();
    expect(policy).toBeTruthy();
    // The shim still sanitizes input — assigning a script payload returns
    // the cleansed string.
    const cleaned = policy.createHTML('<img src=x onerror=alert(1)>');
    expect(cleaned).not.toContain('onerror');
  });

  it("registers a 'gn-admin' policy when trustedTypes is available", () => {
    const seen: string[] = [];
    (globalThis as { trustedTypes?: unknown }).trustedTypes = {
      createPolicy: (name: string, rules: { createHTML?: (input: string) => string }) => {
        seen.push(name);
        return { createHTML: rules.createHTML!, createScriptURL: () => '' };
      },
    };
    installAdminPolicy();
    expect(seen).toContain(GN_ADMIN_POLICY_NAME);
  });

  it("registers a 'gn-editor' policy when trustedTypes is available", () => {
    const seen: string[] = [];
    (globalThis as { trustedTypes?: unknown }).trustedTypes = {
      createPolicy: (name: string, rules: { createHTML?: (input: string) => string }) => {
        seen.push(name);
        return { createHTML: rules.createHTML!, createScriptURL: () => '' };
      },
    };
    installEditorPolicy();
    expect(seen).toContain(GN_EDITOR_POLICY_NAME);
  });

  it('is idempotent — repeat calls return the same instance', () => {
    const a = installAdminPolicy();
    const b = installAdminPolicy();
    expect(a).toBe(b);
  });

  it('falls back to a shim when createPolicy throws', () => {
    (globalThis as { trustedTypes?: unknown }).trustedTypes = {
      createPolicy: () => {
        throw new Error('duplicate policy');
      },
    };
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const policy = installAdminPolicy();
    // Still functional — the shim runs DOMPurify and returns the cleaned
    // string.
    expect(policy.createHTML('<b>safe</b>')).toContain('<b>');
  });
});

describe('setHTML — sanitization', () => {
  let host: HTMLDivElement;

  beforeEach(() => {
    host = document.createElement('div');
    document.body.appendChild(host);
  });

  afterEach(() => {
    host.remove();
  });

  it('writes sanitized HTML to el.innerHTML', () => {
    setHTML(host, '<p>hello <mark>world</mark></p>');
    expect(host.innerHTML).toContain('<mark>world</mark>');
  });

  it('strips script vectors before assignment', () => {
    setHTML(host, '<img src=x onerror="alert(1)">');
    // onerror handler must be stripped; the <img> may remain but
    // without the event handler attribute.
    expect(host.innerHTML).not.toContain('onerror');
    expect(host.innerHTML).not.toContain('alert(1)');
  });

  it('strips <script> tags', () => {
    setHTML(host, '<div>safe</div><script>alert(1)</script>');
    expect(host.innerHTML).not.toContain('<script');
    expect(host.innerHTML).toContain('safe');
  });

  it("is a no-op when el is null (so callers can pass ref.current without a guard)", () => {
    // Should not throw.
    expect(() => setHTML(null, '<p>x</p>')).not.toThrow();
  });

  it('uses the editor surface when surface="editor" is passed', () => {
    // The editor profile permits inline SVG; the admin profile does NOT
    // permit <style>. Confirm a known divergence: <style> is dropped
    // under both profiles, but inline SVG survives under editor.
    setHTML(host, '<svg><circle cx="5" cy="5" r="2"/></svg>', 'editor');
    expect(host.innerHTML).toContain('<svg');
    expect(host.innerHTML).toContain('<circle');
  });
});

describe('setURL — script URL minting', () => {
  it('passes non-script-URL attributes through unchanged', () => {
    const a = document.createElement('a');
    setURL(a, 'href', '/profile/me');
    expect(a.getAttribute('href')).toBe('/profile/me');
  });

  it('rejects javascript: pseudo-scheme on <script src>', () => {
    const s = document.createElement('script');
    expect(() => setURL(s, 'src', 'javascript:alert(1)')).toThrow(/origin allowlist/);
  });

  it('rejects data: pseudo-scheme on <script src>', () => {
    const s = document.createElement('script');
    expect(() => setURL(s, 'src', 'data:text/javascript,alert(1)')).toThrow(/origin allowlist/);
  });

  it('accepts same-origin relative path on <script src>', () => {
    const s = document.createElement('script');
    setURL(s, 'src', '/static/admin.js');
    expect(s.getAttribute('src')).toBe('/static/admin.js');
  });

  it('rejects protocol-relative URLs (cross-origin bypass)', () => {
    const s = document.createElement('script');
    expect(() => setURL(s, 'src', '//evil.example.com/x.js')).toThrow(/origin allowlist/);
  });

  it("is a no-op when el is null", () => {
    expect(() => setURL(null, 'href', '/x')).not.toThrow();
  });
});

describe('sanitizedHTML — React-compatible escape hatch', () => {
  it('returns a { __html } object that DOMPurify has run over', () => {
    const v = sanitizedHTML('<p>hi</p><script>alert(1)</script>');
    expect(v.__html).toContain('hi');
    expect(v.__html).not.toContain('<script');
  });

  it('routes through the editor profile when requested', () => {
    const v = sanitizedHTML('<svg><circle r="2"/></svg>', 'editor');
    expect(v.__html).toContain('<svg');
  });
});
