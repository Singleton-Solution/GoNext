/**
 * Posts — admin list screen.
 *
 * Spec: `docs/05-admin-api.md` §2.3.
 *
 * Architecture
 * ============
 * This is a React Server Component. It fetches the first page of posts
 * directly from the GoNext REST API and hands the result to a client
 * island (`PostListClient`) for all interactive behaviour — sorting,
 * filtering, bulk actions, "load more". The split lets us keep the
 * initial render fast (no client-side hydration to fetch posts) while
 * still getting a rich interactive table without a full SPA.
 *
 *   ┌─────────────────────┐    ┌──────────────────────────┐
 *   │  page.tsx (server)  │ →  │  PostListClient (client) │
 *   │  fetches first page │    │  search/filter/sort/etc. │
 *   └─────────────────────┘    └──────────────────────────┘
 *
 * Auth
 * ====
 * Admin pages are session-protected. The session cookie lives on the
 * admin origin (`:3001` in dev, the public admin host in prod) and is
 * forwarded explicitly via `next/headers` `cookies()` — without this
 * the server-side fetch would issue an anonymous request and the API
 * would 401 every list screen. The auth middleware in front of the
 * admin guarantees `cookies()` is populated by the time we get here.
 *
 * REST API dependency
 * ===================
 * The endpoint `GET /api/v1/posts` is tracked in issue #76 and may not
 * yet exist in `main` when this PR lands. The component is defensive:
 * on any fetch failure (network error, 404, 5xx, etc.) we render the
 * empty / error state inline. The page never throws and never crashes
 * the surrounding layout. When #76 ships the only thing that changes
 * is that real data starts appearing — no code change here.
 *
 * Suspense + Error boundary
 * =========================
 * Initial fetch happens inside a `<Suspense>` boundary with a simple
 * skeleton fallback so the surrounding layout (sidebar, header) paints
 * immediately. A client-side `PostsErrorBoundary` wraps the interactive
 * island to catch any render error and offer a retry.
 *
 * Pagination
 * ==========
 * "Load more" (cursor-based). Chosen over numbered pages because
 *   (a) it composes naturally with the API's cursor scheme,
 *   (b) the implementation is simpler — no need for total-count math
 *       on the server,
 *   (c) the user need for direct page jumps is low for an admin list
 *       (Saved Views handle the "I want exactly this slice" case).
 * Numbered pages can be added later if the activity log grows large
 * enough to warrant a page-jump UI.
 */
import { cookies } from 'next/headers';
import Link from 'next/link';
import { Suspense, type ReactElement } from 'react';
import { apiBaseUrl } from '@/lib/api-client';
import { PostListClient } from './PostListClient';
import { PostsErrorBoundary } from './PostsErrorBoundary';
import type { PostListResponse } from './columns';
import styles from './posts.module.css';

export const dynamic = 'force-dynamic';

/** Loading skeleton for the Suspense fallback. */
function PostsSkeleton(): ReactElement {
  return (
    <div className={styles.tableWrap} aria-busy="true" aria-live="polite">
      <div style={{ padding: 'var(--space-4)' }}>
        <span className="visually-hidden">Loading posts…</span>
        {Array.from({ length: 6 }).map((_, idx) => (
          <div key={idx} className={styles.skeletonRow} />
        ))}
      </div>
    </div>
  );
}

/**
 * Empty / error state rendered when the fetch fails. Intentionally
 * structured like the regular page so the layout doesn't shift.
 */
function FetchFailureState({ reason }: { reason: string }): ReactElement {
  return (
    <div className={styles.error} role="alert">
      <h2>Couldn&apos;t load posts</h2>
      <p className="muted">
        We couldn&apos;t fetch the list from the GoNext API ({reason}).
        Try reloading; if it keeps failing the admin API may not be
        reachable yet.
      </p>
      <p className="muted" style={{ fontSize: 13 }}>
        Tracked in issue #76 — the REST endpoint may not yet be in main.
      </p>
    </div>
  );
}

/**
 * Server-side fetch helper. Wraps the api-client's URL resolution with
 * an explicit cookie forward so the session travels with the request,
 * and a typed return type. Returns `null` on any failure so the caller
 * can render a friendly state.
 */
async function fetchInitialPosts(): Promise<{
  data: PostListResponse | null;
  error: string | null;
}> {
  let cookieHeader = '';
  try {
    const store = await cookies();
    cookieHeader = store
      .getAll()
      .map((c) => `${c.name}=${c.value}`)
      .join('; ');
  } catch {
    // `cookies()` can throw during static generation / certain build
    // paths. We swallow and continue with an anonymous request — the
    // API will return 401, which we treat as "no posts" below.
    cookieHeader = '';
  }

  const url = `${apiBaseUrl}/api/v1/posts?status=any&limit=20`;
  try {
    const res = await fetch(url, {
      method: 'GET',
      headers: {
        Accept: 'application/json',
        ...(cookieHeader ? { Cookie: cookieHeader } : {}),
      },
      // Server-to-server call — `credentials: 'include'` is a browser-
      // only concept; we forward auth via the Cookie header instead.
      cache: 'no-store',
    });

    if (!res.ok) {
      return {
        data: null,
        error: `HTTP ${res.status}`,
      };
    }
    const json = (await res.json()) as PostListResponse;
    // Be defensive: the API contract is still evolving (issue #76) so
    // missing fields shouldn't crash the page.
    return {
      data: {
        posts: Array.isArray(json.posts) ? json.posts : [],
        nextCursor: json.nextCursor ?? null,
        total: typeof json.total === 'number' ? json.total : 0,
      },
      error: null,
    };
  } catch (err) {
    const reason = err instanceof Error ? err.message : 'network error';
    return { data: null, error: reason };
  }
}

async function PostsListServer(): Promise<ReactElement> {
  const { data, error } = await fetchInitialPosts();

  if (error || !data) {
    // Graceful empty state — the API endpoint may not yet exist.
    return (
      <>
        <FetchFailureState reason={error ?? 'no data'} />
        <div className={styles.empty} style={{ marginTop: 16 }} role="status">
          <h2>No posts yet</h2>
          <p>Start by creating your first post.</p>
          <Link
            href={{ pathname: '/posts/new' }}
            className={styles.primaryAction}
          >
            Create your first
          </Link>
        </div>
      </>
    );
  }

  return <PostListClient initialData={data} />;
}

export default function PostsPage(): ReactElement {
  return (
    <section>
      <div className={styles.headerBar}>
        <h1>Posts</h1>
        <Link
          href={{ pathname: '/posts/new' }}
          className={styles.primaryAction}
        >
          New post
        </Link>
      </div>
      <PostsErrorBoundary>
        <Suspense fallback={<PostsSkeleton />}>
          <PostsListServer />
        </Suspense>
      </PostsErrorBoundary>
    </section>
  );
}
