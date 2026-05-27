/**
 * Edit a redirect rule — Living-Systems brand surface.
 *
 * Hydrates the form from a server-fetched row and renders a hit
 * timeline beside it (last-hit-at relative + total hits). The hit
 * timeline is a thin paper-2 well that reuses the rule's own
 * server-side counters — no extra fetch.
 */
import Link from 'next/link';
import { notFound } from 'next/navigation';
import type { ReactElement } from 'react';
import { ArrowLeft, ActivitySquare } from 'lucide-react';
import { serverApiFetch } from '@/lib/server-api';
import { Headline } from '@/components/ui/headline';
import { Badge } from '@/components/ui/badge';
import { RedirectForm } from '../RedirectForm';
import type { Redirect } from '../types';

export const dynamic = 'force-dynamic';

async function fetchRedirect(id: string): Promise<Redirect | null> {
  const res = await serverApiFetch(
    `/api/v1/admin/redirects/${encodeURIComponent(id)}`,
  );
  if (!res.ok) {
    return null;
  }
  return (await res.json()) as Redirect;
}

function formatAbsolute(iso?: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

export default async function EditRedirectPage({
  params,
}: {
  params: Promise<{ id: string }>;
}): Promise<ReactElement> {
  const { id } = await params;
  const initial = await fetchRedirect(id);
  if (!initial) {
    notFound();
  }
  return (
    <section className="flex flex-col gap-6">
      <div className="flex flex-col gap-2">
        <Link
          href="/redirects"
          className="inline-flex w-fit items-center gap-1 text-xs text-fg-muted hover:text-emerald-deep"
        >
          <ArrowLeft size={14} aria-hidden="true" /> Redirects
        </Link>
        <span className="eyebrow">Routing &middot; edit rule</span>
        <Headline as="h1" size="page">
          Tune the <em>landing</em>.
        </Headline>
        <p className="lead max-w-[58ch]">
          Edits take effect on the next engine reload (instant after save).
          The rule keeps its hit history, so you can rename the destination
          without losing the trend.
        </p>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[1fr_280px]">
        <RedirectForm initial={initial} />

        <aside
          aria-label="Hit timeline"
          className="card flex flex-col gap-4"
          data-testid="hit-timeline"
        >
          <div className="flex items-center gap-2">
            <ActivitySquare aria-hidden="true" size={16} className="text-emerald-deep" />
            <h2 className="m-0 font-display text-base font-semibold text-ink">
              Hit timeline
            </h2>
          </div>

          <dl className="flex flex-col gap-3 text-sm">
            <div className="flex flex-col gap-0.5">
              <dt className="text-xs uppercase tracking-wide text-fg-subtle">Total hits</dt>
              <dd className="font-mono text-2xl font-semibold text-ink">
                {initial.hit_count.toLocaleString()}
              </dd>
            </div>
            <div className="flex flex-col gap-0.5">
              <dt className="text-xs uppercase tracking-wide text-fg-subtle">Last hit</dt>
              <dd className="font-mono text-xs text-ink-soft">
                {formatAbsolute(initial.last_hit_at)}
              </dd>
            </div>
            <div className="flex flex-col gap-0.5">
              <dt className="text-xs uppercase tracking-wide text-fg-subtle">Created</dt>
              <dd className="font-mono text-xs text-ink-soft">
                {formatAbsolute(initial.created_at)}
              </dd>
            </div>
            <div className="flex flex-col gap-0.5">
              <dt className="text-xs uppercase tracking-wide text-fg-subtle">Status</dt>
              <dd>
                {initial.status === 301 ? (
                  <Badge variant="emerald" dot>
                    <span className="font-mono">{initial.status}</span>
                  </Badge>
                ) : (
                  <Badge variant="lavender" dot>
                    <span className="font-mono">{initial.status}</span>
                  </Badge>
                )}
              </dd>
            </div>
          </dl>
        </aside>
      </div>
    </section>
  );
}
