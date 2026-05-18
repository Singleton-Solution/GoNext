/**
 * Shared types for the autosave + post-lock subsystem.
 *
 * The hooks (`useAutosave`, `usePostLock`) and the UI components
 * (`RecoveryDialog`, `LockBanner`) all consume the same shapes. They
 * live in one file so the surface is easy to scan and the imports
 * stay short.
 */
import type { BlockTree } from '@gonext/blocks-sdk';

/**
 * The lifecycle states `useAutosave` walks through.
 *
 *  - `idle`   — no save in flight, no error pending.
 *  - `saving` — a POST is in flight.
 *  - `saved`  — last save completed successfully. `lastSavedAt` is set.
 *  - `error`  — last save failed. `error` is set.
 *
 * The UI typically renders a tiny status pip in the toolbar from this
 * value, so the order matters: `saving` should win over a stale
 * `error` only after the new request finishes.
 */
export type AutosaveStatus = 'idle' | 'saving' | 'saved' | 'error';

/**
 * Public state shape returned by `useAutosave`. Stable across renders
 * — the hook returns the same object reference when nothing has
 * changed, so callers can use it in `useMemo` deps without thrashing.
 */
export interface AutosaveState {
  status: AutosaveStatus;
  /** Wall-clock time of the last successful save, or null if never. */
  lastSavedAt: Date | null;
  /** The last error message, or null if the most recent save succeeded. */
  error: string | null;
}

/**
 * Options for `useAutosave`. All optional. The defaults match the
 * behaviour described in the design doc: 30s autosave interval, 1500ms
 * debounce on rapid edits.
 */
export interface UseAutosaveOptions {
  /**
   * How often to attempt a save when blocks have changed. Default 30s.
   * The hook will not save more frequently than this even if the user
   * edits faster (that's what the debounce is for).
   */
  intervalMs?: number;
  /**
   * Debounce window for collapsing a burst of edits into a single save.
   * Default 1500ms. We don't fire a save until the user has been
   * idle for at least this long *within* the interval window.
   */
  debounceMs?: number;
  /**
   * The base path for the autosave endpoint. Default `/api/v1/posts`.
   * The page-type mount uses `/api/v1/pages`; the editor app passes
   * the right base when it renders.
   */
  endpointBase?: string;
  /**
   * Injection point for `fetch` so tests can mock without poking
   * `globalThis.fetch`. Defaults to `globalThis.fetch`.
   */
  fetchImpl?: typeof fetch;
}

/**
 * Lock state returned by `usePostLock`. `locked` is true when *this*
 * client holds the lock; `lockedBy` is set when another user holds it
 * (and `locked` is false). They are mutually exclusive: at most one
 * of them carries information at any time.
 */
export interface PostLockState {
  /** True iff this client currently holds the lock. */
  locked: boolean;
  /**
   * The display name of the other user holding the lock, when
   * applicable. `null` when this client holds it, when no lock has
   * been acquired yet, or when we couldn't resolve a name.
   */
  lockedBy: PostLockHolder | null;
  /**
   * Manually trigger a re-acquire. Useful for the "Try Again" button
   * on the LockBanner when the user wants to check whether the other
   * editor's lock has expired.
   */
  refreshLock: () => Promise<void>;
}

/**
 * Identity of a lock holder. The shape mirrors what the API returns
 * from the lock-acquire endpoint; the client never invents fields.
 */
export interface PostLockHolder {
  userId: string;
  displayName: string;
  /** When the lock is expected to expire, ISO-8601. */
  expiresAt: string;
}

/**
 * The autosave POST body. Exported so the integration test fixtures
 * can construct it without re-deriving the shape.
 */
export interface AutosavePayload {
  blocks: BlockTree;
}

/**
 * The autosave GET response shape. Mirrors `posts.Autosave` in Go.
 */
export interface AutosaveResponse {
  post_id: string;
  user_id: string;
  blocks: BlockTree;
  updated_at: string;
}
