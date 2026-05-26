/**
 * Revisions browser — list every stored revision for a post and let an
 * editor roll the post back to any of them.
 *
 * The page is a thin client component. On mount it pulls
 * `/api/v1/admin/posts/{id}/revisions` and renders the list as a
 * vertical timeline: each row shows the kind chip (autosave / manual /
 * publish), the relative timestamp, the author id, and a Restore
 * button. Clicking Restore opens a confirmation dialog (a deliberate
 * second click — the rollback is destructive) and on confirm POSTs to
 * `/api/v1/admin/posts/{id}/revisions/{rev}/restore`. The list reloads
 * on success and the new "manual" revision the API writes for the
 * restore appears at the top of the timeline.
 *
 * Restore is gated by the same edit_posts capability the API checks; a
 * 403 surfaces as the inline error chip, not a hidden button — UI-only
 * hiding would mislead operators about what they can actually do.
 */
'use client';

import {
  useCallback,
  useEffect,
  useState,
  type ReactElement,
} from 'react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import {
  AlertTriangle,
  ChevronLeft,
  Clock,
  History,
  Loader2,
  RotateCcw,
  User as UserIcon,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Headline } from '@/components/ui/headline';
import { api, ApiError } from '@/lib/api-client';

/** Shape returned by GET /api/v1/admin/posts/{id}/revisions. */
interface RevisionView {
  id: string;
  post_id: string;
  author_id?: string;
  kind: 'autosave' | 'manual' | 'publish';
  created_at: string;
  title?: string;
  excerpt?: string;
  comment?: string;
  is_snapshot: boolean;
  is_permanent?: boolean;
}

interface ListResponse {
  data: RevisionView[];
}

type LoadStatus =
  | { kind: 'loading' }
  | { kind: 'loaded'; revisions: RevisionView[] }
  | { kind: 'error'; message: string };

type RestoreStatus =
  | { kind: 'idle' }
  | { kind: 'confirming'; revisionId: string }
  | { kind: 'submitting'; revisionId: string }
  | { kind: 'success'; revisionId: string }
  | { kind: 'error'; message: string };

