/**
 * ErrorState — "something didn't respond" surface, brand-tokenized.
 *
 * Per the design handoff (`docs/design/ui_kits/states/index.html`,
 * `.error` recipe) AND the brand voice section of `HANDOFF.md`:
 * errors in GoNext are *calm* — they explain what happened, what was
 * preserved, and what the user can do next. They never alarm, never
 * blame, never apologise in a saccharine way.
 *
 * Visual structure:
 *   • 56×56 lavender-soft icon tile with a Lucide AlertTriangle in
 *     --lavender-deep. *Not* danger-red — that's reserved for
 *     destructive *modals*, not for "we couldn't fetch the list".
 *     The lavender pairs with the brand's secondary accent and reads
 *     as "this is unexpected, but it's fine."
 *   • Headline (Archivo 800, 24px, line-height 1.1) with the italic
 *     accent rule honoured — typical phrasing is
 *     "Something didn't *respond*.". The italic word uses the same
 *     --lavender-deep tone as the icon so the moment feels colour-
 *     coordinated rather than alarmist.
 *   • Optional `code` pill — monospace error code (e.g. "err.503 ·
 *     us-east-1") on a lavender-soft background, sitting *above* the
 *     headline so the operator can spot it without reading prose.
 *   • Body in Geist, --fg-muted, ~1.55 leading.
 *   • Retry action — emerald button (the calm, positive CTA).
 *
 * The retry callback is intentionally optional. Read-only surfaces
 * (a static error page) just show the message; interactive surfaces
 * pass a function and the button appears.
 *
 * Why lavender, not red:
 *   Red carries cultural baggage — danger, blood, stop. A list that
 *   failed to fetch is not dangerous, just unfinished. Reserving red
 *   for destructive confirms (Delete, Discard, Revoke) preserves its
 *   meaning. Lavender signals "off-nominal but composed" — exactly
 *   the brand voice.
 */
'use client';

import * as React from 'react';
import { AlertTriangle, RefreshCw } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

export interface ErrorStateProps
  extends Omit<React.HTMLAttributes<HTMLDivElement>, 'title'> {
  /**
   * Headline content — pass JSX so the italic accent rule applies:
   *   `title={<>Something didn't <em>respond</em>.</>}`
   * A plain string is allowed but loses the italic moment.
   */
  title: React.ReactNode;
  /**
   * Body copy, --fg-muted. Honest and specific — "Our edge in
   * us-east-1 is having a moment. Your draft is saved." beats
   * "An error occurred."
   */
  body: React.ReactNode;
  /**
   * Optional retry handler. If provided, an emerald "Retry" button is
   * rendered with a Lucide RefreshCw icon. The handler can be async
   * — the button stays interactive (no pending state) since the
   * parent typically swaps this surface for <LoadingState> while the
   * retry is in flight.
   */
  retry?: () => void;
  /**
   * Optional secondary action — typically a "Status page" or "Logs"
   * link. Rendered next to the retry button in the default variant.
   */
  secondaryAction?: React.ReactNode;
  /**
   * Optional monospace error code, rendered as a lavender-soft pill
   * above the headline. Useful for operators correlating a UI error
   * with server logs. Example: "err.503 · us-east-1".
   */
  code?: string;
  /**
   * Label for the retry button. Defaults to "Retry". The brand voice
   * prefers single-word, verb-only CTAs ("Retry", not "Try again
   * now!").
   */
  retryLabel?: string;
}

const ErrorState = React.forwardRef<HTMLDivElement, ErrorStateProps>(
  (
    {
      title,
      body,
      retry,
      secondaryAction,
      code,
      retryLabel = 'Retry',
      className,
      ...rest
    },
    ref,
  ): React.ReactElement => {
    return (
      <div
        ref={ref}
        // role="alert" instead of role="status" — screen readers
        // should interrupt to announce errors, vs. politely waiting
        // for the user to be idle (status). aria-live="assertive" is
        // implicit on role="alert".
        role="alert"
        className={cn(
          'flex flex-col overflow-hidden rounded-lg border border-border bg-paper-2 shadow-xs',
          className,
        )}
        data-testid="error-state"
        {...rest}
      >
        <div className="flex flex-1 items-center justify-center bg-paper px-6 py-9 min-h-[280px]">
          <div className="max-w-[380px] text-center">
            <div className="mx-auto mb-[18px] flex h-14 w-14 items-center justify-center rounded-md bg-lavender-soft">
              <AlertTriangle
                size={26}
                strokeWidth={1.75}
                // Lavender-deep, not danger-red. The brand chooses
                // composure over alarm — see the file header.
                className="text-lavender-deep"
                aria-hidden="true"
              />
            </div>

            {code ? (
              <span
                className={cn(
                  'mb-[14px] inline-flex items-center gap-[6px] rounded-full px-[10px] py-1',
                  'font-mono text-xs text-lavender-deep bg-lavender-soft',
                )}
                data-testid="error-state-code"
              >
                <AlertTriangle size={11} strokeWidth={2} aria-hidden="true" />
                {code}
              </span>
            ) : null}

            {/*
              Headline composed inline so the italic accent picks up
              the lavender-deep tone instead of the default emerald.
              The brand uses colour to set mood — lavender on error
              surfaces, emerald on positive surfaces.
            */}
            <h3
              className={cn(
                'font-display text-2xl font-extrabold leading-[1.1] tracking-tight text-ink',
                '[&_em]:font-serif [&_em]:italic [&_em]:font-normal',
                '[&_em]:text-[1.05em] [&_em]:tracking-[-0.01em] [&_em]:text-lavender-deep',
              )}
              data-testid="error-state-title"
            >
              {title}
            </h3>

            <p
              className="mx-0 mb-[18px] mt-[10px] text-sm leading-[1.55] text-fg-muted"
              data-testid="error-state-body"
            >
              {body}
            </p>

            {retry || secondaryAction ? (
              <div
                className="inline-flex gap-2"
                data-testid="error-state-actions"
              >
                {retry ? (
                  <Button
                    type="button"
                    variant="emerald"
                    onClick={retry}
                    data-testid="error-state-retry"
                  >
                    <RefreshCw size={13} strokeWidth={2} aria-hidden="true" />
                    {retryLabel}
                  </Button>
                ) : null}
                {secondaryAction}
              </div>
            ) : null}
          </div>
        </div>
      </div>
    );
  },
);
ErrorState.displayName = 'ErrorState';

export { ErrorState };
