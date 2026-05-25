/**
 * LoadingState — calm "we're getting it" surface, brand-tokenized.
 *
 * Per the design handoff (`docs/design/ui_kits/states/index.html`,
 * `.loading` + `.skel` + `.spinner`), loading is a *brand moment*,
 * not dead air. The shimmer is paper-3 → paper-4 → paper-3 on a
 * 1.6s linear loop, and the live-status row pairs an emerald spinner
 * with a short Geist label.
 *
 * The component ships four pieces:
 *
 *   <LoadingState label="…" />        — top-level "fetching this page"
 *                                       surface, spinner + label,
 *                                       80px min-height. Inert when
 *                                       used inside a Suspense
 *                                       boundary too.
 *
 *   <SkeletonText lines={3} />        — block of N shimmer lines for
 *                                       paragraph-shaped placeholders.
 *                                       Last line shortens automatically.
 *
 *   <SkeletonRow />                   — single 12px shimmer bar with
 *                                       optional `width` — the
 *                                       atom of skeleton placeholders.
 *
 *   <SkeletonCard />                  — full card placeholder: title +
 *                                       3 lines + 80px tile, matches
 *                                       the handoff's `.state` + skel
 *                                       composition. Use as the
 *                                       default Suspense fallback.
 *
 * The shimmer animation is keyframed in `styles/states.css` (linked
 * from `app/globals.css`) because Tailwind v3 can't express the
 * three-stop gradient + background-position animation purely in
 * utilities. Tokens stay tokens — the CSS file references
 * `--paper-3` / `--paper-4` directly, so this stays loyal to the
 * design contract.
 *
 * Voice rule: the label, if any, is calibrated — "Fetching post · 142
 * of 142", not "Loading...". When the consumer doesn't know what to
 * say, omit the label and let the visual carry the moment.
 */
import * as React from 'react';

import { cn } from '@/lib/utils';

export interface LoadingStateProps
  extends React.HTMLAttributes<HTMLDivElement> {
  /**
   * Optional status label rendered next to the emerald spinner. Keep
   * it short and specific — "Fetching post · 142 of 142" beats
   * "Loading...". When omitted, only the spinner is shown.
   */
  label?: React.ReactNode;
  /**
   * `spinner` (default) — just the rotating emerald ring + label, used
   * inline (toolbars, button-level pendings, etc.).
   * `card` — a full <SkeletonCard /> rendered as the loading surface,
   * intended as a Suspense fallback for a whole panel.
   */
  variant?: 'spinner' | 'card';
}

const LoadingState = React.forwardRef<HTMLDivElement, LoadingStateProps>(
  ({ label, variant = 'spinner', className, ...rest }, ref): React.ReactElement => {
    if (variant === 'card') {
      return (
        <div
          ref={ref}
          role="status"
          aria-live="polite"
          aria-busy="true"
          className={cn('flex w-full', className)}
          data-testid="loading-state"
          data-variant="card"
          {...rest}
        >
          <SkeletonCard
            // We expose the label to screenreaders via the parent
            // role="status" so the visually-hidden text stays where
            // assistive tech expects it.
            srLabel={typeof label === 'string' ? label : 'Loading content'}
          />
        </div>
      );
    }
    return (
      <div
        ref={ref}
        role="status"
        aria-live="polite"
        aria-busy="true"
        className={cn(
          'flex min-h-[80px] items-center justify-center gap-2 px-4 py-6 text-sm text-fg-muted',
          className,
        )}
        data-testid="loading-state"
        data-variant="spinner"
        {...rest}
      >
        <Spinner />
        {label ? <span data-testid="loading-state-label">{label}</span> : (
          // Always emit *something* to assistive tech — silent
          // spinners are an accessibility regression.
          <span className="sr-only">Loading</span>
        )}
      </div>
    );
  },
);
LoadingState.displayName = 'LoadingState';

/**
 * Emerald spinner — 14×14 ring with the top edge cut to emerald.
 * Driven by the `gn-spin` keyframe in `styles/states.css`. We keep
 * the markup CSS-free in JSX (no inline styles) so the brand token
 * is the only place colour and timing are defined.
 */
function Spinner(): React.ReactElement {
  return (
    <span
      aria-hidden="true"
      data-testid="loading-spinner"
      className={cn(
        'inline-block h-[14px] w-[14px] shrink-0 rounded-full',
        'border-2 border-paper-3 border-t-emerald',
        'animate-[gn-spin_0.8s_linear_infinite]',
      )}
    />
  );
}

export interface SkeletonRowProps
  extends Omit<React.HTMLAttributes<HTMLSpanElement>, 'children'> {
  /**
   * Bar width — `full` (default, 100%), `mid` (78%), `short` (44%),
   * `title` (60% wide, 36px tall — heading placeholder). Mirrors the
   * `.skel.line.full / .mid / .short / .title` classes in the handoff.
   */
  width?: 'full' | 'mid' | 'short' | 'title';
}

