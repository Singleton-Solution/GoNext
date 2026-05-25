/**
 * Next.js edge middleware for @gonext/admin.
 *
 * Responsibilities, in the order they fire on every request:
 *
 *   1. **Install-lock gate** — before anything else, probe
 *      `/api/v1/setup/status`. If the deployment has no users yet
 *      (`installation_completed: false`) AND the requested path isn't
 *      already the setup wizard, redirect to /setup. This applies to
 *      /login too: a brand-new install must reach the wizard, not the
 *      sign-in form. Fails OPEN on any network / parse error — the
 *      wizard would otherwise be unreachable when the API is down.
 *
 *   2. **Auth gate** — for paths in the authenticated route group
 *      (everything that isn't /login, /setup, or /setup/*) without
 *      a session cookie, 302 to `/login?next=<original-path>`. The
 *      session cookie name is read from `GONEXT_SESSION_COOKIE_NAME`
 *      with a sensible default. The redirect target keeps the
 *      `next` param so the login form can bounce back to the originally
 *      requested URL after a successful sign-in.
 *
 *   3. **CSP** — stamp the canonical Content-Security-Policy header,
 *      including a fresh per-request nonce mirrored on X-Script-Nonce.
 *      Mirrors the AdminPolicy preset in
 *      packages/go/middleware/csp/preset.go so the dev path (Next.js
 *      alone) matches the prod path (Go reverse-proxy injection).
 *
 * Why edge-middleware-not-next.config: `next.config.ts`'s `headers()`
 * cannot inject a per-request nonce, and the canonical CSP shape uses
 * a fresh nonce per response (see security-baseline §3.2). Middleware
 * has access to a real `Request`, can derive a nonce, and can mirror
 * it via `Content-Security-Policy` AND `X-Script-Nonce` so the React
 * tree can read it during SSR.
 *
 * The matcher excludes the static asset paths Next.js owns so the
 * header doesn't leak onto fonts / images / Next runtime chunks where
 * it would be either redundant or harmful (e.g. blocking the inline
 * Webpack runtime).
 */
import { NextResponse, type NextRequest } from 'next/server';

/**
 * Names of the Trusted Types policies the admin allows. MUST mirror the
 * `AdminStrictPolicy` preset in
 * `packages/go/middleware/csp/preset.go::AdminStrictPolicy` so the dev
 * path (Next.js alone) and prod path (Go reverse proxy) emit IDENTICAL
 * headers — drift between the two is silently catastrophic, because
 * every assignment to a DOM sink THROWS once the directive is in
 * force.
 *
 * Policy names emitted (issue #59):
 *
 *   - `gn-admin`           — the admin chrome's setHTML/setURL helpers
 *     in `apps/admin/src/lib/trusted-types.ts`. Used by the sidebar
 *     search, comment moderation previews, and any other surface that
 *     displays server-supplied HTML.
 *
 *   - `gn-editor`          — the block editor's icon and rich-content
 *     sinks in `packages/ts/blocks-editor/src/trusted-types.ts`.
 *
 *   - `'allow-duplicates'` — the CSP keyword that permits the same
 *     policy name to be registered twice without throwing. Required in
 *     dev because Next.js Fast Refresh re-evaluates modules, and in
 *     prod because the admin host and a plugin frontend may both
 *     attempt to install `gn-editor` independently.
 *
 * Intentionally NOT listed:
 *
 *   - `nextjs#bundler` / `default` — the Next.js bundler's per-request
 *     nonce + `'strict-dynamic'` chain (see `script-src` below) is the
 *     modern strict pattern recommended by CSP3 §6.1. Listing a bundler
 *     policy would defeat the point of strict-dynamic.
 *
 *   - `gn-plugin` — plugin-contributed JS loads through
 *     `@gonext/plugin-frontend-host` on a separate surface that owns
 *     its own CSP; the admin's strict policy intentionally rejects
 *     plugin-minted HTML. Plugins that need to render rich content in
 *     the admin must round-trip through the `gn-editor` policy
 *     (DOMPurify-sanitized) instead.
 */
