/**
 * `<SortableBlockList>` — wraps a flat list of blocks in a dnd-kit
 * `SortableContext` so authors can drag a block (or a set of
 * multi-selected blocks) to reorder the document.
 *
 * The component is intentionally *next to* `<BlockEditCanvas>`, not
 * inside it. The canvas's job is to render edit components; the
 * sortable wrapper's job is to manage drag affordances. Keeping them
 * decoupled means hosts that don't want drag-drop (e.g. the
 * read-only preview view) just skip mounting this component.
 *
 * Interaction model:
 *
 *   - Each row carries a drag handle. Clicking the handle without
 *     modifiers selects the row; Cmd / Ctrl-click toggles; Shift-
 *     click selects the range from anchor → row.
 *   - Dragging a *selected* row moves all selected rows as a group;
 *     dragging an unselected row first selects only that row then
 *     moves it.
 *   - The drop target is announced via dnd-kit's `arrayMove`
 *     semantics; we apply that move to the multi-selection set as a
 *     whole (gathering the picked items, then re-splicing them into
 *     the new position) before calling `onReorder` with the next id
 *     order.
 *
 * The component is *order-only* — it doesn't know about the rest of
 * the block shape (attributes, innerBlocks). The caller decides how
 * to apply the new id ordering to its `BlockTree` state.
 *
 * Why not put the sortable inside `block-edit-canvas.tsx`? The brief
 * forbids touching it. The integration point lives in
 * `editor-chrome.tsx`: the chrome wraps the canvas in
 * `<SortableBlockList>` and feeds the reordered ids back into the
 * tree it owns.
 */
'use client';

import {
  closestCenter,
  DndContext,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core';
import {
  arrayMove,
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import {
  useMemo,
  type CSSProperties,
  type MouseEvent as ReactMouseEvent,
  type ReactNode,
} from 'react';
import {
  handleSelectionClick,
  SelectionProvider,
  useSelection,
} from './SelectionContext.tsx';

export interface SortableBlockListProps {
  /** Stable ids for the current list, in display order. */
  ids: readonly string[];
  /** Renders the body of a single row (the block's edit surface). */
  renderItem: (id: string, isSelected: boolean) => ReactNode;
  /** Called when the user reorders. Receives the new id list. */
  onReorder: (nextIds: string[]) => void;
  /**
   * When omitted, the component mounts its own `<SelectionProvider>`
   * so it works as a drop-in. Pass `true` if a parent (e.g. the
   * editor chrome) already owns the provider so the outline + canvas
   * share state.
   */
  externalSelectionProvider?: boolean;
  className?: string;
}

export function SortableBlockList(props: SortableBlockListProps) {
  if (props.externalSelectionProvider) {
    return <SortableBlockListInner {...props} />;
  }
  return (
    <SelectionProvider>
      <SortableBlockListInner {...props} />
    </SelectionProvider>
  );
}

function SortableBlockListInner({
  ids,
  renderItem,
  onReorder,
  className,
}: SortableBlockListProps) {
  const selection = useSelection();
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    }),
  );

  // Stable copy of the id list for the SortableContext + range
  // selection. dnd-kit checks identity on the items array between
  // renders, so we memoise on the joined-key — the list is tiny
  // (one entry per block) so JSON-like equality is fine here.
  const orderedIds = useMemo(() => [...ids], [ids.join('|')]);

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event;
    if (over === null || active.id === over.id) return;
    const fromIndex = orderedIds.indexOf(String(active.id));
    const toIndex = orderedIds.indexOf(String(over.id));
    if (fromIndex === -1 || toIndex === -1) return;

    // Multi-drag: if the active id is part of the selection AND there
    // are other selected ids, move the whole bag. Otherwise treat it
    // as a simple single-item arrayMove.
    const activeId = String(active.id);
    const selectionSize = selection.ids.size;
    if (selectionSize > 1 && selection.ids.has(activeId)) {
      // 1. partition the list into "picked" (selected) and "rest"
      const picked = orderedIds.filter((id) => selection.ids.has(id));
      const rest = orderedIds.filter((id) => !selection.ids.has(id));
      // 2. locate the drop target *in `rest`* — the `over.id` may or
      // may not be in `picked`; if it is, snap to the next non-picked
      // slot so the bag lands somewhere sensible.
      let insertAt = rest.indexOf(String(over.id));
      if (insertAt === -1) {
        // over.id was a selected sibling — drop after the last picked
        // sibling's "rest" neighbour.
        insertAt = Math.min(toIndex, rest.length);
      } else if (toIndex > fromIndex) {
        insertAt += 1;
      }
      const next = [
        ...rest.slice(0, insertAt),
        ...picked,
        ...rest.slice(insertAt),
      ];
      onReorder(next);
      return;
    }

    onReorder(arrayMove(orderedIds, fromIndex, toIndex));
  };

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={closestCenter}
      onDragEnd={handleDragEnd}
    >
      <SortableContext
        items={orderedIds}
        strategy={verticalListSortingStrategy}
      >
        <div
          className={className}
          data-testid="sortable-block-list"
          role="list"
        >
          {orderedIds.map((id) => (
            <SortableRow
              key={id}
              id={id}
              orderedIds={orderedIds}
              renderItem={renderItem}
            />
          ))}
        </div>
      </SortableContext>
    </DndContext>
  );
}

