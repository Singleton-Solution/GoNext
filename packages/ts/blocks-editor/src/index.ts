/**
 * @gonext/blocks-editor — public entry point.
 *
 * Companion to `@gonext/blocks-sdk`. While the SDK stays import-light
 * (types + registry + validator), this package ships the React surfaces
 * the admin app needs to drive the block tree:
 *
 *  - **`<BlockInserter>`** — categorised, searchable picker fed by a
 *    `BlockRegistry`. Calls `onInsert(block)` with a fresh block shape.
 *  - **`<BlockEditCanvas>`** — minimal walker that renders each block in a
 *    tree using the registered type's `edit` component. Handles unknown
 *    types gracefully.
 *  - **`defaultCoreBlocks`** — convenience helper that registers a tiny
 *    pair of placeholder core blocks (`core/paragraph`, `core/heading`)
 *    into a given registry. Enough to make the inserter useful in tests.
 *
 * All components are client components (`'use client'`). Server-component-
 * safe wrappers are explicitly out of scope for this issue.
 */

export {
  BLOCK_INSERTER_PATTERNS_TAB,
  BlockInserter,
  INSERTER_CATEGORIES,
  type BlockInserterProps,
} from './block-inserter.tsx';

export type {
  Pattern,
  PatternRegistry,
} from './pattern-types.ts';

export { clonePatternBlocks } from './pattern-clone.ts';

export type {
  Transform,
  TransformRegistry,
  TransformResult,
} from './transform-types.ts';

export {
  BlockTransformToolbar,
  type BlockTransformToolbarProps,
} from './block-transform-toolbar.tsx';

export {
  BlockEditCanvas,
  clearEditModuleCache,
  type BlockEditCanvasProps,
} from './block-edit-canvas.tsx';

export {
  defaultCoreBlocks,
  headingBlock,
  paragraphBlock,
} from './default-core-blocks.ts';

export {
  LockBanner,
  RecoveryDialog,
  useAutosave,
  usePostLock,
  type AutosavePayload,
  type AutosaveResponse,
  type AutosaveState,
  type AutosaveStatus,
  type LockBannerProps,
  type PostLockHolder,
  type PostLockState,
  type RecoveryDialogProps,
  type UseAutosaveOptions,
} from './autosave/index.ts';