const TRUSTED_TYPES_POLICIES = [
  'gn-admin',
  'gn-editor',
  "'allow-duplicates'",
] as const;

/**
 * Default name of the cookie that carries the admin session. The Go
 * auth handler issues this cookie; the middleware checks for its
 * presence to decide whether a visitor is logged in. Override via
 * `GONEXT_SESSION_COOKIE_NAME` if the deployment renames it (e.g.
 * to namespace multiple GoNext instances on the same parent domain).
 */
const DEFAULT_SESSION_COOKIE_NAME = 'gonext_session';

function sessionCookieName(): string {
  return process.env.GONEXT_SESSION_COOKIE_NAME ?? DEFAULT_SESSION_COOKIE_NAME;
}

/**
 * Generates a 16-byte random nonce, base64-encoded. The nonce
 * production code uses lives in the Go security middleware; this is
 * the Next-side equivalent for dev / standalone runs where the proxy
 * isn't present.
 */
function generateNonce(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  // Edge runtime has Buffer-less environments; use the manual encoder.
  let binary = '';
  for (let i = 0; i < bytes.length; i++) {
    binary += String.fromCharCode(bytes[i]!);
  }
  // btoa is available in the edge runtime.
  return btoa(binary);
}

/**
 * Builds the strict admin CSP string with the supplied per-request
 * nonce folded into script-src and style-src (issue #59). The output
 * mirrors the AdminStrictPolicy preset in
 * packages/go/middleware/csp/preset.go:
 *
 *   default-src 'self'
 *   script-src 'self' 'nonce-…' 'strict-dynamic'
 *   style-src 'self' 'nonce-…'
 *   img-src 'self' data: blob:
 *   font-src 'self' data:
 *   connect-src 'self'
 *   frame-src 'self'
 *   media-src 'self' blob:
 *   frame-ancestors 'none'
 *   form-action 'self'
 *   base-uri 'self'
 *   object-src 'none'
 *   worker-src 'self' blob:
 *   manifest-src 'self'
 *   upgrade-insecure-requests
 *   require-trusted-types-for 'script'
 *   trusted-types gn-admin gn-editor 'allow-duplicates'
 *
 * Why `'strict-dynamic'` — once the per-request nonce authorizes the
 * Next.js root bundle, `'strict-dynamic'` extends that trust transitively
 * to every script the bundle loads (App Router chunks, async boundaries,
 * etc.) without us having to enumerate them. This closes the
 * host-allowlist bypass class that older CSPs were vulnerable to.
 *
 * Why `'allow-duplicates'` in trusted-types — dev's Fast-Refresh reloads
 * re-execute module-init code, which re-runs `installAdminPolicy()`.
 * Without `'allow-duplicates'` the browser would throw on the second
 * registration. In prod the keyword is harmless: each policy is still
 * functionally identical to the first.
 */
function buildCSP(nonce: string): string {
  const nonceSource = `'nonce-${nonce}'`;
  const directives: Array<[string, string[]] | string> = [
    ['default-src', ["'self'"]],
    // 'strict-dynamic' transitively trusts scripts loaded by the nonced
    // root bundle — see issue #59 acceptance criteria.
    ['script-src', ["'self'", nonceSource, "'strict-dynamic'"]],
    ['style-src', ["'self'", nonceSource]],
    ['img-src', ["'self'", 'data:', 'blob:']],
    ['font-src', ["'self'", 'data:']],
    ['connect-src', ["'self'"]],
    ['frame-src', ["'self'"]],
    ['media-src', ["'self'", 'blob:']],
    ['frame-ancestors', ["'none'"]],
    ['form-action', ["'self'"]],
    ['base-uri', ["'self'"]],
    ['object-src', ["'none'"]],
    ['worker-src', ["'self'", 'blob:']],
    ['manifest-src', ["'self'"]],
    'upgrade-insecure-requests',
    ['require-trusted-types-for', ["'script'"]],
    ['trusted-types', [...TRUSTED_TYPES_POLICIES]],
  ];

  const parts = directives.map((d) =>
    typeof d === 'string' ? d : `${d[0]} ${d[1].join(' ')}`,
  );
  return parts.join('; ');
}

