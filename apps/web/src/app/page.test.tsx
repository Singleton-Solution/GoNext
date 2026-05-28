/**
 * Tests for the homepage dispatcher (issue #510).
 *
 * The handler at `apps/web/src/app/page.tsx` reads
 * `core.reading.homepage_type` + `core.reading.homepage_page_id` from
 * the public-site endpoint and either renders the marketing landing
 * (the `'latest_posts'` default) or the pinned static page through
 * `renderSingular` + `PublicShell`.
 *
 * These tests pin the four branches the dispatcher walks:
 *
 *   1. `homepage_type='latest_posts'`               → marketing landing
 *   2. `homepage_type='static_page'` + valid id     → page content
 *   3. `homepage_type='static_page'` + empty id     → marketing fallback
 *   4. page fetch returns 404                       → marketing fallback
 *
 * The fetch surface is routed per-URL: the dispatcher hits
 * `/api/v1/public/site`, `/api/v1/posts/by-slug/{slug}` (for the
 * static-page branch), and `/api/v1/posts?...` (the marketing
 * landing's recent-stories grid). Anything we don't explicitly mock
 * falls through to a 404 so a regression that adds a new upstream
 * dependency fails loudly.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, act } from '@testing-library/react';
import { Suspense, type ReactElement } from 'react';
import HomePage from './page';

/**
 * RTL doesn't await async Server Components on its own, so we wrap the
 * homepage's returned element in a Suspense boundary to keep React 19
 * happy when MarketingNav / MarketingFooter (themselves async server
 * components) appear inside the marketing landing branch.
 *
 * `act(async ...)` lets the suspended children's promises settle
 * between the render call and the assertions.
 */
async function renderHomepageBranch(element: ReactElement) {
  let utils: ReturnType<typeof render> | undefined;
  await act(async () => {
    utils = render(<Suspense fallback={null}>{element}</Suspense>);
  });
  if (!utils) throw new Error('render did not run');
  return utils;
}

vi.mock('next/headers', () => ({
  cookies: async () => ({
    getAll: () => [] as Array<{ name: string; value: string }>,
  }),
  headers: async () => new Headers(),
}));

function jsonResponse(status: number, body: unknown): Response {
  return new Response(body === undefined ? '' : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

/**
 * Per-URL fetch router. Returning `undefined` from the matcher falls
 * through to a 404 — same convention every other route test in this
 * package uses.
 */
function installRouter(router: (url: string) => Response | undefined): void {
  vi.spyOn(globalThis, 'fetch').mockImplementation(
    async (input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : input.toString();
      const res = router(url);
      if (!res) return jsonResponse(404, null);
      return res;
    },
  );
}

/**
 * Build the `/api/v1/public/site` envelope so individual tests only
 * spell out the bits they care about. Mirrors the Go handler's
 * shape — three top-level strings plus the nested `reading` object.
 */
function publicSitePayload(reading: {
  homepage_type: string;
  homepage_page_id: string;
}) {
  return {
    name: 'Test Site',
    tagline: 'Just testing',
    url: 'https://test.example',
    reading,
  };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('HomePage dispatcher', () => {
  it('renders the marketing landing when homepage_type=latest_posts', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/public/site')) {
        return jsonResponse(
          200,
          publicSitePayload({ homepage_type: 'latest_posts', homepage_page_id: '' }),
        );
      }
      if (url.includes('/api/v1/posts?')) {
        return jsonResponse(200, { posts: [] });
      }
      return undefined;
    });

    const element = await HomePage();
    const { container } = await renderHomepageBranch(element);
    const html = container.innerHTML;
    // The marketing landing wraps its main column in `.bg-paper` and
    // the hero uses `.text-ink`. The async MarketingNav / Footer may
    // be unresolved Promises in the RTL sync render, but the outer
    // wrapper is synchronous JSX so its class is observable.
    expect(container.querySelector('.bg-paper')).not.toBeNull();
    // The static-page branch would have produced a `.gn-site` wrapper
    // from PublicShell — its absence proves we took the marketing path.
    expect(container.querySelector('.gn-site')).toBeNull();
    expect(html).not.toContain('data-gn-template');
  });

  it('renders the pinned page when homepage_type=static_page and the page resolves', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/public/site')) {
        return jsonResponse(
          200,
          publicSitePayload({
            homepage_type: 'static_page',
            homepage_page_id: 'welcome',
          }),
        );
      }
      if (url.includes('/api/v1/posts/by-slug/welcome')) {
        return jsonResponse(200, {
          id: '42',
          slug: 'welcome',
          title: 'Welcome Home',
          postType: 'page',
          blocks: [
            { type: 'core/paragraph', attributes: { content: 'static body' } },
          ],
        });
      }
      // Theme + template endpoints are not load-bearing — let them
      // 404 so renderSingular falls back to its hand-assembled main.
      return undefined;
    });

    const element = await HomePage();
    const { container } = await renderHomepageBranch(element);
    const html = container.innerHTML;
    // The PublicShell stamps the template basename on the wrapper —
    // the presence of that attribute is the cleanest assertion for
    // "we took the static-page branch".
    expect(container.querySelector('.gn-site')).not.toBeNull();
    // The post title appears in the fallback singular template.
    expect(html).toContain('Welcome Home');
  });

  it('falls back to the marketing landing when homepage_type=static_page but homepage_page_id is empty', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/public/site')) {
        return jsonResponse(
          200,
          publicSitePayload({
            homepage_type: 'static_page',
            homepage_page_id: '',
          }),
        );
      }
      if (url.includes('/api/v1/posts?')) {
        return jsonResponse(200, { posts: [] });
      }
      return undefined;
    });

    const element = await HomePage();
    const { container } = await renderHomepageBranch(element);
    // Marketing path again — no .gn-site wrapper from PublicShell.
    expect(container.querySelector('.gn-site')).toBeNull();
    expect(container.querySelector('.bg-paper')).not.toBeNull();
  });

  it('falls back to the marketing landing when the pinned page returns 404', async () => {
    installRouter((url) => {
      if (url.includes('/api/v1/public/site')) {
        return jsonResponse(
          200,
          publicSitePayload({
            homepage_type: 'static_page',
            homepage_page_id: 'deleted-page',
          }),
        );
      }
      if (url.includes('/api/v1/posts/by-slug/deleted-page')) {
        return jsonResponse(404, null);
      }
      if (url.includes('/api/v1/posts?')) {
        return jsonResponse(200, { posts: [] });
      }
      // renderSingular's 404 path also fetches the resolved template +
      // active theme; let them fall through so the renderer paints the
      // fallback. The dispatcher should still detect status=404 and
      // pick the marketing landing — the page-content text from the
      // 404 fallback would have been "Page not found", and we assert
      // its absence below.
      return undefined;
    });

    const element = await HomePage();
    const { container } = await renderHomepageBranch(element);
    const html = container.innerHTML;
    // We must not have painted the 404 template inside the homepage —
    // the dispatcher promotes a non-200 from renderSingular into the
    // marketing fallback.
    expect(html).not.toContain('Page <em>not</em> found');
    expect(container.querySelector('.bg-paper')).not.toBeNull();
    expect(container.querySelector('.gn-site')).toBeNull();
  });
});
