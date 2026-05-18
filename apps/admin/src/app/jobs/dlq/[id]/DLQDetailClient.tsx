'use client';

/**
 * DLQ detail client island.
 *
 * Shows the full payload and metadata for a single archived task plus
 * the three actions: replay, discard, redact.
 *
 * Action UX:
 *  - Replay and Discard are confirmed in a native confirm() prompt for
 *    now (the design-system Dialog primitive lands in #34; swapping in
 *    is a one-liner). The action is fire-and-forget from the user's
 *    POV — on success we navigate back to the list, on failure we
 *    surface the error inline.
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
import { useCallback, useMemo, useState, type ReactElement } from 'react';
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
    <section>
      <div style={{ marginBottom: 16 }}>
        <Link
          href={`/jobs/dlq?queue=${encodeURIComponent(task.queue)}`}
          className="muted"
        >
          ← Back to DLQ
        </Link>
      </div>
      <h1 style={{ marginTop: 0 }}>
        <code data-testid="dlq-detail-type">{task.type}</code>
      </h1>
      <dl
        style={{
          display: 'grid',
          gridTemplateColumns: '120px 1fr',
          gap: '8px 16px',
          margin: '16px 0',
        }}
      >
        <dt>Task ID</dt>
        <dd>
          <code>{task.id}</code>
        </dd>
        <dt>Queue</dt>
        <dd>{task.queue}</dd>
        <dt>Failed at</dt>
        <dd>{task.failed_at || '—'}</dd>
        <dt>Retries</dt>
        <dd>
          {task.retried} / {task.max_retry}
        </dd>
        <dt>Last error</dt>
        <dd style={{ whiteSpace: 'pre-wrap', fontFamily: 'monospace' }}>
          {task.last_error || '(no error message recorded)'}
        </dd>
        {task.redacted ? (
          <>
            <dt>Redacted</dt>
            <dd>
              <strong>Yes</strong> — fields:{' '}
              {(task.redacted_fields ?? []).join(', ')}
            </dd>
          </>
        ) : null}
      </dl>

      <h2>Payload</h2>
      <pre
        data-testid="dlq-detail-payload"
        style={{
          background: 'var(--color-surface)',
          border: '1px solid var(--color-border)',
          borderRadius: 'var(--radius)',
          padding: 'var(--space-3)',
          overflowX: 'auto',
          fontSize: 12,
          maxHeight: 480,
        }}
      >
        {formatPayload(task.payload)}
      </pre>

      {error ? (
        <div role="alert" data-testid="dlq-detail-error" style={{ color: 'red' }}>
          {error}
        </div>
      ) : null}

      <div
        style={{
          display: 'flex',
          gap: 'var(--space-2)',
          marginTop: 'var(--space-3)',
        }}
      >
        <button
          type="button"
          disabled={busy !== null}
          onClick={() => {
            void handleReplay();
          }}
          data-testid="dlq-detail-replay"
        >
          {busy === 'replay' ? 'Replaying…' : 'Replay'}
        </button>
        <button
          type="button"
          disabled={busy !== null}
          onClick={() => {
            void handleDiscard();
          }}
          data-testid="dlq-detail-discard"
        >
          {busy === 'discard' ? 'Discarding…' : 'Discard'}
        </button>
        <button
          type="button"
          disabled={busy !== null}
          onClick={() => setRedactOpen(true)}
          data-testid="dlq-detail-redact"
        >
          Redact…
        </button>
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