/**
 * <SkeletonRow /> — single shimmer bar. The shimmer animation lives
 * in `styles/states.css` (`@keyframes gn-shimmer`). 12px tall by
 * default; the `title` variant bumps to 36px and 60% width to mimic a
 * page heading placeholder.
 */
const SkeletonRow = React.forwardRef<HTMLSpanElement, SkeletonRowProps>(
  ({ width = 'full', className, ...rest }, ref): React.ReactElement => {
    const heightClass = width === 'title' ? 'h-9 rounded-md' : 'h-3 rounded-sm';
    const widthClass =
      width === 'full'
        ? 'w-full'
        : width === 'mid'
          ? 'w-[78%]'
          : width === 'short'
            ? 'w-[44%]'
            : 'w-[60%]'; // title
    return (
      <span
        ref={ref}
        aria-hidden="true"
        data-testid="skeleton-row"
        data-width={width}
        className={cn(
          'block bg-[length:200%_100%] bg-gradient-to-r from-paper-3 via-paper-4 to-paper-3',
          'animate-[gn-shimmer_1.6s_linear_infinite]',
          heightClass,
          widthClass,
          className,
        )}
        {...rest}
      />
    );
  },
);
SkeletonRow.displayName = 'SkeletonRow';

export interface SkeletonTextProps
  extends React.HTMLAttributes<HTMLDivElement> {
  /**
   * How many lines of body to render. Defaults to 3. The last line is
   * shortened to 44% width to mimic real paragraph end-of-line ragged
   * edges. Lines 2..n-1 use the 78% mid width; line 1 is full width.
   */
  lines?: number;
}

/**
 * <SkeletonText lines={n} /> — paragraph-shaped placeholder. Useful
 * for any inline body-text block that hasn't loaded yet. Stack with
 * <SkeletonRow width="title" /> for headline+body shapes.
 */
const SkeletonText = React.forwardRef<HTMLDivElement, SkeletonTextProps>(
  ({ lines = 3, className, ...rest }, ref): React.ReactElement => {
    // Clamp at 1 to avoid useless empty renders; reasonable upper
    // bound at 12 so a typo can't blow up a page with hundreds of
    // shimmer bars.
    const safeLines = Math.max(1, Math.min(12, Math.floor(lines)));
    return (
      <div
        ref={ref}
        data-testid="skeleton-text"
        className={cn('flex flex-col gap-[14px]', className)}
        {...rest}
      >
        {Array.from({ length: safeLines }).map((_, index) => {
          // Pattern: first line full, last line short, middle lines
          // mid. With lines=1 we just show short so it doesn't read
          // as a wall.
          const widthForLine: SkeletonRowProps['width'] =
            safeLines === 1
              ? 'short'
              : index === 0
                ? 'full'
                : index === safeLines - 1
                  ? 'short'
                  : 'mid';
          return <SkeletonRow key={index} width={widthForLine} />;
        })}
      </div>
    );
  },
);
SkeletonText.displayName = 'SkeletonText';

export interface SkeletonCardProps
  extends React.HTMLAttributes<HTMLDivElement> {
  /**
   * Visually-hidden screenreader label for the loading surface. The
   * card itself is `aria-hidden` so AT users get the label exactly
   * once. Defaults to "Loading".
   */
  srLabel?: string;
}

/**
 * <SkeletonCard /> — full panel placeholder. Title bar + 3 body lines
 * + 80px tile, framed in a paper-2 card. Drop it directly as a
 * Suspense fallback.
 */
const SkeletonCard = React.forwardRef<HTMLDivElement, SkeletonCardProps>(
  ({ srLabel = 'Loading', className, ...rest }, ref): React.ReactElement => {
    return (
      <div
        ref={ref}
        // We split the role: the screenreader gets "status" + label on
        // the outer node; visual skeleton chrome inside is aria-hidden
        // because the announcer doesn't care about empty grey bars.
        role="status"
        aria-live="polite"
        aria-busy="true"
        data-testid="skeleton-card"
        className={cn(
          'flex w-full flex-col gap-[14px] rounded-lg border border-border bg-paper-2 p-6 shadow-xs',
          className,
        )}
        {...rest}
      >
        <span className="sr-only">{srLabel}</span>
        <div aria-hidden="true" className="flex flex-col gap-[14px]">
          <SkeletonRow width="title" />
          <SkeletonRow width="full" />
          <SkeletonRow width="mid" />
          <SkeletonRow width="short" />
          <span
            data-testid="skeleton-card-tile"
            className={cn(
              'mt-[10px] block h-20 rounded-md bg-[length:200%_100%]',
              'bg-gradient-to-r from-paper-3 via-paper-4 to-paper-3',
              'animate-[gn-shimmer_1.6s_linear_infinite]',
            )}
          />
        </div>
      </div>
    );
  },
);
SkeletonCard.displayName = 'SkeletonCard';

export { LoadingState, SkeletonRow, SkeletonText, SkeletonCard };
