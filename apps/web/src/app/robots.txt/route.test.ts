/**
 * Tests for /robots.txt.
 *
 * The route is intentionally thin — `buildRobotsTxt` is exhaustively
 * covered in feeds.test.ts. These tests pin the wire shape: the
 * handler must route the allow/disallow decision through the API's
 * public-site config and stamp the right Content-Type.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { GET } from './route.ts';

function jsonResponse(status: number, body: unknown): Response {
  return new Response(body === undefined ? '' : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function installRouter(
  router: (url: string) => Response | undefined,
): void {
  vi.spyOn(globalThis, 'fetch').mockImplementation(
    async (input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : input.toString();
      const res = router(url);
      if (!res) return jsonResponse(404, null);
      return res;
    },
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('GET /robots.txt', () => {
  it('allows everything when production-shaped (allowIndex=true)', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/public-site/config')) {
        return jsonResponse(200, {
          baseUrl: 'https://example.com',
          allowIndex: true,
        });
      }
      return undefined;
    });
    const res = await GET();
    expect(res.status).toBe(200);
    expect(res.headers.get('Content-Type')).toBe('text/plain; charset=utf-8');
    const body = await res.text();
    expect(body).toContain('User-agent: *');
    expect(body).toContain('Allow: /');
    expect(body).toContain('Sitemap: https://example.com/sitemap.xml');
    expect(body).not.toContain('Disallow:');
  });

  it('disallows everything in staging / dev (allowIndex=false)', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/public-site/config')) {
        return jsonResponse(200, {
          baseUrl: 'https://staging.example.com',
          allowIndex: false,
        });
      }
      return undefined;
    });
    const res = await GET();
    expect(res.status).toBe(200);
    const body = await res.text();
    expect(body).toContain('User-agent: *');
    expect(body).toContain('Disallow: /');
    // Crucial: even though the API returned a baseUrl, we must NOT
    // leak a sitemap line. A misbehaving crawler that ignores
    // Disallow shouldn't be handed the URL list.
    expect(body).not.toContain('Sitemap:');
    expect(body).not.toContain('staging.example.com');
  });

  it('disallows everything when the API is unreachable', async () => {
    // No router => fetch throws => the API client returns the safe
    // fallback ({ allowIndex: false, baseUrl: '' }). The route must
    // serve the staging-shaped output rather than 500.
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('offline'));
    const res = await GET();
    expect(res.status).toBe(200);
    const body = await res.text();
    expect(body).toContain('Disallow: /');
    expect(body).not.toContain('Sitemap:');
  });

  it('stamps cache headers', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/public-site/config')) {
        return jsonResponse(200, {
          baseUrl: 'https://example.com',
          allowIndex: true,
        });
      }
      return undefined;
    });
    const res = await GET();
    expect(res.headers.get('Cache-Control')).toContain('s-maxage=3600');
  });

  it('omits the sitemap line when baseUrl is empty even if allowed', async () => {
    // Edge case: an operator wired AllowIndex=true but forgot BaseURL.
    // We allow indexing but can't build an absolute sitemap URL — so
    // we drop the Sitemap line rather than emit a relative one
    // (search engines reject relative sitemap URLs).
    installRouter((url) => {
      if (url.includes('/api/v1/public-site/config')) {
        return jsonResponse(200, {
          baseUrl: '',
          allowIndex: true,
        });
      }
      return undefined;
    });
    const res = await GET();
    const body = await res.text();
    expect(body).toContain('Allow: /');
    expect(body).not.toContain('Sitemap:');
  });
});
