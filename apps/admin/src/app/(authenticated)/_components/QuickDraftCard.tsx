/**
 * QuickDraftCard — dashboard widget for capturing a fast post draft
 * without leaving the pulse view.
 *
 * Drops into the "Site pulse" landing page next to the activity rail.
 * The form is intentionally minimal — title + content textarea — and
 * POSTs to `/api/v1/posts` with `status="draft"`. On success the inputs
 * clear and an inline confirmation chip slides in for ~4s; on failure a
 * red chip surfaces the server's error message. We use inline
 * confirmation instead of the global toaster because the dashboard
 * doesn't mount one and the chip stays anchored to the originating
 * form, which is easier to scan than a corner toast.
 *
 * Why a client component? The dashboard page is rendered on the
 * server; the form needs local state and a fetch handler, so it lives
 * in its own `'use client'` island and the dashboard composes it in.
 */
'use client';

import { useEffect, useRef, useState, type ReactElement, type FormEvent } from 'react';
import { Loader2, PenLine, Send } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api, ApiError } from '@/lib/api-client';

type Status =
  | { kind: 'idle' }
  | { kind: 'submitting' }
  | { kind: 'success'; title: string }
  | { kind: 'error'; message: string };

/**
 * Wrap the post body in the minimal block tree the renderer expects.
 * One paragraph block per non-empty line keeps the markdown round-trip
 * faithful without dragging the full markdown parser into this widget.
 */
function toContentBlocks(text: string): unknown {
  const lines = text.split(/\r?\n/).filter((l) => l.trim().length > 0);
  if (lines.length === 0) {
    return { version: 1, blocks: [] };
  }
  return {
    version: 1,
    blocks: lines.map((content) => ({
      type: 'core/paragraph',
      attributes: { content },
    })),
  };
}

export function QuickDraftCard(): ReactElement {
  const [title, setTitle] = useState('');
  const [content, setContent] = useState('');
  const [status, setStatus] = useState<Status>({ kind: 'idle' });
  const dismissTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    return () => {
      if (dismissTimer.current) clearTimeout(dismissTimer.current);
    };
  }, []);

  // Auto-dismiss success / error chips after 4s so the next interaction
  // doesn't start under a stale confirmation.
  useEffect(() => {
    if (status.kind === 'success' || status.kind === 'error') {
      if (dismissTimer.current) clearTimeout(dismissTimer.current);
      dismissTimer.current = setTimeout(
        () => setStatus({ kind: 'idle' }),
        4000,
      );
    }
  }, [status]);

  async function handleSubmit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    const trimmedTitle = title.trim();
    const trimmedContent = content.trim();
    if (trimmedTitle === '' && trimmedContent === '') {
      setStatus({
        kind: 'error',
        message: 'Add a title or some content before saving.',
      });
      return;
    }

    setStatus({ kind: 'submitting' });
    try {
      const draftStatus = 'draft';
      await api.post('/api/v1/posts', {
        status: draftStatus,
        title: trimmedTitle || 'Untitled draft',
        content_blocks: toContentBlocks(trimmedContent),
      });
      setStatus({ kind: 'success', title: trimmedTitle || 'Untitled draft' });
      setTitle('');
      setContent('');
    } catch (err) {
      const message =
        err instanceof ApiError
          ? extractApiErrorMessage(err)
          : err instanceof Error
            ? err.message
            : 'Could not save the draft.';
      setStatus({ kind: 'error', message });
    }
  }

  const isBusy = status.kind === 'submitting';

  return (
    <section
      aria-labelledby="quick-draft-heading"
      data-testid="quick-draft-card"
      className="rounded-lg border border-border bg-paper-2 p-6 shadow-xs"
    >
      <header className="flex items-center gap-2">
        <span
          aria-hidden="true"
          className="flex h-7 w-7 items-center justify-center rounded-sm bg-emerald-soft text-emerald-deep"
        >
          <PenLine width={13} height={13} />
        </span>
        <h3
          id="quick-draft-heading"
          className="font-sans text-sm font-semibold text-ink"
        >
          Quick <em className="font-serif italic text-emerald-deep">draft</em>.
        </h3>
      </header>
      <p className="mt-2 text-xs text-fg-muted">
        Capture an idea before it slips. Saves as a draft you can finish
        later from the posts list.
      </p>

      <form
        onSubmit={handleSubmit}
        className="mt-4 flex flex-col gap-3"
        data-testid="quick-draft-form"
      >
        <div className="flex flex-col gap-1">
          <Label htmlFor="quick-draft-title" className="text-fg-subtle">
            Title
          </Label>
          <Input
            id="quick-draft-title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="What's this about?"
            disabled={isBusy}
            data-testid="quick-draft-title"
          />
        </div>

        <div className="flex flex-col gap-1">
          <Label htmlFor="quick-draft-content" className="text-fg-subtle">
            Content
          </Label>
          <textarea
            id="quick-draft-content"
            value={content}
            onChange={(e) => setContent(e.target.value)}
            rows={4}
            placeholder="Start writing — the rest can come later."
            disabled={isBusy}
            data-testid="quick-draft-content"
            className="rounded-md border border-border bg-paper p-3 font-sans text-sm text-ink outline-none transition-colors placeholder:text-fg-faint focus:border-emerald focus:shadow-focus disabled:cursor-not-allowed disabled:opacity-60"
          />
        </div>

        <div className="flex items-center justify-between gap-3">
          {/* Inline status chip — wide enough for the longest copy we
              produce and stable so the button doesn't jump column when
              it appears. */}
          <div
            aria-live="polite"
            data-testid="quick-draft-status"
            className="min-h-[24px] flex-1 text-xs"
          >
            {status.kind === 'success' ? (
              <span className="inline-flex items-center gap-[6px] rounded-pill border border-emerald/35 bg-emerald-soft px-3 py-[2px] font-medium text-emerald-deep">
                Draft saved · “{status.title}”
              </span>
            ) : null}
            {status.kind === 'error' ? (
              <span className="inline-flex items-center gap-[6px] rounded-pill border border-danger/35 bg-danger-soft px-3 py-[2px] font-medium text-danger">
                {status.message}
              </span>
            ) : null}
          </div>
          <Button
            type="submit"
            variant="emerald"
            disabled={isBusy}
            data-testid="quick-draft-submit"
          >
            {isBusy ? (
              <Loader2
                aria-hidden="true"
                width={14}
                height={14}
                className="animate-spin"
              />
            ) : (
              <Send aria-hidden="true" width={14} height={14} />
            )}
            {isBusy ? 'Saving…' : 'Save draft'}
          </Button>
        </div>
      </form>
    </section>
  );
}

/**
 * Pull a useful message out of an ApiError payload. The REST handlers
 * return `{ error: { code, message } }` for the standard shape; we
 * fall back to the HTTP status text if the payload doesn't match.
 */
function extractApiErrorMessage(err: ApiError): string {
  const payload = err.payload;
  if (payload && typeof payload === 'object') {
    const errField = (payload as { error?: unknown }).error;
    if (errField && typeof errField === 'object') {
      const msg = (errField as { message?: unknown }).message;
      if (typeof msg === 'string' && msg.length > 0) return msg;
    }
    const direct = (payload as { message?: unknown }).message;
    if (typeof direct === 'string' && direct.length > 0) return direct;
  }
  return err.statusText || 'Could not save the draft.';
}
