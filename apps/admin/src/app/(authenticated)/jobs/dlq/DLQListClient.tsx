'use client';

/**
 * DLQ list client island.
 *
 * Loads the archived task list for a given queue, renders it via the
 * shared <ResourceList> component, and wires per-row actions
 * (replay / discard / redact) onto the row link + the bulk-action
 * toolbar.
 *
 * Brand: Living-Systems (#432). Mono row IDs (Geist Mono on paper-3),
 * lavender-accent queue chips ("critical" → emerald, "important" /
 * "webhooks" → lavender, defaults → fg, low-priority → fg-subtle), and
 * a danger-soft row tint on redacted entries so an operator can see at
 * a glance which payloads are masked.
 *
 * Why the bulk toolbar uses confirm dialogs:
 *  - Replay re-enqueues work that might still be broken (the operator
 *    might've fixed only one of several bugs the task surfaced). A
 *    confirm step is the minimal speed bump.
 *  - Discard is irreversible. Always confirm.
 *  - Redact stops at the per-row "Redact…" link — bulk redaction is
 *    explicitly NOT supported because the field set differs per row.
 *
 * URL state:
 *  - `queue` is the only query param the page reads. Default is
 *    "default" so first-load works without setup.
 *  - Pagination cursor is held in component state rather than the URL.
 *    Deep-linking to "page 3 of the DLQ" is a non-feature; operators
 *    reach the DLQ via the issue/incident, not via bookmark.
 */
import Link from 'next/link';
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactElement,
} from 'react';
import { ResourceList } from '@/components/ResourceList';
import { Badge } from '@/components/ui/badge';
import type {
  BulkAction,
  Column,
  FilterChip,
} from '@/components/ResourceList';
import {
  discardTask,
  listArchivedTasks,
  redactTask,
  replayTask,
} from './actions';
import { KNOWN_QUEUES, type ArchivedTask, type DLQListResponse } from './types';

export interface DLQListClientProps {
  initialQueue: string;
  initialData: DLQListResponse;
}

const PAGE_LIMIT = 30;

/**
 * Queue tone map — mirrors the pulse.html data-viz palette:
 *
 *  - critical → emerald (urgent but healthy)
 *  - webhooks, important → lavender (the deliveries surface accent)
 *  - default, media, search, reports → fg-muted (neutral)
 *  - low → fg-subtle (cold)
 *
 * Unknown queue names fall through to the neutral tone.
 */
function queueTone(
  queue: string,
): 'emerald' | 'lavender' | 'default' | 'outline' {
  if (queue === 'critical') return 'emerald';
  if (queue === 'webhooks' || queue === 'important') return 'lavender';
  if (queue === 'low') return 'outline';
  return 'default';
}

/**
 * formatFailedAt renders an ISO8601 timestamp as a compact "5m ago"
 * for the table cell. The exact timestamp is in the title attribute so
 * a hover reveals it.
 */
