'use client';

/**
 * DLQ detail client island.
 *
 * Shows the full payload and metadata for a single archived task plus
 * the three actions: replay, discard, redact.
 *
 * Brand: Living-Systems (#432). Two-column layout — left column holds
 * the metadata grid (mono labels on paper-2, ink values), right column
 * holds the payload JSON viewer (mono on paper-3 — the recessed code
 * surface from the handoff). Action toolbar pins to the top so an
 * operator never has to scroll to act. Replay/Discard/Redact land on
 * emerald / danger / lavender so the colour itself hints at the
 * outcome.
 *
 * Action UX:
 *  - Replay and Discard are confirmed in a native confirm() prompt for
 *    now (the design-system Dialog primitive is now in the shadcn set
 *    but the inline confirm has tests pinned to it; swapping in is a
 *    follow-up to keep this change visual-only).
 *  - Redact opens RedactDialog, which lists the top-level fields
 *    parsed out of the payload. Applying the dialog calls the redact
 *    endpoint and immediately refetches the task so the payload view
 *    shows the masked form.
 *
 * The "back to list" link preserves the queue param so the user
 * lands on the page they came from.
 */
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import {
  ChevronLeft,
  Eraser,
  RotateCcw,
  Trash2,
} from 'lucide-react';
import { useCallback, useMemo, useState, type ReactElement } from 'react';
import { Headline } from '@/components/ui/headline';
import { Card } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  discardTask,
  getArchivedTask,
  redactTask,
  replayTask,
} from '../actions';
import { RedactDialog } from '../components/RedactDialog';
import type { ArchivedTask } from '../types';

export interface DLQDetailClientProps {
  initialTask: ArchivedTask;
}

/**
 * extractPayloadFields parses the payload as JSON and returns its
 * top-level field names. For non-object payloads (arrays, strings,
 * numbers) we return [], which RedactDialog treats as a special
 * "wholesale mask only" mode.
 */
function extractPayloadFields(payload: string | undefined): string[] {
  if (!payload) return [];
  try {
    const parsed = JSON.parse(payload) as unknown;
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return Object.keys(parsed as Record<string, unknown>);
    }
  } catch {
    /* not JSON; fall through. */
  }
  return [];
}

function formatPayload(payload: string | undefined): string {
  if (!payload) return '(empty)';
  try {
    return JSON.stringify(JSON.parse(payload), null, 2);
  } catch {
    return payload;
  }
}

function queueTone(
  queue: string,
): 'emerald' | 'lavender' | 'default' | 'outline' {
  if (queue === 'critical') return 'emerald';
  if (queue === 'webhooks' || queue === 'important') return 'lavender';
  if (queue === 'low') return 'outline';
  return 'default';
}

