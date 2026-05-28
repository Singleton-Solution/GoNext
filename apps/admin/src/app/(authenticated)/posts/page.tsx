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
 * Brand treatment ("Living systems"): the page head adopts the
 * display-type with the italic-serif accent ("All *posts*.") matching
 * the admin moodboard in `docs/design/ui_kits/admin/index.html`.
 *
 * Auth
 * ====
 * Admin pages are session-protected. The session cookie lives on the
 * admin origin (`:3001` in dev, the public admin host in prod) and is
 * forwarded by `serverApiFetch` (see `lib/server-api.ts`) — without
 * that the server-side fetch would issue an anonymous request and the
 * API would 401 every list screen. The auth middleware in front of
 * the admin guarantees the cookie store is populated by the time we
 * get here.
 */
import Link from 'next/link';
import { Suspense, type ReactElement } from 'react';
import { Download, Plus } from 'lucide-react';
import type { paths } from '@gonext/api-types';
import { serverApiFetch } from '@/lib/server-api';
import { Headline } from '@/components/ui/headline';
import { Button } from '@/components/ui/button';
import { PostListClient } from './PostListClient';
import { PostsErrorBoundary } from './PostsErrorBoundary';
import type { PostListResponse } from './columns';
import styles from './posts.module.css';

export const dynamic = 'force-dynamic';

/**
 * Last 8 chars of a UUID — enough to disambiguate authors in the list
 * table without occupying half the column. Mirrors the helper of the
 * same name in `posts/[id]/revisions/page.tsx` (issue #515 fixes the
 * blank-Author-cell bug by routing through this fallback when the list
 * API doesn't include an author display name).
 */
function shortAuthorId(id: string): string {
  if (id.length <= 8) return id;
  return id.slice(-8);
}

/**
 * Wire shape we expect from `GET /api/v1/posts`.
 *
 * Pilot migration (issue #514): the base shape is derived from the
 * OpenAPI spec via `@gonext/api-types` instead of hand-typed. We treat
 * every field as optional (`Partial<>`) because the list endpoint
 * doesn't currently emit the full single-post projection — and the
 * admin must not crash when a field the spec marks required is missing
 * (the adapter below already falls back defensively).
 *
 * The `author` extension is admin-side only — the server returns an
 * `author_id` and the admin attempts to enrich with a display name
 * when available. That field isn't modelled in the OpenAPI spec yet
 * (tracked under issue #515), so it stays as a local intersection.
 */
type PostSchema =
  paths['/api/v1/posts']['get']['responses']['200']['content']['application/json']['data'][number];
export type ApiPost = Partial<PostSchema> & {
  id: string;
  author?: { id?: string; display_name?: string; displayName?: string } | null;
};

/**
 * Adapt an API post to the flatter `Post` shape the list UI expects.
 *
 * Pulled out of `fetchInitialPosts` so it can be unit tested without
 * spinning up the whole server component (issue #515).
 *
 * Author display name: the list endpoint doesn't currently JOIN
 * `users`, so `author.display_name` is usually absent. We fall back
 * to the last 8 chars of the author UUID rather than rendering a
 * blank Author cell — matches the pattern used on the revisions page
 * (`shortId` helper).
 *
 * Comments count: a separate aggregate the list endpoint doesn't
 * compute. Left at 0 until the API gains the column / sub-select
 * (tracked as a follow-up in #515).
 */
export function adaptApiPost(p: ApiPost): PostListResponse['posts'][number] {
  const apiName =
    p.author?.display_name ?? p.author?.displayName ?? '';
  const authorId = p.author?.id ?? p.author_id ?? '';
  return {
    id: p.id,
    title: p.title ?? '(untitled)',
    status:
      (p.status as PostListResponse['posts'][number]['status']) ?? 'draft',
    date: p.published_at ?? p.updated_at ?? p.created_at ?? '',
    author: {
      id: authorId,
      displayName: apiName || (authorId ? shortAuthorId(authorId) : ''),
    },
    // Comments aggregate isn't part of the list payload — would
    // require a SELECT COUNT(*) join we don't run yet. See #515.
    commentsCount: 0,
  };
}

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
 * Server-side fetch helper. Cookie forwarding is handled by
 * `serverApiFetch`. Returns `null` on any failure so the caller can
 * render a friendly state.
 */
async function fetchInitialPosts(): Promise<{
  data: PostListResponse | null;
  error: string | null;
}> {
  try {
    const res = await serverApiFetch('/api/v1/posts?status=any&limit=20');

    if (!res.ok) {
      return {
        data: null,
        error: `HTTP ${res.status}`,
      };
    }
    // The API returns the envelope shape `{data: [...], pagination: {...}}`
    // — see rest/posts/handlers.go list(). The page UI works in
    // `{posts, nextCursor, total}` form with a flatter Post shape, so
    // adapt here. Be defensive: a missing field shouldn't crash the
    // page (issue #76 — contract still evolving).
    type ApiEnvelope = {
      data?: ApiPost[];
      posts?: ApiPost[];
      pagination?: { next_cursor?: string; nextCursor?: string };
      nextCursor?: string;
      total?: number;
    };
    const json = (await res.json()) as ApiEnvelope;
    const rows = Array.isArray(json.data)
      ? json.data
      : Array.isArray(json.posts)
        ? json.posts
        : [];
    const cursor =
      json.pagination?.next_cursor ??
      json.pagination?.nextCursor ??
      json.nextCursor ??
      null;
    // Map ApiPost → the flatter Post shape the UI expects.
    const posts = rows.map(adaptApiPost);
    return {
      data: {
        posts: posts as PostListResponse['posts'],
        nextCursor: cursor,
        total: typeof json.total === 'number' ? json.total : posts.length,
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
    <section data-testid="posts-page" className="flex flex-col gap-6">
      {/* ─── Page head — brand display-type with italic accent ─── */}
      <div className="flex flex-wrap items-end justify-between gap-6 border-b border-border pb-6">
        <div>
          <Headline as="h1" size="page" className="text-[clamp(36px,4.5vw,44px)]">
            All <em>posts</em>.
          </Headline>
          <p className="mt-[10px] max-w-[480px] text-sm text-fg-muted">
            Drafts, scheduled, and published content. Filter by status to focus
            on what needs attention.
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="default" size="default" asChild>
            <Link href="/posts/import">
              <Download aria-hidden="true" width={14} height={14} />
              Import
            </Link>
          </Button>
          <Button variant="primary" size="default" asChild>
            <Link href="/posts/new">
              <Plus aria-hidden="true" width={14} height={14} />
              New post
            </Link>
          </Button>
        </div>
      </div>

      <PostsErrorBoundary>
        <Suspense fallback={<PostsSkeleton />}>
          <PostsListServer />
        </Suspense>
      </PostsErrorBoundary>
    </section>
  );
}
