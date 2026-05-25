/**
 * `@gonext/blocks-editor/dnd` — drag-drop reorder + multi-select.
 *
 * Two public surfaces:
 *
 *  - `<SortableBlockList>` — wraps a flat block-id list in a dnd-kit
 *    `SortableContext`. Authors drag rows (or multi-select groups)
 *    to reorder. The component owns the visual chrome (grip handle,
 *    selection outline) and emits `onReorder(nextIds)`.
 *
 *  - `<SelectionProvider>` + `useSelection()` — small React context
 *    holding the selected client ids and the shift-click anchor. The
 *    canvas, outline, and list view all read from the same context
 *    so highlighting stays in sync across panels.
 *
 * The companion helper `handleSelectionClick(event, id, ids, actions)`
 * does the modifier-key bookkeeping for click handlers that aren't
 * inside `<SortableRow>` (e.g. the document outline tree-view links).
 */
export {
  handleSelectionClick,
  SelectionProvider,
  useSelection,
  type SelectionActions,
  type SelectionContextValue,
  type SelectionProviderProps,
  type SelectionState,
} from './SelectionContext.tsx';

export {
  SortableBlockList,
  type SortableBlockListProps,
} from './SortableBlockList.tsx';
