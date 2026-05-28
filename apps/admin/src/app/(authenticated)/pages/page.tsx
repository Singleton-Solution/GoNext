/**
 * Pages — admin list screen (issue #506).
 *
 * Pages are the "evergreen" content type — distinct from time-stamped
 * posts but stored in the same table (post_type='page'). The
 * `/api/v1/pages` mount isn't wired yet (see deps.go), so we filter
 * the posts endpoint with the new `post_type=page` query param that
 * the handler now accepts.
 *
 * Architecture
 * ============
 * Server Component that fetches the first page directly from the API
 * and renders the list inline — there's no client island here because
 * the pages surface doesn't need bulk actions / sortable columns the
 * way posts do. When the block editor for pages lands the per-row
 * "Edit" link will route to the dedicated editor, but the list itself
 * stays render-on-server.
 *
 * Auth: session cookies are forwarded via `serverApiFetch` (the same
 * pattern posts uses). Without that the API would 401 every render.
 *
 * Brand treatment: "All *pages*." italic-accent display headline +
 * emerald-soft active filter chip + paper-3 row hover. The chrome
 * around the table is identical to the posts list so the IA stays
 * predictable.
 */
import Link from 'next/link';
import { Suspense, type ReactElement } from 'react';
import { Plus } from 'lucide-react';
import { serverApiFetch } from '@/lib/server-api';
import { Headline } from '@/components/ui/headline';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';

export const dynamic = 'force-dynamic';

/** Status values the page list surfaces. Mirrors the WP-compat set
 * the API speaks. The API uses `publish`/`scheduled`/`draft`; we
 * normalise scheduled to `future` so the badge logic stays aligned
 * with the post detail surface. */
export type PageStatus = 'publish' | 'draft' | 'future' | 'private' | 'trash';

/** Flatter shape the list UI renders. Pulled out of the adapter so
 * the tests can exercise the field-mapping rules without spinning up
 * the whole server component. */
export interface SitePage {
  id: string;
  title: string;
  slug: string;
  status: PageStatus;
  /** ISO8601 or pre-formatted string — displayed as-is. */
  updatedAt: string;
}

/** Wire shape we expect from `GET /api/v1/posts?post_type=page`. */
export type ApiPagePost = {
  id: string;
  title?: string;
  slug?: string;
  status?: string;
  published_at?: string | null;
  updated_at?: string;
  created_at?: string;
};

/**
 * Adapt an API page-post into the flatter `SitePage` shape the
 * list UI expects.
 *
 * Status: the API speaks `published`/`scheduled`/`draft`; the badge
 * helpers downstream want `publish`/`future`/`draft` (the WP-classic
 * names the rest of the admin uses), so we normalise here.
 *
 * Updated label: prefer `updated_at` so an in-progress draft surfaces
 * its most-recent-change time. Fall back to `published_at` (covers
 * historic rows that only carry a publish stamp), then `created_at`.
 */
export function adaptApiPage(p: ApiPagePost): SitePage {
  const rawStatus = p.status ?? 'draft';
  let status: PageStatus = 'draft';
  if (rawStatus === 'published' || rawStatus === 'publish') status = 'publish';
  else if (rawStatus === 'scheduled' || rawStatus === 'future') status = 'future';
  else if (rawStatus === 'private') status = 'private';
  else if (rawStatus === 'trash') status = 'trash';
  else status = 'draft';

  return {
    id: p.id,
    title: p.title?.trim() || '(untitled)',
    slug: p.slug ?? '',
    status,
    updatedAt: p.updated_at ?? p.published_at ?? p.created_at ?? '',
  };
}

function PageStatusBadge({ status }: { status: PageStatus }): ReactElement {
  if (status === 'publish') {
    return (
      <Badge variant="success" dot>
        Published
      </Badge>
    );
  }
  if (status === 'future') {
    return (
      <Badge variant="lavender" dot>
        Scheduled
      </Badge>
    );
  }
  if (status === 'private') {
    return (
      <Badge variant="ink" dot>
        Private
      </Badge>
    );
  }
  if (status === 'trash') {
    return <Badge dot>Trashed</Badge>;
  }
  return <Badge dot>Draft</Badge>;
}

interface FetchResult {
  rows: SitePage[];
  total: number;
  error: string | null;
}

/**
 * Server-side fetch helper. Returns a friendly empty `rows` array on
 * any failure so the caller can render a degraded state rather than
 * crash. Mirrors the posts/page.tsx defensive shape.
 */
async function fetchPages(): Promise<FetchResult> {
  try {
    const res = await serverApiFetch(
      '/api/v1/posts?post_type=page&status=any&limit=20',
    );
    if (!res.ok) {
      return { rows: [], total: 0, error: `HTTP ${res.status}` };
    }
    type Envelope = {
      data?: ApiPagePost[];
      posts?: ApiPagePost[];
      pagination?: { next_cursor?: string };
      total?: number;
    };
    const json = (await res.json()) as Envelope;
    const list = Array.isArray(json.data)
      ? json.data
      : Array.isArray(json.posts)
        ? json.posts
        : [];
    const rows = list.map(adaptApiPage);
    return {
      rows,
      total: typeof json.total === 'number' ? json.total : rows.length,
      error: null,
    };
  } catch (err) {
    const reason = err instanceof Error ? err.message : 'network error';
    return { rows: [], total: 0, error: reason };
  }
}

