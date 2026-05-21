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
 *   - The first-run gate: when /api/v1/setup/status reports the install
 *     lock is open, admin paths are redirected to /setup; the wizard's
 *     own routes are never redirected (no loop).
 *
 * jsdom doesn't ship `crypto.getRandomValues` by default but the
 * Node runtime does — Vitest exposes the Node `crypto` global to
 * tests, so we don't need a polyfill.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { NextRequest } from 'next/server';
import { middleware } from '../middleware';

function newRequest(path: string = '/'): NextRequest {
  return new NextRequest(`http://admin.test${path}`);
}

/** Helper: stub fetch to return whatever /setup/status payload the test wants. */
function stubStatusFetch(body: {
  installation_completed: boolean;
  user_count?: number;
}): void {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(
      JSON.stringify({ user_count: 0, ...body }),
      { status: 200, headers: { 'content-type': 'application/json' } },
    ),
  );
}

// The middleware now probes /api/v1/setup/status to decide the install
// gate. The CSP suite below pre-stubs that fetch with
// `installation_completed: true` so the CSP assertions don't trip the
// redirect path; the dedicated setup-gate tests override the stub with
// the per-case payload they need.
beforeEach(() => {
  stubStatusFetch({ installation_completed: true, user_count: 1 });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('admin middleware — CSP', () => {
  it('emits a Content-Security-Policy header on every response', async () => {
    const res = await middleware(newRequest());
    expect(res.headers.get('Content-Security-Policy')).toBeTruthy();
  });

  it('includes require-trusted-types-for and gn-plugin policy name', async () => {
    const csp = (await middleware(newRequest())).headers.get('Content-Security-Policy')!;
    expect(csp).toMatch(/require-trusted-types-for 'script'/);
    expect(csp).toMatch(/trusted-types[^;]*\bgn-plugin\b/);
  });

  it('forbids framing the admin (frame-ancestors none)', async () => {
    const csp = (await middleware(newRequest())).headers.get('Content-Security-Policy')!;
    expect(csp).toMatch(/frame-ancestors 'none'/);
  });

  it('forbids plugins / Flash (object-src none)', async () => {
    const csp = (await middleware(newRequest())).headers.get('Content-Security-Policy')!;
    expect(csp).toMatch(/object-src 'none'/);
  });

  it('does not emit unsafe-inline or unsafe-eval', async () => {
    const csp = (await middleware(newRequest())).headers.get('Content-Security-Policy')!;
    expect(csp).not.toContain("'unsafe-inline'");
    expect(csp).not.toContain("'unsafe-eval'");
  });

  it('folds a fresh nonce into script-src and style-src on every call', async () => {
    const first = (await middleware(newRequest())).headers.get('Content-Security-Policy')!;
    const second = (await middleware(newRequest())).headers.get('Content-Security-Policy')!;
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

  it('mirrors the per-request nonce on X-Script-Nonce', async () => {
    const res = await middleware(newRequest());
    const csp = res.headers.get('Content-Security-Policy')!;
    const nonceHeader = res.headers.get('X-Script-Nonce')!;
    expect(nonceHeader).toBeTruthy();
    expect(csp).toContain(`'nonce-${nonceHeader}'`);
  });

  it('emits upgrade-insecure-requests', async () => {
    const csp = (await middleware(newRequest())).headers.get('Content-Security-Policy')!;
    expect(csp).toContain('upgrade-insecure-requests');
  });
});

describe('admin middleware — first-run install gate', () => {
  it('redirects an admin URL to /setup when the install lock is open', async () => {
    stubStatusFetch({ installation_completed: false, user_count: 0 });
    const res = await middleware(newRequest('/'));
    expect(res.status).toBeGreaterThanOrEqual(300);
    expect(res.status).toBeLessThan(400);
    const location = res.headers.get('Location');
    expect(location).toBeTruthy();
    expect(location!).toContain('/setup');
  });

  it('redirects a nested admin URL too', async () => {
    stubStatusFetch({ installation_completed: false, user_count: 0 });
    const res = await middleware(newRequest('/posts'));
    expect(res.headers.get('Location')!).toContain('/setup');
  });

  it('does NOT redirect /setup itself (no loop)', async () => {
    stubStatusFetch({ installation_completed: false, user_count: 0 });
    const res = await middleware(newRequest('/setup'));
    // .next/redirect responses carry a Location header; a normal pass-through
    // doesn't. Asserting the absence of Location is the cleanest signal that
    // the gate was skipped.
    expect(res.headers.get('Location')).toBeNull();
    expect(res.headers.get('Content-Security-Policy')).toBeTruthy();
  });

  it('does NOT redirect /setup sub-routes', async () => {
    stubStatusFetch({ installation_completed: false, user_count: 0 });
    const res = await middleware(newRequest('/setup/done'));
    expect(res.headers.get('Location')).toBeNull();
  });

  it('does NOT redirect when the install is already completed', async () => {
    stubStatusFetch({ installation_completed: true, user_count: 1 });
    const res = await middleware(newRequest('/'));
    expect(res.headers.get('Location')).toBeNull();
  });

  it('fails open if /setup/status is unreachable', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('econn'));
    const res = await middleware(newRequest('/'));
    // No redirect — render whatever the admin's own login / route does.
    expect(res.headers.get('Location')).toBeNull();
    // CSP still stamped.
    expect(res.headers.get('Content-Security-Policy')).toBeTruthy();
  });
});