interface SortableRowProps {
  id: string;
  orderedIds: readonly string[];
  renderItem: (id: string, isSelected: boolean) => ReactNode;
}

const handleStyle: CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  width: 20,
  height: 20,
  marginRight: 8,
  border: 'none',
  background: 'transparent',
  color: 'var(--fg-muted, #4A5C52)',
  cursor: 'grab',
  borderRadius: 'var(--r-sm, 6px)',
  // The handle stays in the document flow so screen readers can find
  // it — `aria-label` is set on the button itself.
};

const handleStyleHover: CSSProperties = {
  ...handleStyle,
  background: 'var(--paper-3, #E5E0CE)',
};

const selectedRowStyle: CSSProperties = {
  outline: '2px solid var(--emerald, #10B981)',
  outlineOffset: 2,
  borderRadius: 'var(--r-md, 8px)',
};

function SortableRow({ id, orderedIds, renderItem }: SortableRowProps) {
  const selection = useSelection();
  const isSelected = selection.ids.has(id);
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id });

  const style: CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
    position: 'relative',
    padding: 'var(--s-2, 8px) 0',
    ...(isSelected ? selectedRowStyle : {}),
  };

  const onHandleClick = (event: ReactMouseEvent<HTMLButtonElement>) => {
    event.stopPropagation();
    handleSelectionClick(event, id, orderedIds, selection);
  };

  // If the user drags an unselected row, make sure it becomes the
  // active selection before the drag begins. We attach this to the
  // pointer-down on the handle so the selection is set before
  // dnd-kit's activation distance fires.
  const onHandlePointerDown = (event: ReactMouseEvent<HTMLButtonElement>) => {
    if (!isSelected && !event.shiftKey && !event.metaKey && !event.ctrlKey) {
      selection.replace(id);
    }
  };

  return (
    <div
      ref={setNodeRef}
      data-testid={`sortable-row-${id}`}
      data-selected={isSelected ? 'true' : 'false'}
      data-dragging={isDragging ? 'true' : 'false'}
      role="listitem"
      style={style}
    >
      <button
        type="button"
        aria-label={`Drag block ${id}`}
        data-testid={`sortable-handle-${id}`}
        {...attributes}
        {...listeners}
        onClick={onHandleClick}
        onPointerDown={(e) => {
          onHandlePointerDown(e);
          // chain dnd-kit's own listener
          (listeners as { onPointerDown?: (e: unknown) => void })
            ?.onPointerDown?.(e);
        }}
        style={isSelected ? handleStyleHover : handleStyle}
      >
        {/* Six-dot grip glyph; inline SVG so we don't take a lucide dep. */}
        <svg
          aria-hidden="true"
          width="10"
          height="14"
          viewBox="0 0 10 14"
          fill="currentColor"
        >
          <circle cx="2" cy="3" r="1" />
          <circle cx="2" cy="7" r="1" />
          <circle cx="2" cy="11" r="1" />
          <circle cx="8" cy="3" r="1" />
          <circle cx="8" cy="7" r="1" />
          <circle cx="8" cy="11" r="1" />
        </svg>
      </button>
      <div style={{ display: 'inline-block', verticalAlign: 'top' }}>
        {renderItem(id, isSelected)}
      </div>
    </div>
  );
}
