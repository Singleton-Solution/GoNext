/**
 * Autosave + post-lock surface for the block editor.
 *
 * Two hooks and two components, all client-only:
 *
 *  - `useAutosave(postId, blocks, opts)` — POSTs every `intervalMs`
 *    iff blocks have changed; abortable, debounced.
 *  - `usePostLock(postId, opts)` — acquires + heartbeats the
 *    post_lock; surfaces holder info when somebody else has it.
 *  - `<RecoveryDialog>` — on mount, offers "restore unsaved draft"
 *    if the server has a newer autosave.
 *  - `<LockBanner>` — renders when another user holds the lock.
 *
 * The matching server endpoints live in
 * `apps/api/internal/rest/posts/autosave.go`. The persistence schema
 * is `migrations/000016_post_autosaves.up.sql`.
 */
export { useAutosave } from './useAutosave.ts';
export { usePostLock } from './usePostLock.ts';
export { RecoveryDialog, type RecoveryDialogProps } from './RecoveryDialog.tsx';
export { LockBanner, type LockBannerProps } from './LockBanner.tsx';
export {
  AutosaveIndicator,
  relativeTimestamp,
  type AutosaveIndicatorProps,
} from './AutosaveIndicator.tsx';
export type {
  AutosavePayload,
  AutosaveResponse,
  AutosaveState,
  AutosaveStatus,
  PostLockHolder,
  PostLockState,
  UseAutosaveOptions,
} from './types.ts';