/**
 * Resolves the API base URL for the SSR-side install-status probe. The
 * client-facing var is NEXT_PUBLIC_API_URL; in-cluster SSR fetches use
 * GONEXT_API_URL when the admin pod talks to the API service over a
 * service-internal name. Both default to http://localhost:8080 so a
 * `make up` developer doesn't need to set anything.
 */
function apiBaseURL(): string {
  return (
    process.env.GONEXT_API_URL ??
    process.env.NEXT_PUBLIC_API_URL ??
    'http://localhost:8080'
  );
}

/**
 * Shape of the `/api/v1/setup/status` JSON response. The API may
 * surface the completion marker on one of two option keys depending
 * on which migration has landed:
 *
 *   - `core.installation_completed_at`       (K1, original layout)
 *   - `core.site.installation_completed_at`  (K5 site-namespaced layout)
 *
 * The handler can also surface a top-level boolean
 * `installation_completed` derived from either key. We accept ALL THREE
 * signals because the K1 → K5 migration (issue L3) may land
 * independently of this PR, and we don't want the admin to flip into a
 * spurious "uninstalled" state for the brief window where one side has
 * shipped and the other hasn't.
 */
interface SetupStatusResponse {
  installation_completed?: boolean;
  options?: {
    'core.installation_completed_at'?: string | null;
    'core.site.installation_completed_at'?: string | null;
  };
}

/**
 * True iff the API reports the install lock is OPEN (the wizard should
 * be reached). We treat the install as completed if ANY of:
 *
 *   1. `installation_completed: true` at the response top level, OR
 *   2. `options['core.installation_completed_at']` is a non-empty string, OR
 *   3. `options['core.site.installation_completed_at']` is a non-empty string.
 *
 * Any network failure or unexpected shape returns `false` — the
 * middleware fails OPEN on the install probe because the wizard itself
 * shows a clearer error than a redirect loop would.
 */
async function isUninstalled(): Promise<boolean> {
  try {
    const res = await fetch(`${apiBaseURL()}/api/v1/setup/status`, {
      method: 'GET',
      cache: 'no-store',
      headers: { Accept: 'application/json' },
    });
    if (!res.ok) return false;
    const json = (await res.json()) as SetupStatusResponse;

    if (json.installation_completed === true) return false;

    const opts = json.options;
    if (opts) {
      const k1 = opts['core.installation_completed_at'];
      const k5 = opts['core.site.installation_completed_at'];
      if ((typeof k1 === 'string' && k1.length > 0) ||
          (typeof k5 === 'string' && k5.length > 0)) {
        return false;
      }
    }

    // If the top-level boolean is explicitly false, the install is open.
    if (json.installation_completed === false) return true;

    // Ambiguous payload (no boolean, no option keys) — fail OPEN so the
    // admin remains reachable. An honest "no users yet" deployment will
    // surface either the boolean or the option key.
    return false;
  } catch {
    return false;
  }
}

/**
 * True for paths that should be redirected to /setup when the install
 * lock is open. The wizard's own routes never gate (no redirect loop)
 * — every other path does, including /login, because a brand-new
 * install has no user to sign in as.
 */
function shouldGateForSetup(pathname: string): boolean {
  if (pathname === '/setup' || pathname.startsWith('/setup/')) return false;
  return true;
}

