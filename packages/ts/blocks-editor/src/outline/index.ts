/**
 * `@gonext/blocks-editor/outline` — document outline + flat list view.
 *
 * Two side-panel components:
 *
 *  - `<DocumentOutline>` — tree of just `core/heading` blocks, the
 *    "reader's table of contents". Use this in the chrome's
 *    "Outline" tab; click a row to jump-scroll the canvas.
 *
 *  - `<ListView>` — flat hierarchical tree of *every* block. Hover
 *    syncs with the canvas selection via `onHover`. Use this in the
 *    chrome's "List view" tab for structural editing.
 *
 * Both panels read the block tree once on render and emit selection
 * intents through `onSelect`. They never mutate the tree — that's
 * the host's job.
 */
export {
  buildOutline,
  DocumentOutline,
  type DocumentOutlineProps,
  type OutlineNode,
} from './DocumentOutline.tsx';

export {
  flattenBlocks,
  ListView,
  type ListViewProps,
} from './ListView.tsx';
