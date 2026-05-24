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
 *   - The dual completion signal: both `core.installation_completed_at`
 *     and `core.site.installation_completed_at` count as "installed".
 *   - The auth gate: authenticated paths without a session cookie are
 *     redirected to /login?next=<path>; unauthenticated surfaces are
 *     not gated; the cookie name is read from
 *     `GONEXT_SESSION_COOKIE_NAME` when set.
 *
 * jsdom doesn't ship `crypto.getRandomValues` by default but the
 * Node runtime does — Vitest exposes the Node `crypto` global to
 * tests, so we don't need a polyfill.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { NextRequest } from 'next/server';
import { middleware } from '../middleware';

/**
 * Build a NextRequest that already carries a session cookie. Used in
 * the suites where we want to focus on something other than the auth
 * gate (CSP, the setup gate) and need to bypass the redirect to /login.
 */
function newAuthedRequest(path: string = '/'): NextRequest {
  const req = new NextRequest(`http://admin.test${path}`);
  req.cookies.set('gonext_session', 'test-session-value');
  return req;
}

/**
 * Build a NextRequest with NO cookies — used for auth-gate tests where
 * the absence of the session is the whole point.
 */
function newUnauthedRequest(path: string = '/'): NextRequest {
  return new NextRequest(`http://admin.test${path}`);
}

/** Helper: stub fetch to return whatever /setup/status payload the test wants. */
function stubStatusFetch(body: Record<string, unknown>): void {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(
      JSON.stringify(body),
      { status: 200, headers: { 'content-type': 'application/json' } },
    ),
  );
}

// The middleware probes /api/v1/setup/status to decide the install
// gate. The CSP / auth suites below pre-stub that fetch with
// `installation_completed: true` so those assertions don't trip the
// redirect path; the dedicated setup-gate tests override the stub with
// the per-case payload they need.
beforeEach(() => {
  stubStatusFetch({ installation_completed: true, user_count: 1 });
});

afterEach(() => {
  vi.restoreAllMocks();
  delete process.env.GONEXT_SESSION_COOKIE_NAME;
});

describe('admin middleware — CSP', () => {
  it('emits a Content-Security-Policy header on every response', async () => {
    const res = await middleware(newAuthedRequest());
    expect(res.headers.get('Content-Security-Policy')).toBeTruthy();
  });

  it('includes require-trusted-types-for and gn-plugin policy name', async () => {
    const csp = (await middleware(newAuthedRequest())).headers.get('Content-Security-Policy')!;
    expect(csp).toMatch(/require-trusted-types-for 'script'/);
    expect(csp).toMatch(/trusted-types[^;]*\bgn-plugin\b/);
  });

  it('forbids framing the admin (frame-ancestors none)', async () => {
    const csp = (await middleware(newAuthedRequest())).headers.get('Content-Security-Policy')!;
    expect(csp).toMatch(/frame-ancestors 'none'/);
  });

  it('forbids plugins / Flash (object-src none)', async () => {
    const csp = (await middleware(newAuthedRequest())).headers.get('Content-Security-Policy')!;
    expect(csp).toMatch(/object-src 'none'/);
  });

  it('does not emit unsafe-inline or unsafe-eval', async () => {
    const csp = (await middleware(newAuthedRequest())).headers.get('Content-Security-Policy')!;
    expect(csp).not.toContain("'unsafe-inline'");
    expect(csp).not.toContain("'unsafe-eval'");
  });

  it('folds a fresh nonce into script-src and style-src on every call', async () => {
    const first = (await middleware(newAuthedRequest())).headers.get('Content-Security-Policy')!;
    const second = (await middleware(newAuthedRequest())).headers.get('Content-Security-Policy')!;
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
    const res = await middleware(newAuthedRequest());
    const csp = res.headers.get('Content-Security-Policy')!;
    const nonceHeader = res.headers.get('X-Script-Nonce')!;
    expect(nonceHeader).toBeTruthy();
    expect(csp).toContain(`'nonce-${nonceHeader}'`);
  });

  it('emits upgrade-insecure-requests', async () => {
    const csp = (await middleware(newAuthedRequest())).headers.get('Content-Security-Policy')!;
    expect(csp).toContain('upgrade-insecure-requests');
  });
});