/**
 * True for paths that require an authenticated session (the
 * `(authenticated)` route group). These are everything that isn't an
 * unauthenticated surface — currently /login, /setup, /setup/*. As
 * the public surface grows (e.g. /forgot-password, /verify-email)
 * extend the allowlist here.
 *
 * Returns true for the dashboard root (/) too: it lives under the
 * authenticated layout and must redirect to /login when no session
 * cookie is present.
 */
function isAuthenticatedPath(pathname: string): boolean {
  if (pathname === '/login' || pathname.startsWith('/login/')) return false;
  if (pathname === '/setup' || pathname.startsWith('/setup/')) return false;
  if (pathname === '/forgot-password' || pathname.startsWith('/forgot-password/'))
    return false;
  if (pathname === '/verify-email' || pathname.startsWith('/verify-email/'))
    return false;
  return true;
}

/**
 * Builds the /login?next=<path> redirect URL. The `next` param is the
 * pathname (plus query string) of the original request; the login
 * page reads it after a successful sign-in to bounce back to wherever
 * the user was trying to reach.
 */
function loginRedirect(request: NextRequest): URL {
  const next = `${request.nextUrl.pathname}${request.nextUrl.search ?? ''}`;
  const target = new URL('/login', request.nextUrl);
  target.searchParams.set('next', next);
  return target;
}

export async function middleware(request: NextRequest): Promise<NextResponse> {
  const nonce = generateNonce();

  // Step 1: install-lock gate. If the deployment has no users yet AND
  // the requested path isn't already the setup wizard, redirect there
  // before doing anything else. The CSP header still rides along on
  // the redirect response so the wizard's own page inherits the same
  // strict policy. This runs BEFORE the auth gate so an uninstalled
  // deployment never bounces visitors through /login on the way to
  // /setup (which would also have flashed the empty signed-out chrome).
  if (shouldGateForSetup(request.nextUrl.pathname)) {
    const uninstalled = await isUninstalled();
    if (uninstalled) {
      const target = new URL('/setup', request.nextUrl);
      const redirect = NextResponse.redirect(target);
      redirect.headers.set('Content-Security-Policy', buildCSP(nonce));
      redirect.headers.set('X-Script-Nonce', nonce);
      return redirect;
    }
  }

  // Step 2: auth gate. For authenticated paths without a session
  // cookie, redirect to /login?next=<original>. The auth handler on
  // the API side sets the cookie under whatever name we read here
  // (default `gonext_session`); the middleware only checks presence.
  // Cookie *validity* is enforced by the API on the next call — at
  // this layer we just want to keep the chrome from leaking to logged-
  // out visitors.
  if (isAuthenticatedPath(request.nextUrl.pathname)) {
    const cookie = request.cookies.get(sessionCookieName());
    if (!cookie || !cookie.value) {
      const target = loginRedirect(request);
      const redirect = NextResponse.redirect(target);
      redirect.headers.set('Content-Security-Policy', buildCSP(nonce));
      redirect.headers.set('X-Script-Nonce', nonce);
      return redirect;
    }
  }

  // Step 3: pass-through with CSP stamped.
  const response = NextResponse.next({
    request: {
      headers: (() => {
        const h = new Headers(request.headers);
        // Pass the nonce down to the React tree so server components
        // can stamp it onto nonced inline scripts. Mirrors what the
        // Go security.WithNonce middleware writes for the API host.
        h.set('x-nonce', nonce);
        return h;
      })(),
    },
  });

  response.headers.set('Content-Security-Policy', buildCSP(nonce));
  response.headers.set('X-Script-Nonce', nonce);
  return response;
}

/**
 * Matcher: apply CSP to every page response, but skip Next's internal
 * `_next/static` and `_next/image` paths and the favicon. The proxy
 * (production) sets equivalent headers for those, and the Next-managed
 * inline runtime in `_next/static` would be unnecessarily restricted
 * here.
 */
export const config = {
  matcher: [
    // Negative-lookahead matcher mirroring Next.js's documented pattern.
    '/((?!_next/static|_next/image|favicon.ico|api/).*)',
  ],
};