function formatFailedAt(iso: string): string {
  if (!iso) return '—';
  const t = new Date(iso).getTime();
  if (!Number.isFinite(t)) return iso;
  const diffMs = Date.now() - t;
  const sec = Math.floor(diffMs / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.floor(hr / 24);
  return `${day}d ago`;
}

/**
 * truncateError keeps the error column readable. The full message is
 * available on the detail page; this is the table-row peek.
 */
function truncateError(s: string, n = 80): string {
  if (s.length <= n) return s;
  return s.slice(0, n) + '…';
}

export function DLQListClient({
  initialQueue,
  initialData,
}: DLQListClientProps): ReactElement {
  const [queue, setQueue] = useState(initialQueue);
  const [tasks, setTasks] = useState<ArchivedTask[]>(initialData.data);
  const [cursor, setCursor] = useState<string>(
    initialData.pagination.next_cursor ?? '',
  );
  const [history, setHistory] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  // Refetch whenever the queue changes.
  const refetch = useCallback(
    async (q: string, c?: string) => {
      setLoading(true);
      setError(null);
      try {
        const res = await listArchivedTasks({
          queue: q,
          limit: PAGE_LIMIT,
          cursor: c,
        });
        setTasks(res.data);
        setCursor(res.pagination.next_cursor ?? '');
      } catch (err) {
        setError(err instanceof Error ? err : new Error(String(err)));
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  // Reset the page state when the parent's initial data changes (a
  // back/forward nav from another page returns to here).
  useEffect(() => {
    setTasks(initialData.data);
    setCursor(initialData.pagination.next_cursor ?? '');
    setQueue(initialQueue);
    setHistory([]);
  }, [initialData, initialQueue]);

  const columns: Column<ArchivedTask>[] = useMemo(
    () => [
      {
        key: 'type',
        label: 'Task type',
        render: (row) => (
          <Link
            href={{
              pathname: `/jobs/dlq/${encodeURIComponent(row.id)}`,
              query: { queue: row.queue },
            }}
            className="block transition-colors hover:text-emerald-deep"
          >
            {/* Mono row identity — keep types on a Geist Mono surface so
                operators can scan task names at a glance. */}
            <code className="font-mono text-xs font-medium text-ink">
              {row.type}
            </code>
            <div className="mt-[2px] truncate font-mono text-2xs text-fg-subtle">
              {row.id}
            </div>
          </Link>
        ),
      },
      {
        key: 'queue',
        label: 'Queue',
        width: '120px',
        render: (row) => (
          <Badge variant={queueTone(row.queue)} dot>
            {row.queue}
          </Badge>
        ),
      },
      {
        key: 'failed_at',
        label: 'Failed',
        width: '110px',
        render: (row) => (
          <span
            title={row.failed_at}
            className="font-sans text-xs tabular-nums text-fg-subtle"
          >
            {formatFailedAt(row.failed_at)}
          </span>
        ),
      },
      {
        key: 'retried',
        label: 'Retries',
        width: '90px',
        render: (row) => (
          <span className="font-mono text-xs tabular-nums text-ink-soft">
            {row.retried}/{row.max_retry}
          </span>
        ),
      },
      {
        key: 'last_error',
        label: 'Error',
        render: (row) => (
          <span
            title={row.last_error}
            className="block truncate font-sans text-xs text-fg-muted"
          >
            {truncateError(row.last_error)}
          </span>
        ),
      },
      {
        key: 'payload_preview',
        label: 'Payload',
        render: (row) => (
          // Redacted rows pick up a danger-soft tint on the payload chip
          // so the masked state reads at a glance — calm, but unmistakable.
          <code
            className={
              row.redacted
                ? 'inline-block max-w-[280px] truncate rounded-xs bg-danger-soft/60 px-[6px] py-[2px] font-mono text-2xs text-danger'
                : 'inline-block max-w-[280px] truncate rounded-xs bg-paper-3 px-[6px] py-[2px] font-mono text-2xs text-fg-muted'
            }
          >
            {row.payload_preview}
            {row.redacted ? ' (redacted)' : ''}
          </code>
        ),
      },
    ],
    [],
  );

  const filters: FilterChip[] = useMemo(
    () => [
      {
        key: 'queue',
        label: 'Queue',
        current: queue,
        options: KNOWN_QUEUES.map((q) => ({ value: q, label: q })),
      },
    ],
    [queue],
  );

  const handleFilterChange = useCallback(
    async (key: string, value: string | null) => {
      if (key !== 'queue' || value === null) return;
      setQueue(value);
      setHistory([]);
      await refetch(value);
    },
    [refetch],
  );

  const handleNext = useCallback(async () => {
    if (!cursor) return;
    setHistory((prev) => [...prev, cursor]);
    await refetch(queue, cursor);
  }, [cursor, queue, refetch]);

  const handlePrev = useCallback(async () => {
    setHistory((prev) => {
      const next = prev.slice(0, -1);
      const prevCursor = next.length > 0 ? next[next.length - 1] : undefined;
      void refetch(queue, prevCursor);
      return next;
    });
  }, [queue, refetch]);

  const bulkActions: BulkAction[] = useMemo(
    () => [
      {
        id: 'replay',
        label: 'Replay',
        confirm:
          'Replay the selected tasks? They will be moved back onto the pending queue.',
        onApply: async (ids: string[]) => {
          // The list endpoint returns rows scoped to one queue at a
          // time, so we can use the current queue verbatim.
          await Promise.all(ids.map((id) => replayTask(id, queue)));
          await refetch(queue);
        },
      },
      {
        id: 'discard',
        label: 'Discard',
        danger: true,
        confirm:
          'Permanently delete the selected tasks? This cannot be undone.',
        onApply: async (ids: string[]) => {
          await Promise.all(ids.map((id) => discardTask(id, queue)));
          await refetch(queue);
        },
      },
    ],
    [queue, refetch],
  );

  return (
    <ResourceList<ArchivedTask>
      columns={columns}
      data={tasks}
      total={tasks.length}
      pagination={{
        cursor: cursor || null,
        onNext: () => {
          void handleNext();
        },
        onPrev: () => {
          void handlePrev();
        },
        hasNext: Boolean(cursor),
        hasPrev: history.length > 0,
      }}
      filters={filters}
      bulkActions={bulkActions}
      onSearch={() => {
        /* Search is a follow-up — Asynq's ListArchivedTasks has no
         * search predicate and we don't want to client-filter a
         * partial page. */
      }}
      onFilterChange={(key, value) => {
        void handleFilterChange(key, value);
      }}
      loading={loading}
      error={error}
      onRetry={() => {
        void refetch(queue);
      }}
      emptyState={
        <div className="flex flex-col gap-1">
          <strong className="font-display text-sm font-bold text-ink">
            No archived tasks on this queue.
          </strong>
          <div className="font-sans text-xs text-fg-muted">
            That&apos;s the desired state — failures move here once their
            retry budget is exhausted.
          </div>
        </div>
      }
    />
  );
}

// Helper export for redact button — the detail page imports redactTask
// directly, but we re-export here so any future "redact from list"
// affordance has a single place to wire.
export { redactTask };