describe('admin middleware — first-run install gate', () => {
  it('redirects an admin URL to /setup when the install lock is open', async () => {
    stubStatusFetch({ installation_completed: false, user_count: 0 });
    const res = await middleware(newAuthedRequest('/'));
    expect(res.status).toBeGreaterThanOrEqual(300);
    expect(res.status).toBeLessThan(400);
    const location = res.headers.get('Location');
    expect(location).toBeTruthy();
    expect(location!).toContain('/setup');
  });

  it('redirects a nested admin URL too', async () => {
    stubStatusFetch({ installation_completed: false, user_count: 0 });
    const res = await middleware(newAuthedRequest('/posts'));
    expect(res.headers.get('Location')!).toContain('/setup');
  });

  it('does NOT redirect /setup itself (no loop)', async () => {
    stubStatusFetch({ installation_completed: false, user_count: 0 });
    const res = await middleware(newUnauthedRequest('/setup'));
    // .next/redirect responses carry a Location header; a normal pass-through
    // doesn't. Asserting the absence of Location is the cleanest signal that
    // the gate was skipped.
    expect(res.headers.get('Location')).toBeNull();
    expect(res.headers.get('Content-Security-Policy')).toBeTruthy();
  });

  it('does NOT redirect /setup sub-routes', async () => {
    stubStatusFetch({ installation_completed: false, user_count: 0 });
    const res = await middleware(newUnauthedRequest('/setup/done'));
    expect(res.headers.get('Location')).toBeNull();
  });

  it('does NOT redirect when the install is already completed', async () => {
    stubStatusFetch({ installation_completed: true, user_count: 1 });
    const res = await middleware(newAuthedRequest('/'));
    expect(res.headers.get('Location')).toBeNull();
  });

  it('fails open if /setup/status is unreachable', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('econn'));
    const res = await middleware(newAuthedRequest('/'));
    // No redirect — render whatever the admin's own login / route does.
    expect(res.headers.get('Location')).toBeNull();
    // CSP still stamped.
    expect(res.headers.get('Content-Security-Policy')).toBeTruthy();
  });

  it('redirects /login to /setup when uninstalled (no flashed login form)', async () => {
    stubStatusFetch({ installation_completed: false, user_count: 0 });
    const res = await middleware(newUnauthedRequest('/login'));
    expect(res.headers.get('Location')!).toContain('/setup');
  });

  it('accepts core.installation_completed_at (K1) as a completion signal', async () => {
    // No top-level boolean, only the K1 option key — the install IS
    // completed and the admin must not be sent through the wizard.
    stubStatusFetch({
      options: { 'core.installation_completed_at': '2026-05-01T00:00:00Z' },
    });
    const res = await middleware(newAuthedRequest('/'));
    expect(res.headers.get('Location')).toBeNull();
  });

  it('accepts core.site.installation_completed_at (K5) as a completion signal', async () => {
    // Same as above but the K5 site-namespaced key — handles the
    // case where L3 lands the namespaced layout separately.
    stubStatusFetch({
      options: { 'core.site.installation_completed_at': '2026-05-01T00:00:00Z' },
    });
    const res = await middleware(newAuthedRequest('/'));
    expect(res.headers.get('Location')).toBeNull();
  });

  it('treats an empty option-key string as "not completed"', async () => {
    // Defensive: an empty timestamp is not a completion proof.
    stubStatusFetch({
      installation_completed: false,
      options: {
        'core.installation_completed_at': '',
        'core.site.installation_completed_at': '',
      },
    });
    const res = await middleware(newAuthedRequest('/'));
    expect(res.headers.get('Location')!).toContain('/setup');
  });
});

describe('admin middleware — auth gate', () => {
  it('redirects an authenticated path with no session cookie to /login', async () => {
    const res = await middleware(newUnauthedRequest('/'));
    const location = res.headers.get('Location');
    expect(location).toBeTruthy();
    expect(location!).toContain('/login');
  });

  it('preserves the original path on the next= query param', async () => {
    const res = await middleware(newUnauthedRequest('/posts'));
    const location = res.headers.get('Location')!;
    const url = new URL(location);
    expect(url.pathname).toBe('/login');
    expect(url.searchParams.get('next')).toBe('/posts');
  });

  it('preserves the search string on the next= param', async () => {
    const res = await middleware(newUnauthedRequest('/posts?status=draft'));
    const url = new URL(res.headers.get('Location')!);
    expect(url.searchParams.get('next')).toBe('/posts?status=draft');
  });

  it('redirects nested authenticated paths', async () => {
    const res = await middleware(newUnauthedRequest('/settings/general'));
    const url = new URL(res.headers.get('Location')!);
    expect(url.pathname).toBe('/login');
    expect(url.searchParams.get('next')).toBe('/settings/general');
  });

  it('does NOT redirect /login itself', async () => {
    const res = await middleware(newUnauthedRequest('/login'));
    expect(res.headers.get('Location')).toBeNull();
  });

  it('does NOT redirect /setup', async () => {
    const res = await middleware(newUnauthedRequest('/setup'));
    expect(res.headers.get('Location')).toBeNull();
  });

  it('passes through an authenticated path WITH a session cookie', async () => {
    const res = await middleware(newAuthedRequest('/posts'));
    expect(res.headers.get('Location')).toBeNull();
    expect(res.headers.get('Content-Security-Policy')).toBeTruthy();
  });

  it('honors GONEXT_SESSION_COOKIE_NAME when set', async () => {
    process.env.GONEXT_SESSION_COOKIE_NAME = 'custom_session';
    // The default name is now NOT what the middleware looks for.
    const req = new NextRequest('http://admin.test/');
    req.cookies.set('gonext_session', 'wrong-cookie');
    const res = await middleware(req);
    // Without the *configured* cookie, it must still redirect.
    expect(res.headers.get('Location')!).toContain('/login');

    // With the configured cookie name, the request passes through.
    const req2 = new NextRequest('http://admin.test/');
    req2.cookies.set('custom_session', 'right-cookie');
    const res2 = await middleware(req2);
    expect(res2.headers.get('Location')).toBeNull();
  });

  it('runs the setup gate BEFORE the auth gate', async () => {
    // When uninstalled, even an unauthenticated visitor to /posts must
    // land on /setup, not /login. Otherwise a fresh deployment shows
    // the sign-in form first, which has no users to authenticate
    // against.
    stubStatusFetch({ installation_completed: false, user_count: 0 });
    const res = await middleware(newUnauthedRequest('/posts'));
    expect(res.headers.get('Location')!).toContain('/setup');
  });
});
