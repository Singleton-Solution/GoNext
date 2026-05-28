/**
 * Next.js configuration for @gonext/admin.
 *
 * Decisions:
 *  - `output: 'standalone'` produces a self-contained `.next/standalone/server.js`
 *    that the Dockerfile (apps/admin/Dockerfile) detects and runs directly.
 *    Without this, the Dockerfile's CMD fallback chain falls through to the
 *    placeholder echo.
 *  - `experimental.typedRoutes: true` gives us compile-time-checked `<Link href>`
 *    values across the App Router — catches typos before they reach users.
 *  - `headers()` returns a small baseline of safe defaults. The canonical
 *    security-headers stack is applied server-side via the reverse proxy in
 *    production (see docs/13-security-baseline.md); these are belt-and-braces
 *    so a direct hit on the Next.js server is not naked.
 */
import type { NextConfig } from 'next';

const nextConfig: NextConfig = {
  output: 'standalone',
  reactStrictMode: true,
  // typedRoutes moved out of `experimental` in Next.js 15; opt in at the
  // top level so `<Link href>` values are checked at build time.
  typedRoutes: true,
  /**
   * Proxy /api/* to the GoNext API service. When the bundle ships with
   * NEXT_PUBLIC_API_URL="" (the docker-compose default) the browser
   * uses same-origin fetches against /api/*. This rewrite hops those
   * requests over to the docker-internal API hostname so dev / preview
   * environments don't need a separate reverse proxy.
   *
   * rewrites() evaluates at build time, so GONEXT_API_URL must be set
   * as a build-arg (see apps/admin/Dockerfile + docker-compose.override.yml).
   */
  async rewrites() {
    const apiUrl =
      process.env.GONEXT_API_URL ||
      process.env.NEXT_PUBLIC_API_URL ||
      'http://localhost:8080';
    return [{ source: '/api/:path*', destination: `${apiUrl}/api/:path*` }];
  },
  async headers() {
    return [
      {
        // Apply to every route. The proxy in front of admin overrides these
        // in production with the policy from docs/13-security-baseline.md.
        source: '/:path*',
        headers: [
          { key: 'X-Content-Type-Options', value: 'nosniff' },
          { key: 'X-Frame-Options', value: 'DENY' },
          { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
          {
            key: 'Permissions-Policy',
            value: 'camera=(), microphone=(), geolocation=()',
          },
        ],
      },
    ];
  },
};

export default nextConfig;