export function DLQDetailClient({
  initialTask,
}: DLQDetailClientProps): ReactElement {
  const router = useRouter();
  const [task, setTask] = useState<ArchivedTask>(initialTask);
  const [busy, setBusy] = useState<'replay' | 'discard' | 'redact' | null>(
    null,
  );
  const [error, setError] = useState<string | null>(null);
  const [redactOpen, setRedactOpen] = useState(false);

  const payloadFields = useMemo(
    () => extractPayloadFields(task.payload),
    [task.payload],
  );

  const refresh = useCallback(async () => {
    try {
      const next = await getArchivedTask(task.id, task.queue);
      setTask(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [task.id, task.queue]);

  const handleReplay = useCallback(async () => {
    if (
      typeof window !== 'undefined' &&
      !window.confirm(
        'Replay this task? It will be moved back onto the pending queue.',
      )
    ) {
      return;
    }
    setBusy('replay');
    setError(null);
    try {
      await replayTask(task.id, task.queue);
      // Replay removes the task from the archived set — back to the
      // list view, no point staying on a detail page for a row that
      // no longer exists.
      router.push(`/jobs/dlq?queue=${encodeURIComponent(task.queue)}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setBusy(null);
    }
  }, [router, task.id, task.queue]);

  const handleDiscard = useCallback(async () => {
    if (
      typeof window !== 'undefined' &&
      !window.confirm('Permanently delete this task? This cannot be undone.')
    ) {
      return;
    }
    setBusy('discard');
    setError(null);
    try {
      await discardTask(task.id, task.queue);
      router.push(`/jobs/dlq?queue=${encodeURIComponent(task.queue)}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setBusy(null);
    }
  }, [router, task.id, task.queue]);

  const handleRedactApply = useCallback(
    async (fields: string[]) => {
      setRedactOpen(false);
      if (fields.length === 0) return;
      setBusy('redact');
      setError(null);
      try {
        await redactTask(task.id, { queue: task.queue, fields });
        await refresh();
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        setBusy(null);
      }
    },
    [refresh, task.id, task.queue],
  );

  return (
    <section
      data-testid="dlq-detail-page"
      className="flex flex-col gap-6"
    >
      {/* Page head with breadcrumb back-link */}
      <div className="flex items-end justify-between gap-6 border-b border-border pb-6">
        <div className="flex flex-col gap-3">
          <span className="font-sans text-2xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
            Jobs · Failed · Detail
          </span>
          <Headline as="h1" size="sub">
            Failure on <em>{task.type}</em>.
          </Headline>
          <div className="flex flex-wrap items-center gap-2 font-mono text-2xs text-fg-subtle">
            <span data-testid="dlq-detail-type">{task.type}</span>
            <span aria-hidden="true">·</span>
            <span className="select-all">{task.id}</span>
            <Badge variant={queueTone(task.queue)} dot>
              {task.queue}
            </Badge>
            {task.redacted ? (
              <Badge variant="lavender">redacted</Badge>
            ) : null}
          </div>
        </div>
        <Link
          href={`/jobs/dlq?queue=${encodeURIComponent(task.queue)}`}
          className="inline-flex shrink-0 items-center gap-1 font-sans text-sm text-fg-subtle transition-colors hover:text-ink"
        >
          <ChevronLeft className="h-[13px] w-[13px]" aria-hidden="true" />
          Back to DLQ
        </Link>
      </div>

      {/* Action toolbar — Replay / Discard / Redact, with colour cues
          encoded in the variant: emerald for positive (re-enqueue),
          destructive for permanent delete, lavender for the mask-only
          path. */}
      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          variant="emerald"
          disabled={busy !== null}
          onClick={() => {
            void handleReplay();
          }}
          data-testid="dlq-detail-replay"
        >
          <RotateCcw className="h-[14px] w-[14px]" aria-hidden="true" />
          {busy === 'replay' ? 'Replaying…' : 'Replay'}
        </Button>
        <Button
          type="button"
          variant="destructive"
          disabled={busy !== null}
          onClick={() => {
            void handleDiscard();
          }}
          data-testid="dlq-detail-discard"
        >
          <Trash2 className="h-[14px] w-[14px]" aria-hidden="true" />
          {busy === 'discard' ? 'Discarding…' : 'Discard'}
        </Button>
        <Button
          type="button"
          variant="default"
          disabled={busy !== null}
          onClick={() => setRedactOpen(true)}
          data-testid="dlq-detail-redact"
          className="border-lavender/40 bg-lavender-soft text-lavender-deep hover:bg-lavender/15 hover:border-lavender hover:text-lavender-deep"
        >
          <Eraser className="h-[14px] w-[14px]" aria-hidden="true" />
          {busy === 'redact' ? 'Redacting…' : 'Redact…'}
        </Button>
        {error ? (
          <span
            role="alert"
            data-testid="dlq-detail-error"
            className="ml-2 inline-flex items-center rounded-sm bg-danger-soft px-2 py-1 font-sans text-xs text-danger"
          >
            {error}
          </span>
        ) : null}
      </div>

      {/* Two-column layout: metadata on the left, payload on the right.
          Stacks on narrow viewports so the JSON never gets clipped. */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)]">
        <Card className="p-5">
          <h2 className="mb-4 font-display text-sm font-bold uppercase tracking-[0.08em] text-fg-subtle">
            Metadata
          </h2>
          <dl
            className="grid grid-cols-[120px_minmax(0,1fr)] gap-y-3 gap-x-4 font-sans text-sm"
          >
            <dt className="text-fg-subtle">Task ID</dt>
            <dd>
              <code className="select-all break-all font-mono text-xs text-ink">
                {task.id}
              </code>
            </dd>
            <dt className="text-fg-subtle">Queue</dt>
            <dd>
              <Badge variant={queueTone(task.queue)} dot>
                {task.queue}
              </Badge>
            </dd>
            <dt className="text-fg-subtle">Failed at</dt>
            <dd className="font-mono text-xs tabular-nums text-ink-soft">
              {task.failed_at || '—'}
            </dd>
            <dt className="text-fg-subtle">Retries</dt>
            <dd className="font-mono text-xs tabular-nums text-ink-soft">
              {task.retried} / {task.max_retry}
            </dd>
            {task.redacted ? (
              <>
                <dt className="text-fg-subtle">Redacted</dt>
                <dd className="flex flex-wrap items-center gap-2">
                  <Badge variant="lavender">Yes</Badge>
                  <span className="font-sans text-xs text-fg-muted">
                    fields:{' '}
                    <code className="font-mono text-2xs text-ink-soft">
                      {(task.redacted_fields ?? []).join(', ')}
                    </code>
                  </span>
                </dd>
              </>
            ) : null}
          </dl>

          {/* Full error trace — wrapped, mono, recessed on paper-3. */}
          <div className="mt-6">
            <h3 className="mb-2 font-display text-xs font-bold uppercase tracking-[0.08em] text-fg-subtle">
              Last error
            </h3>
            <pre
              data-testid="dlq-detail-error-trace"
              className="max-h-[260px] overflow-auto whitespace-pre-wrap rounded-md border border-border bg-paper-3 p-3 font-mono text-2xs leading-relaxed text-ink-soft"
            >
              {task.last_error || '(no error message recorded)'}
            </pre>
          </div>
        </Card>

        <Card className="overflow-hidden">
          <div className="flex items-center justify-between border-b border-border bg-paper-2 px-4 py-3">
            <h2 className="font-display text-sm font-bold uppercase tracking-[0.08em] text-fg-subtle">
              Payload
            </h2>
            <span className="font-mono text-2xs text-fg-subtle">
              {task.payload ? 'JSON' : 'empty'}
            </span>
          </div>
          <pre
            data-testid="dlq-detail-payload"
            className="max-h-[560px] overflow-auto bg-paper-3 p-4 font-mono text-xs leading-relaxed text-ink-soft"
          >
            {formatPayload(task.payload)}
          </pre>
        </Card>
      </div>

      <RedactDialog
        open={redactOpen}
        fields={payloadFields}
        initiallySelected={task.redacted_fields ?? []}
        onApply={(fields) => {
          void handleRedactApply(fields);
        }}
        onCancel={() => setRedactOpen(false)}
      />
    </section>
  );
}