export default function RevisionsPage(): ReactElement {
  const params = useParams<{ id: string }>();
  const postId = params?.id ?? '';

  const [load, setLoad] = useState<LoadStatus>({ kind: 'loading' });
  const [restore, setRestore] = useState<RestoreStatus>({ kind: 'idle' });

  const fetchRevisions = useCallback(async (): Promise<void> => {
    setLoad({ kind: 'loading' });
    try {
      const resp = await api.get<ListResponse>(
        `/api/v1/admin/posts/${postId}/revisions`,
      );
      setLoad({ kind: 'loaded', revisions: resp.data });
    } catch (err) {
      setLoad({
        kind: 'error',
        message: extractMessage(err, 'Could not load revisions.'),
      });
    }
  }, [postId]);

  useEffect(() => {
    if (postId === '') return;
    void fetchRevisions();
  }, [postId, fetchRevisions]);

  const onRestore = useCallback(
    async (revisionId: string): Promise<void> => {
      setRestore({ kind: 'submitting', revisionId });
      try {
        await api.post(
          `/api/v1/admin/posts/${postId}/revisions/${revisionId}/restore`,
        );
        setRestore({ kind: 'success', revisionId });
        await fetchRevisions();
      } catch (err) {
        setRestore({
          kind: 'error',
          message: extractMessage(err, 'Restore failed.'),
        });
      }
    },
    [postId, fetchRevisions],
  );

  return (
    <section
      data-testid="revisions-page"
      className="flex flex-col gap-6"
      aria-labelledby="revisions-heading"
    >
      {/* Crumb + page head */}
      <div className="flex flex-col gap-3">
        <Link
          href={`/posts/${postId}`}
          className="inline-flex w-fit items-center gap-1 text-xs font-medium text-fg-subtle hover:text-emerald-deep"
        >
          <ChevronLeft aria-hidden="true" width={13} height={13} />
          Back to post
        </Link>
        <div className="flex flex-wrap items-end justify-between gap-6 border-b border-border pb-6">
          <div>
            <Headline
              as="h1"
              id="revisions-heading"
              size="page"
              className="text-[clamp(32px,4vw,44px)]"
            >
              Post <em>history</em>.
            </Headline>
            <p className="mt-[10px] text-sm text-fg-muted">
              Every save lands in the revision log. Restore rolls the post
              back to that version and writes a fresh entry recording the
              rollback.{' '}
              <span className="font-mono text-xs">#{postId}</span>
            </p>
          </div>
        </div>
      </div>

      {/* Restore status — sticky chip above the list */}
      {restore.kind === 'success' ? (
        <div
          data-testid="restore-status"
          className="inline-flex items-center gap-2 self-start rounded-pill border border-emerald/35 bg-emerald-soft px-3 py-1 text-xs font-medium text-emerald-deep"
        >
          <RotateCcw aria-hidden="true" width={12} height={12} />
          Restored revision · {shortId(restore.revisionId)}
        </div>
      ) : null}
      {restore.kind === 'error' ? (
        <div
          data-testid="restore-status"
          role="alert"
          className="inline-flex items-center gap-2 self-start rounded-pill border border-danger/35 bg-danger-soft px-3 py-1 text-xs font-medium text-danger"
        >
          <AlertTriangle aria-hidden="true" width={12} height={12} />
          {restore.message}
        </div>
      ) : null}

      {/* List body */}
      <div
        className="rounded-lg border border-border bg-paper-2 p-2 shadow-xs"
        data-testid="revisions-list"
      >
        {load.kind === 'loading' ? <LoadingRow /> : null}
        {load.kind === 'error' ? <ErrorRow message={load.message} /> : null}
        {load.kind === 'loaded' && load.revisions.length === 0 ? (
          <EmptyRow />
        ) : null}
        {load.kind === 'loaded' && load.revisions.length > 0 ? (
          <ul className="flex flex-col">
            {load.revisions.map((rev, idx) => (
              <RevisionRow
                key={rev.id}
                revision={rev}
                isLast={idx === load.revisions.length - 1}
                isLatest={idx === 0}
                restoreState={restore}
                onConfirm={() =>
                  setRestore({ kind: 'confirming', revisionId: rev.id })
                }
                onCancel={() => setRestore({ kind: 'idle' })}
                onRestore={() => void onRestore(rev.id)}
              />
            ))}
          </ul>
        ) : null}
      </div>
    </section>
  );
}

/* -------------------------------------------------------------------------- */
/* Subcomponents                                                              */
/* -------------------------------------------------------------------------- */

interface RevisionRowProps {
  revision: RevisionView;
  isLast: boolean;
  isLatest: boolean;
  restoreState: RestoreStatus;
  onConfirm: () => void;
  onCancel: () => void;
  onRestore: () => void;
}

