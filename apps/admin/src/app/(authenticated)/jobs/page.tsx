/**
 * Jobs — queue landing page.
 *
 * The DLQ subsurface (/jobs/dlq) links here via a "Back to jobs"
 * affordance, but the parent route had no `page.tsx` and 404'd
 * (issue #507). This file fills the gap with a static card grid: one
 * card per known queue, each routing to the DLQ filtered by that
 * queue.
 *
 * No data fetching today. The queue topology is loaded from the same
 * `KNOWN_QUEUES` constant the DLQ chip filter reads — that list is the
 * canonical chassis queue set (docs/05-admin-api.md §4.3). Live
 * queue-depth / failed-count instrumentation lands when the
 * `/api/v1/admin/jobs/stats` endpoint ships; for now the cards exist so
 * the surface stops looking broken and operators have a discoverable
 * path into each queue's DLQ.
 *
 * Brand: italic accent on the noun ("Background *jobs*."), card grid
 * on paper-2, queue-tone matching the DLQ chips so the cross-link feels
 * cohesive.
 */
import type { ReactElement } from 'react';
import Link from 'next/link';
import { ArrowRight } from 'lucide-react';

import { Headline } from '@/components/ui/headline';
import { Badge } from '@/components/ui/badge';
import { KNOWN_QUEUES } from './dlq/types';

/**
 * Short, human description for each known queue. Keeps the landing
 * page readable instead of just listing nouns — operators can pick
 * the right queue without cross-referencing the chassis docs.
 *
 * Unknown queues (e.g. plugin-defined) render with a generic blurb
 * via `fallbackDescription`; the URL is the queue name so the DLQ
 * still works for them.
 */
const QUEUE_DESCRIPTIONS: Readonly<Record<string, string>> = {
  critical: 'High-priority work — auth flows, billing webhooks, anything that blocks a user.',
  default: 'Catch-all queue. If a job didn’t pick a queue, it lands here.',
  webhooks: 'Outgoing webhook deliveries and the retry budget that comes with them.',
  media: 'Image processing, thumbnail derivatives, large-file moves.',
  search: 'Search index re-builds and incremental document updates.',
  reports: 'Long-running analytics rollups, exports, scheduled summaries.',
  low: 'Background cleanup — orphan sweeps, archive compaction, telemetry rolls.',
};

const fallbackDescription =
  'Background tasks routed onto this queue. Open the DLQ to inspect anything that failed.';

/**
 * Surface tone for the queue badge. Mirrors `queueTone()` in the DLQ
 * client so a "critical" chip looks the same here as it does on the
 * DLQ list — the cross-link is a continuation of the same view.
 */
function queueTone(
  queue: string,
): 'emerald' | 'lavender' | 'outline' | 'default' {
  if (queue === 'critical') return 'emerald';
  if (queue === 'webhooks' || queue === 'important') return 'lavender';
  if (queue === 'low') return 'outline';
  return 'default';
}

export default function JobsPage(): ReactElement {
  return (
    <section data-testid="jobs-page" className="flex flex-col gap-6">
      {/* Page head — instrument-panel feel, matches the DLQ sibling. */}
      <div className="flex flex-col gap-3 border-b border-border pb-6">
        <span className="font-sans text-2xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
          Operations
        </span>
        <Headline as="h1" size="page">
          Background <em>jobs</em>.
        </Headline>
        <p className="max-w-[540px] font-sans text-sm text-fg-muted">
          Every async task runs on one of the chassis queues. Pick a queue
          to inspect its dead-letter contents and replay or discard
          failures.
        </p>
      </div>

      <ul
        role="list"
        aria-label="Queues"
        className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3"
      >
        {KNOWN_QUEUES.map((queue) => (
          <li key={queue}>
            <Link
              href={{ pathname: '/jobs/dlq', query: { queue } }}
              className="group flex h-full flex-col gap-3 rounded-lg border border-border bg-paper-2 p-5 shadow-xs transition-all hover:-translate-y-[2px] hover:border-emerald hover:shadow-md focus-visible:border-emerald focus-visible:shadow-focus focus-visible:outline-none"
              data-testid={`jobs-queue-card-${queue}`}
            >
              <div className="flex items-start justify-between gap-3">
                <Badge variant={queueTone(queue)} dot>
                  {queue}
                </Badge>
                <ArrowRight
                  aria-hidden="true"
                  width={14}
                  height={14}
                  className="mt-1 text-fg-subtle transition-transform group-hover:translate-x-[2px] group-hover:text-emerald-deep"
                />
              </div>
              <p className="text-sm text-fg-muted">
                {QUEUE_DESCRIPTIONS[queue] ?? fallbackDescription}
              </p>
              <span className="mt-auto inline-flex items-center gap-1 text-xs font-medium text-fg-subtle group-hover:text-emerald-deep">
                Open dead-letter queue
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}
