/**
 * Next.js edge middleware for @gonext/admin.
 *
 * Responsibility: stamp the canonical Content-Security-Policy header on
 * every response so plugin-contributed JavaScript runs under the
 * strictest browser shape the platform supports.
 *
 * Why edge-middleware-not-next.config: `next.config.ts`'s `headers()`
 * cannot inject a per-request nonce, and the canonical CSP shape uses
 * a fresh nonce per response (see security-baseline §3.2). Middleware
 * has access to a real `Request`, can derive a nonce, and can mirror
 * it via `Content-Security-Policy` AND `X-Script-Nonce` so the React
 * tree can read it during SSR.
 *
 * The directives mirror the AdminPolicy in
 * packages/go/middleware/csp/preset.go — keep both sides in sync so the
 * reverse-proxy-supplied CSP (production) matches what the Next.js
 * server alone emits in dev. The plugin-hardening additions are:
 *
 *   - `require-trusted-types-for 'script'` — every DOM XSS sink throws
 *     unless the value was minted by a named Trusted Types policy.
 *   - `trusted-types ... gn-plugin` — explicitly lists the policy
 *     plugin code uses (matches
 *     @gonext/plugin-frontend-host's `GN_PLUGIN_POLICY_NAME`).
 *
 * The matcher excludes the static asset paths Next.js owns so the
 * header doesn't leak onto fonts / images / Next runtime chunks where
 * it would be either redundant or harmful (e.g. blocking the inline
 * Webpack runtime).
 */
import { NextResponse, type NextRequest } from 'next/server';

/**
 * Names of the Trusted Types policies the admin allows. Mirror this
 * list with the Go middleware's `Options.RequireTrustedTypes` so the
 * dev path (Next.js alone) and prod path (reverse proxy) emit
 * identical headers.
 */
const TRUSTED_TYPES_POLICIES = [
  'default',
  'nextjs#bundler', // Next.js's bundler emits DOM through this policy
  'dompurify', // sanitization for first-party admin code
  'gn-plugin', // plugin-contributed JS — see @gonext/plugin-frontend-host
] as const;

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
 * Builds the canonical admin CSP string with the supplied per-request
 * nonce folded into script-src and style-src. The output mirrors the
 * AdminPolicy preset in packages/go/middleware/csp/preset.go:
 *
 *   default-src 'self'
 *   script-src 'self' 'nonce-…'
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
 *   trusted-types default nextjs#bundler dompurify gn-plugin
 */
function buildCSP(nonce: string): string {
  const nonceSource = `'nonce-${nonce}'`;
  const directives: Array<[string, string[]] | string> = [
    ['default-src', ["'self'"]],
    ['script-src', ["'self'", nonceSource]],
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

export function middleware(request: NextRequest): NextResponse {
  const nonce = generateNonce();
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