function RevisionRow({
  revision,
  isLast,
  isLatest,
  restoreState,
  onConfirm,
  onCancel,
  onRestore,
}: RevisionRowProps): ReactElement {
  const isConfirming =
    restoreState.kind === 'confirming' &&
    restoreState.revisionId === revision.id;
  const isSubmitting =
    restoreState.kind === 'submitting' &&
    restoreState.revisionId === revision.id;

  return (
    <li
      data-testid={`revision-row-${revision.id}`}
      className={
        'flex items-start gap-4 px-4 py-4' +
        (isLast ? '' : ' border-b border-border')
      }
    >
      <div className="mt-[2px] flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-sm bg-paper-3 text-fg-muted">
        <History aria-hidden="true" width={13} height={13} />
      </div>

      <div className="flex flex-1 flex-col gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <KindBadge kind={revision.kind} />
          {isLatest ? (
            <Badge variant="emerald" dot>
              Latest
            </Badge>
          ) : null}
          {revision.is_permanent ? (
            <Badge variant="lavender">Permanent</Badge>
          ) : null}
          {!revision.is_snapshot ? (
            <Badge variant="default">Delta</Badge>
          ) : null}
          <span className="font-mono text-[11px] text-fg-subtle">
            {shortId(revision.id)}
          </span>
        </div>

        <div className="text-sm text-ink">
          {revision.title ? (
            <strong className="font-semibold">{revision.title}</strong>
          ) : (
            <span className="italic text-fg-muted">(untitled)</span>
          )}
          {revision.comment ? (
            <span className="ml-2 text-fg-muted">— {revision.comment}</span>
          ) : null}
        </div>

        <div className="flex flex-wrap items-center gap-3 text-xs text-fg-subtle">
          <span className="inline-flex items-center gap-1">
            <Clock aria-hidden="true" width={12} height={12} />
            <time dateTime={revision.created_at}>
              {formatTimestamp(revision.created_at)}
            </time>
          </span>
          <span className="inline-flex items-center gap-1">
            <UserIcon aria-hidden="true" width={12} height={12} />
            {revision.author_id ? shortId(revision.author_id) : 'system'}
          </span>
        </div>
      </div>

      <div className="flex flex-shrink-0 items-start">
        {isConfirming ? (
          <div className="flex items-center gap-2">
            <Button
              variant="default"
              size="sm"
              onClick={onCancel}
              data-testid={`revision-restore-cancel-${revision.id}`}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              size="sm"
              onClick={onRestore}
              data-testid={`revision-restore-confirm-${revision.id}`}
            >
              Confirm restore
            </Button>
          </div>
        ) : (
          <Button
            variant={isLatest ? 'default' : 'outline'}
            size="sm"
            onClick={onConfirm}
            disabled={isLatest || isSubmitting}
            data-testid={`revision-restore-${revision.id}`}
          >
            {isSubmitting ? (
              <Loader2
                aria-hidden="true"
                width={12}
                height={12}
                className="animate-spin"
              />
            ) : (
              <RotateCcw aria-hidden="true" width={12} height={12} />
            )}
            Restore
          </Button>
        )}
      </div>
    </li>
  );
}

function KindBadge({
  kind,
}: {
  kind: RevisionView['kind'];
}): ReactElement {
  switch (kind) {
    case 'publish':
      return <Badge variant="emerald">Publish</Badge>;
    case 'manual':
      return <Badge variant="ink">Manual</Badge>;
    case 'autosave':
      return <Badge variant="default">Autosave</Badge>;
  }
}

function LoadingRow(): ReactElement {
  return (
    <div
      data-testid="revisions-loading"
      className="flex items-center justify-center gap-2 px-4 py-12 text-sm text-fg-muted"
    >
      <Loader2 aria-hidden="true" width={14} height={14} className="animate-spin" />
      Loading revisions…
    </div>
  );
}

function ErrorRow({ message }: { message: string }): ReactElement {
  return (
    <div
      role="alert"
      data-testid="revisions-error"
      className="flex items-start gap-3 px-4 py-8 text-sm text-danger"
    >
      <AlertTriangle aria-hidden="true" width={14} height={14} className="mt-[2px]" />
      <span>{message}</span>
    </div>
  );
}

function EmptyRow(): ReactElement {
  return (
    <div
      data-testid="revisions-empty"
      className="flex flex-col items-center gap-2 px-4 py-12 text-center text-sm text-fg-muted"
    >
      <History aria-hidden="true" width={18} height={18} />
      <p>No revisions yet — saves will start landing here.</p>
    </div>
  );
}

/* -------------------------------------------------------------------------- */
/* Helpers                                                                    */
/* -------------------------------------------------------------------------- */

/** Pull a useful message out of an error. ApiError carries the
 *  server's structured payload; anything else falls back to .message. */
function extractMessage(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    const payload = err.payload as { error?: { message?: string } } | undefined;
    const apiMsg = payload?.error?.message;
    if (typeof apiMsg === 'string' && apiMsg.length > 0) return apiMsg;
    return err.statusText || fallback;
  }
  if (err instanceof Error && err.message) return err.message;
  return fallback;
}

/** Last 8 chars of a UUID — enough to disambiguate in the UI without
 *  carrying the full 36 chars on every row. */
function shortId(id: string): string {
  if (id.length <= 8) return id;
  return id.slice(-8);
}

/** ISO timestamp → "Mar 4, 2026 · 14:32" in the operator's locale. */
function formatTimestamp(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}
