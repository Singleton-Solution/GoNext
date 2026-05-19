/**
 * Next.js configuration for @gonext/web — the public site renderer.
 *
 * Decisions mirror apps/admin/next.config.ts where the rationale is
 * shared (`output: 'standalone'` for the Dockerfile to pick up,
 * `reactStrictMode`, typed routes). The diverging knobs:
 *
 *  - `headers()` here intentionally OMITS X-Frame-Options DENY. The
 *    public site may be embedded by partner sites (Open Graph cards,
 *    "Featured on …" widgets) and a blanket DENY breaks that. The
 *    canonical security headers come from the reverse proxy in
 *    production; this baseline is just so a direct hit on the Next.js
 *    server isn't naked.
 *  - No middleware-injected CSP nonce. The admin needs it because
 *    plugin JS runs inside the admin shell. The public site renders
 *    theme + block HTML only, with no inline script entry point; a
 *    plain self-only CSP from the proxy is sufficient.
 */
import type { NextConfig } from 'next';

const nextConfig: NextConfig = {
  output: 'standalone',
  reactStrictMode: true,
  typedRoutes: true,
  async headers() {
    return [
      {
        source: '/:path*',
        headers: [
          { key: 'X-Content-Type-Options', value: 'nosniff' },
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
