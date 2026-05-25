/**
 * Pages — admin list screen for site pages.
 *
 * Mirrors the posts surface (docs/05-admin-api.md §2.3) — pages are
 * the "evergreen" content type, distinct from time-stamped posts. The
 * shape of the screen (filters, table, pagination) is identical so
 * the IA stays predictable; the data set is just narrowed to the
 * `page` post-type on the API side.
 *
 * The pages REST endpoint is tracked in issue #76. Until it lands
 * this page renders the empty/error state — the same pattern the
 * posts page uses to stay defensible.
 *
 * Brand treatment ("Living systems"): display-type headline with the
 * italic-serif accent ("All *pages*."), an emerald-soft active filter
 * chip strip, and paper-3 row hover. Matches the moodboard pattern
 * in `docs/design/ui_kits/admin/index.html`.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';
import { Plus } from 'lucide-react';
import { Headline } from '@/components/ui/headline';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';

export const dynamic = 'force-dynamic';

interface SitePage {
  id: string;
  title: string;
  slug: string;
  status: 'publish' | 'draft' | 'future';
  updatedAt: string;
}

/** Seed pages until the API endpoint lands. The brand is more
 * important than the data — once #76 ships this list comes from
 * `GET /api/v1/posts?type=page`. */
const SEED_PAGES: readonly SitePage[] = [
  {
    id: 'about',
    title: 'About',
    slug: '/about',
    status: 'publish',
    updatedAt: '3 days ago',
  },
  {
    id: 'contact',
    title: 'Contact',
    slug: '/contact',
    status: 'publish',
    updatedAt: '2 weeks ago',
  },
  {
    id: 'privacy',
    title: 'Privacy policy',
    slug: '/privacy',
    status: 'publish',
    updatedAt: '1 month ago',
  },
  {
    id: 'shipping',
    title: 'Shipping &amp; returns',
    slug: '/shipping',
    status: 'draft',
    updatedAt: 'Yesterday',
  },
  {
    id: 'press',
    title: 'Press kit',
    slug: '/press',
    status: 'future',
    updatedAt: 'Mar 12, 10:00',
  },
];

function PageStatus({ status }: { status: SitePage['status'] }): ReactElement {
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
  return <Badge dot>Draft</Badge>;
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

      {/* ─── Table ─── */}
      <div className="-mt-6 overflow-x-auto rounded-b-lg border border-border bg-paper">
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
          <tbody>
            {SEED_PAGES.map((page) => (
              <tr
                key={page.id}
                className="border-b border-border-subtle transition-colors last:border-b-0 hover:bg-paper-3"
              >
                <td className="px-[18px] py-[13px]">
                  <Link
                    href={`/pages/${page.id}`}
                    className="font-medium text-ink hover:text-emerald-deep hover:no-underline"
                  >
                    {page.title}
                  </Link>
                </td>
                <td className="px-[18px] py-[13px] font-mono text-xs text-fg-subtle">
                  {page.slug}
                </td>
                <td className="px-[18px] py-[13px]">
                  <PageStatus status={page.status} />
                </td>
                <td className="px-[18px] py-[13px] text-fg-subtle">
                  {page.updatedAt}
                </td>
                <td className="px-[18px] py-[13px] text-right">
                  <Link
                    href={`/pages/${page.id}`}
                    className="text-xs font-medium text-fg-subtle hover:text-emerald-deep"
                  >
                    Edit →
                  </Link>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <div className="flex items-center justify-between border-t border-border bg-paper-2 px-[18px] py-3">
          <span className="text-xs text-fg-subtle">
            Showing {SEED_PAGES.length} of {SEED_PAGES.length}
          </span>
        </div>
      </div>
    </section>
  );
}
