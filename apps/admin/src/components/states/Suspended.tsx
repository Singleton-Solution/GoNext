/**
 * Suspended — opinionated `<Suspense>` wrapper.
 *
 * The vast majority of Suspense boundaries in the admin want exactly
 * one fallback shape: the brand's <SkeletonCard />. Threading that
 * through every call site is noise — we wrap React.Suspense with a
 * helper so consumers can write
 *
 *   <Suspended>
 *     <SomeAsyncIsland />
 *   </Suspended>
 *
 * …and get the right shimmer card for free.
 *
 * Two escape hatches when the default isn't right:
 *
 *   • `fallback="spinner"` — switch to the inline <LoadingState
 *     variant="spinner"> instead of the full card. Used for small
 *     islands that don't deserve a full-panel placeholder.
 *
 *   • `fallback={<MyCustomFallback />}` — pass any ReactNode to use
 *     it directly. Useful for one-off shapes (e.g. a sidebar list
 *     skeleton with N rows).
 *
 * The component is intentionally a thin wrapper — no portals, no
 * error boundary, no analytics. Errors should land in <ErrorState>
 * via an explicit error boundary at the same nesting level; mixing
 * the two roles inside Suspended would create a sneaky abstraction
 * that's hard to reason about.
 */
import * as React from 'react';

import { LoadingState, SkeletonCard } from './LoadingState';

export type SuspendedFallback = React.ReactNode | 'spinner' | 'card';

export interface SuspendedProps {
  /**
   * The async children whose suspension this boundary catches.
   */
  children: React.ReactNode;
  /**
   * What to render while children suspend.
   *  - `'card'` (default) → `<SkeletonCard />` shimmer
   *  - `'spinner'`        → `<LoadingState variant="spinner" />`
   *  - any ReactNode      → render that node directly
   */
  fallback?: SuspendedFallback;
  /**
   * Optional screenreader label used by the default skeleton card.
   * Forwards to `SkeletonCard.srLabel`. Defaults to "Loading".
   * Ignored when a custom ReactNode fallback is supplied.
   */
  srLabel?: string;
}

/**
 * Resolve the `fallback` prop to a concrete ReactNode. Kept as a
 * pure helper so test snapshots can call it directly without
 * mounting React.
 */
function resolveFallback(
  fallback: SuspendedFallback,
  srLabel?: string,
): React.ReactNode {
  if (fallback === 'spinner') {
    return <LoadingState variant="spinner" label={srLabel ?? 'Loading'} />;
  }
  if (fallback === 'card' || fallback === undefined) {
    return <SkeletonCard srLabel={srLabel ?? 'Loading'} />;
  }
  return fallback;
}

function Suspended({
  children,
  fallback = 'card',
  srLabel,
}: SuspendedProps): React.ReactElement {
  // Cast the fallback ReactNode through React.Suspense's typed slot —
  // it's already a ReactNode so the cast is identity-shaped.
  return (
    <React.Suspense fallback={resolveFallback(fallback, srLabel)}>
      {children}
    </React.Suspense>
  );
}

export { Suspended, resolveFallback };