async function PagesListServer(): Promise<ReactElement> {
  const { rows, total, error } = await fetchPages();

  return (
    <>
      {/* ─── Filter strip ─── */}
      <div className="rounded-t-lg border border-b-0 border-border bg-paper-2 p-4">
        <div className="flex items-center gap-3">
          <input
            type="search"
            placeholder="Search pages"
            aria-label="Search pages"
            className="flex-1 rounded-md border border-border bg-paper px-3 py-2 text-sm text-ink placeholder:text-fg-faint focus:border-emerald focus:shadow-focus focus:outline-none"
          />
          <div
            role="group"
            aria-label="Filter by status"
            className="inline-flex gap-1 rounded-md bg-paper-3 p-[2px]"
          >
            <button
              type="button"
              className="rounded-sm bg-emerald-soft px-3 py-[5px] text-xs font-medium text-emerald-deep shadow-xs"
              aria-pressed="true"
            >
              All
            </button>
            <button
              type="button"
              className="rounded-sm px-3 py-[5px] text-xs font-medium text-fg-muted hover:text-ink"
            >
              Published
            </button>
            <button
              type="button"
              className="rounded-sm px-3 py-[5px] text-xs font-medium text-fg-muted hover:text-ink"
            >
              Drafts
            </button>
            <button
              type="button"
              className="rounded-sm px-3 py-[5px] text-xs font-medium text-fg-muted hover:text-ink"
            >
              Scheduled
            </button>
          </div>
        </div>
      </div>

      {/* ─── Table / empty / error ─── */}
      <div className="-mt-6 overflow-x-auto rounded-b-lg border border-border bg-paper">
        {error ? (
          <div
            role="alert"
            data-testid="pages-error"
            className="px-[18px] py-6 text-sm text-fg-muted"
          >
            Couldn&apos;t load pages ({error}). Try reloading; if the
            problem persists the API may not be reachable yet.
          </div>
        ) : rows.length === 0 ? (
          <div
            role="status"
            data-testid="pages-empty"
            className="px-[18px] py-6 text-sm text-fg-muted"
          >
            No pages yet. Create one to get started.
          </div>
        ) : (
          <table className="w-full border-collapse text-sm" aria-label="Pages">
            <thead className="bg-paper-2">
              <tr>
                <th
                  scope="col"
                  className="border-b border-border-subtle px-[18px] py-[13px] text-left text-xs font-medium text-fg-subtle"
                >
                  Title
                </th>
                <th
                  scope="col"
                  className="border-b border-border-subtle px-[18px] py-[13px] text-left text-xs font-medium text-fg-subtle"
                >
                  Slug
                </th>
                <th
                  scope="col"
                  className="border-b border-border-subtle px-[18px] py-[13px] text-left text-xs font-medium text-fg-subtle"
                >
                  Status
                </th>
                <th
                  scope="col"
                  className="border-b border-border-subtle px-[18px] py-[13px] text-left text-xs font-medium text-fg-subtle"
                >
                  Updated
                </th>
                <th
                  scope="col"
                  className="border-b border-border-subtle px-[18px] py-[13px] text-right text-xs font-medium text-fg-subtle"
                >
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody data-testid="pages-rows">
              {rows.map((page) => (
                <tr
                  key={page.id}
                  className="border-b border-border-subtle transition-colors last:border-b-0 hover:bg-paper-3"
                >
                  <td className="px-[18px] py-[13px]">
                    <Link
                      href={`/pages/${encodeURIComponent(page.id)}`}
                      className="font-medium text-ink hover:text-emerald-deep hover:no-underline"
                    >
                      {page.title}
                    </Link>
                  </td>
                  <td className="px-[18px] py-[13px] font-mono text-xs text-fg-subtle">
                    {page.slug || '—'}
                  </td>
                  <td className="px-[18px] py-[13px]">
                    <PageStatusBadge status={page.status} />
                  </td>
                  <td className="px-[18px] py-[13px] text-fg-subtle">
                    {page.updatedAt || '—'}
                  </td>
                  <td className="px-[18px] py-[13px] text-right">
                    <Link
                      href={`/pages/${encodeURIComponent(page.id)}`}
                      className="text-xs font-medium text-fg-subtle hover:text-emerald-deep"
                    >
                      Edit →
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <div className="flex items-center justify-between border-t border-border bg-paper-2 px-[18px] py-3">
          <span className="text-xs text-fg-subtle">
            Showing {rows.length} of {total}
          </span>
        </div>
      </div>
    </>
  );
}

/** Loading skeleton for the Suspense fallback. Same shape as the
 * empty/error states so the layout doesn't shift on first paint. */
function PagesSkeleton(): ReactElement {
  return (
    <div
      className="-mt-6 overflow-x-auto rounded-b-lg border border-border bg-paper px-[18px] py-6 text-sm text-fg-muted"
      aria-busy="true"
      aria-live="polite"
    >
      Loading pages…
    </div>
  );
}

export default function PagesPage(): ReactElement {
  return (
    <section data-testid="pages-page" className="flex flex-col gap-6">
      {/* ─── Page head ─── */}
      <div className="flex flex-wrap items-end justify-between gap-6 border-b border-border pb-6">
        <div>
          <Headline as="h1" size="page" className="text-[clamp(36px,4.5vw,44px)]">
            All <em>pages</em>.
          </Headline>
          <p className="mt-[10px] max-w-[480px] text-sm text-fg-muted">
            Evergreen content — about, contact, policy. Edit metadata or open
            the block editor for layout-heavy pages.
          </p>
        </div>
        <Button variant="primary" asChild>
          <Link href="/pages/new">
            <Plus aria-hidden="true" width={14} height={14} />
            New page
          </Link>
        </Button>
      </div>

      <Suspense fallback={<PagesSkeleton />}>
        <PagesListServer />
      </Suspense>
    </section>
  );
}
