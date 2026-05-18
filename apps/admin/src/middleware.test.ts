/**
 * Tests for the admin Next.js middleware.
 *
 * The middleware lives at `apps/admin/middleware.ts` (Next convention).
 * We import it from there directly; vitest's resolution handles the
 * `next/server` import via the existing dependency graph.
 *
 * Coverage focus:
 *   - The emitted CSP header carries every required directive,
 *     including the Trusted Types pair.
 *   - A fresh per-request nonce is folded into script-src and style-src.
 *   - The X-Script-Nonce mirror is set so server components can read it.
 *
 * jsdom doesn't ship `crypto.getRandomValues` by default but the
 * Node runtime does — Vitest exposes the Node `crypto` global to
 * tests, so we don't need a polyfill.
 */
import { describe, expect, it } from 'vitest';
import { NextRequest } from 'next/server';
import { middleware } from '../middleware';

function newRequest(): NextRequest {
  return new NextRequest('http://admin.test/');
}

describe('admin middleware — CSP', () => {
  it('emits a Content-Security-Policy header on every response', () => {
    const res = middleware(newRequest());
    expect(res.headers.get('Content-Security-Policy')).toBeTruthy();
  });

  it('includes require-trusted-types-for and gn-plugin policy name', () => {
    const csp = middleware(newRequest()).headers.get('Content-Security-Policy')!;
    expect(csp).toMatch(/require-trusted-types-for 'script'/);
    expect(csp).toMatch(/trusted-types[^;]*\bgn-plugin\b/);
  });

  it('forbids framing the admin (frame-ancestors none)', () => {
    const csp = middleware(newRequest()).headers.get('Content-Security-Policy')!;
    expect(csp).toMatch(/frame-ancestors 'none'/);
  });

  it('forbids plugins / Flash (object-src none)', () => {
    const csp = middleware(newRequest()).headers.get('Content-Security-Policy')!;
    expect(csp).toMatch(/object-src 'none'/);
  });

  it('does not emit unsafe-inline or unsafe-eval', () => {
    const csp = middleware(newRequest()).headers.get('Content-Security-Policy')!;
    expect(csp).not.toContain("'unsafe-inline'");
    expect(csp).not.toContain("'unsafe-eval'");
  });

  it('folds a fresh nonce into script-src and style-src on every call', () => {
    const first = middleware(newRequest()).headers.get('Content-Security-Policy')!;
    const second = middleware(newRequest()).headers.get('Content-Security-Policy')!;
    const nonceRe = /'nonce-([A-Za-z0-9+/=]+)'/g;
    const firstNonces = Array.from(first.matchAll(nonceRe), (m) => m[1]);
    const secondNonces = Array.from(second.matchAll(nonceRe), (m) => m[1]);
    // Each response includes a nonce in both script-src and style-src.
    expect(firstNonces.length).toBeGreaterThanOrEqual(2);
    expect(secondNonces.length).toBeGreaterThanOrEqual(2);
    // All occurrences in a single response use the SAME nonce.
    expect(new Set(firstNonces).size).toBe(1);
    expect(new Set(secondNonces).size).toBe(1);
    // Two consecutive responses use DIFFERENT nonces.
    expect(firstNonces[0]).not.toBe(secondNonces[0]);
  });

  it('mirrors the per-request nonce on X-Script-Nonce', () => {
    const res = middleware(newRequest());
    const csp = res.headers.get('Content-Security-Policy')!;
    const nonceHeader = res.headers.get('X-Script-Nonce')!;
    expect(nonceHeader).toBeTruthy();
    expect(csp).toContain(`'nonce-${nonceHeader}'`);
  });

  it('emits upgrade-insecure-requests', () => {
    const csp = middleware(newRequest()).headers.get('Content-Security-Policy')!;
    expect(csp).toContain('upgrade-insecure-requests');
  });
});
