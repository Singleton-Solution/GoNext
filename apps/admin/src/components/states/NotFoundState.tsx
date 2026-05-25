/**
 * NotFoundState — 404 surface, brand-tokenized.
 *
 * Used wherever a route, resource, or asset can't be found — a deleted
 * post, a moved page, a typo'd permalink. Reuses the cream / forest
 * vocabulary, but bumps the headline to display-scale (44px) so it
 * reads as a *page*, not just a panel — 404s are usually full-route
 * surfaces, and the visual weight signals "this is the page".
 *
 * Visual structure (per `docs/design/ui_kits/states/index.html` and
 * the brand's "Big headline + helpful copy" pattern):
 *
 *   • Big "Not *found*." Headline — Archivo 800 page-scale, with
 *     `found` in Instrument Serif italic / emerald-deep.
 *   • Body copy in Geist --fg-muted.
 *   • Emerald "Back to safety" button (the calming, positive CTA —
 *     "safety" carries the brand voice; alternatives like "Go home"
 *     are also fine).
 *   • Optional `eyebrow` micro-label above the headline ("404 ·
 *     resource not found") for operator clarity.
 *
 * Calmness over alarm: 404s are not errors *as such* — they're a
 * "no such page" outcome. We keep the lavender icon / red colour
 * vocabulary OUT of this state on purpose, because a 404 is not a
 * failure of the system; it's a successful "this address has nothing".
 *
 * The component is `'use client'` because the back-to-safety action
 * can be either a Next.js `<Link>` (default) or a callback — when the
 * user passes `onAction`, we wire a button. When they don't, we link
 * to `/`. Mixing both modes inside a server component would crash on
 * the callback ref, so the whole surface lives on the client.
 */
'use client';

import * as React from 'react';
import Link from 'next/link';
import { Compass, ArrowLeft } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

export interface NotFoundStateProps
  extends Omit<React.HTMLAttributes<HTMLDivElement>, 'title'> {
  /**
   * Override the default "Not *found*." headline. Pass JSX to keep
   * the italic accent: `title={<>Page <em>missing</em>.</>}`. The
   * default copy works for nearly every consumer.
   */
  title?: React.ReactNode;
  /**
   * Body copy in Geist, --fg-muted. Defaults to the brand's
   * "We couldn't find that page. It may have moved, been renamed,
   * or never existed at all." — calibrated, not panicked.
   */
  body?: React.ReactNode;
  /**
   * Eyebrow micro-label rendered above the headline. Defaults to
   * "404 · resource not found". Pass `null` to suppress.
   */
  eyebrow?: React.ReactNode;
  /**
   * Where the "Back to safety" button should go. Defaults to "/" (the
   * admin home). Ignored if `onAction` is provided — a callback wins.
   */
  href?: string;
  /**
   * Action label. Defaults to "Back to safety" — the brand voice's
   * way of saying "go home" without making it sound like a rescue.
   */
  actionLabel?: string;
  /**
   * Callback for the action button. When provided, the component
   * renders a `<button>` instead of a `<Link>` and calls this on
   * click. Useful for in-place soft-recovery (e.g. clearing a stale
   * URL state without a full navigation).
   */
  onAction?: () => void;
}

const DEFAULT_TITLE: React.ReactNode = (
  <>
    Not <em>found</em>.
  </>
);

const DEFAULT_BODY = (
  <>
    We couldn&apos;t find that page. It may have moved, been renamed, or
    never existed at all.
  </>
);

const NotFoundState = React.forwardRef<HTMLDivElement, NotFoundStateProps>(
  (
    {
      title = DEFAULT_TITLE,
      body = DEFAULT_BODY,
      eyebrow = '404 · resource not found',
      href = '/',
      actionLabel = 'Back to safety',
      onAction,
      className,
      ...rest
    },
    ref,
  ): React.ReactElement => {
    return (
      <div
        ref={ref}
        // role="status" because this is a state, not an alert — the
        // user navigated here intentionally (even if by mistake).
        role="status"
        className={cn(
          'flex flex-col overflow-hidden rounded-lg border border-border bg-paper-2 shadow-xs',
          className,
        )}
        data-testid="not-found-state"
        {...rest}
      >
        <div className="flex flex-1 items-center justify-center bg-paper px-6 py-12 min-h-[360px]">
          <div className="max-w-[460px] text-center">
            <div className="mx-auto mb-5 flex h-14 w-14 items-center justify-center rounded-md bg-paper-3">
              <Compass
                size={26}
                strokeWidth={1.75}
                className="text-fg-muted"
                aria-hidden="true"
              />
            </div>

            {eyebrow ? (
              <span
                className={cn(
                  'mb-3 inline-block text-xs font-medium uppercase tracking-[0.12em] text-emerald-deep',
                )}
                data-testid="not-found-state-eyebrow"
              >
                {eyebrow}
              </span>
            ) : null}

            {/*
              Page-scale headline (44px). Bigger than EmptyState's
              28px to mark this as a route-level surface, not an
              in-panel state. The italic accent uses emerald-deep —
              calm, not alarm.
            */}
            <h1
              className={cn(
                'font-display text-[44px] font-extrabold leading-[1.05] tracking-tight text-ink',
                '[&_em]:font-serif [&_em]:italic [&_em]:font-normal',
                '[&_em]:text-[1.05em] [&_em]:tracking-[-0.01em] [&_em]:text-emerald-deep',
              )}
              data-testid="not-found-state-title"
            >
              {title}
            </h1>

            <p
              className="mx-0 mb-[22px] mt-3 text-sm leading-[1.55] text-fg-muted"
              data-testid="not-found-state-body"
            >
              {body}
            </p>

            <div className="inline-flex" data-testid="not-found-state-actions">
              {onAction ? (
                <Button
                  type="button"
                  variant="emerald"
                  onClick={onAction}
                  data-testid="not-found-state-action"
                >
                  <ArrowLeft size={13} strokeWidth={2} aria-hidden="true" />
                  {actionLabel}
                </Button>
              ) : (
                <Button
                  asChild
                  variant="emerald"
                  data-testid="not-found-state-action"
                >
                  <Link href={href}>
                    <ArrowLeft
                      size={13}
                      strokeWidth={2}
                      aria-hidden="true"
                    />
                    {actionLabel}
                  </Link>
                </Button>
              )}
            </div>
          </div>
        </div>
      </div>
    );
  },
);
NotFoundState.displayName = 'NotFoundState';

export { NotFoundState };
