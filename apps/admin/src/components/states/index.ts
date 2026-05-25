/**
 * @gonext/admin — shared state-surface components.
 *
 * Every admin and public surface in GoNext consumes these. The five
 * primitives cover the in-between moments of a real product:
 *
 *   <EmptyState />     — "no items here yet" — first run, no results
 *   <LoadingState />   — animated emerald spinner OR skeleton variants
 *   <SkeletonRow />    — single shimmer bar (atom)
 *   <SkeletonText />   — paragraph-shaped placeholder
 *   <SkeletonCard />   — full panel placeholder, default Suspense fallback
 *   <ErrorState />     — calm, lavender, "didn't respond" surface + retry
 *   <NotFoundState />  — 404, page-scale "Not *found*." + Back to safety
 *   <Suspended />      — React.Suspense wrapper with the right defaults
 *
 * Voice rule (HANDOFF.md): "Calm, calibrated, never alarming." The
 * states between actions are where the product feels alive — or
 * doesn't. Read README.md in this directory before adding a new one.
 */
export { EmptyState } from './EmptyState';
export type { EmptyStateProps } from './EmptyState';
export {
  LoadingState,
  SkeletonRow,
  SkeletonText,
  SkeletonCard,
} from './LoadingState';
export type {
  LoadingStateProps,
  SkeletonRowProps,
  SkeletonTextProps,
  SkeletonCardProps,
} from './LoadingState';
export { ErrorState } from './ErrorState';
export type { ErrorStateProps } from './ErrorState';
export { NotFoundState } from './NotFoundState';
export type { NotFoundStateProps } from './NotFoundState';
export { Suspended, resolveFallback } from './Suspended';
export type { SuspendedProps, SuspendedFallback } from './Suspended';
