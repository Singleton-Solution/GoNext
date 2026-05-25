/**
 * `SelectionContext` — small React context that holds the set of
 * currently selected block `clientId`s plus the "anchor" id used for
 * shift-click range selection.
 *
 * Why a context instead of a parent-owned ref? Because the consumers
 * are deep in the canvas tree: each `<SortableBlockNode>` listens for
 * its own `aria-selected` flag, and the outline / list view widgets
 * elsewhere in the chrome want to highlight selected rows. A context
 * keeps the prop drilling out of `block-edit-canvas.tsx` (we leave
 * that file alone — single integration point lives in
 * `editor-chrome.tsx`).
 *
 * The state shape is deliberately tiny:
 *
 *   - `ids: Set<string>`  — what's selected right now
 *   - `anchorId: string | null` — the "pivot" for shift-click. Reset
 *     whenever a click happens without modifiers.
 *
 * The reducer exposes three intents:
 *
 *   - `replace(id)` — single-select, used by a bare click
 *   - `toggle(id)` — Cmd / Ctrl click; adds or removes the id, keeps
 *     the anchor at the most-recent toggled id
 *   - `range(id, orderedIds)` — Shift click; selects every id between
 *     the anchor and the clicked id, inclusive. If there's no anchor,
 *     falls back to `replace`.
 *
 * `orderedIds` is supplied by the caller because the canvas owns the
 * block order and the context shouldn't have to mirror it. Passing it
 * per-call avoids a stale-mirror class of bugs.
 */
'use client';

import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from 'react';

export interface SelectionState {
  /** The currently selected client ids. */
  ids: ReadonlySet<string>;
  /** The pivot for shift-click ranges. */
  anchorId: string | null;
}

export interface SelectionActions {
  /** Single-select; clears the set and seeds the anchor with `id`. */
  replace: (id: string) => void;
  /** Toggle membership; the anchor moves to whichever id was acted on. */
  toggle: (id: string) => void;
  /** Range select from anchor → `id` in `orderedIds`. */
  range: (id: string, orderedIds: readonly string[]) => void;
  /** Clear everything. */
  clear: () => void;
}

export type SelectionContextValue = SelectionState & SelectionActions;

const SelectionCtx = createContext<SelectionContextValue | null>(null);

export interface SelectionProviderProps {
  children: ReactNode;
  /** Optional initial set, used for tests + restored sessions. */
  initialIds?: readonly string[];
}

/**
 * The provider keeps the state in `useState` rather than `useReducer`
 * because the three actions don't share any branching state — each
 * one is essentially a one-liner. `useReducer` would just add a layer.
 */
export function SelectionProvider({
  children,
  initialIds = [],
}: SelectionProviderProps) {
  const [ids, setIds] = useState<ReadonlySet<string>>(
    () => new Set(initialIds),
  );
  const [anchorId, setAnchorId] = useState<string | null>(
    initialIds.length > 0 ? (initialIds[initialIds.length - 1] ?? null) : null,
  );

  const replace = useCallback((id: string) => {
    setIds(new Set([id]));
    setAnchorId(id);
  }, []);

  const toggle = useCallback((id: string) => {
    setIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
    setAnchorId(id);
  }, []);

  const range = useCallback(
    (id: string, orderedIds: readonly string[]) => {
      setAnchorId((currentAnchor) => {
        // No anchor → behave like `replace`. The caller would otherwise
        // have to special-case the very first shift-click.
        if (currentAnchor === null) {
          setIds(new Set([id]));
          return id;
        }
        const fromIndex = orderedIds.indexOf(currentAnchor);
        const toIndex = orderedIds.indexOf(id);
        if (fromIndex === -1 || toIndex === -1) {
          // Anchor is no longer in the tree (e.g. a delete since the
          // last click). Reset to a single-select.
          setIds(new Set([id]));
          return id;
        }
        const [lo, hi] =
          fromIndex < toIndex ? [fromIndex, toIndex] : [toIndex, fromIndex];
        const next = new Set<string>();
        for (let i = lo; i <= hi; i++) {
          const slot = orderedIds[i];
          if (slot !== undefined) next.add(slot);
        }
        setIds(next);
        // Keep the original anchor — shift-clicking around should
        // pivot off the *original* fix point, not the most recent
        // hover. This matches Gmail / Finder muscle memory.
        return currentAnchor;
      });
    },
    [],
  );

  const clear = useCallback(() => {
    setIds(new Set());
    setAnchorId(null);
  }, []);

  const value = useMemo<SelectionContextValue>(
    () => ({ ids, anchorId, replace, toggle, range, clear }),
    [ids, anchorId, replace, toggle, range, clear],
  );

  return (
    <SelectionCtx.Provider value={value}>{children}</SelectionCtx.Provider>
  );
}

/**
 * Read the selection state from any descendant. Throws if used
 * outside a `<SelectionProvider>` — loud failure beats silent no-op.
 */
export function useSelection(): SelectionContextValue {
  const ctx = useContext(SelectionCtx);
  if (ctx === null) {
    throw new Error(
      'useSelection() called outside <SelectionProvider>. ' +
        'Wrap the canvas in a <SortableBlockList> or mount the provider ' +
        'in editor-chrome.tsx before reading selection state.',
    );
  }
  return ctx;
}

/**
 * Translate a mouse event's modifier state into the right action.
 * Centralised here so the canvas, outline, and list view all share
 * the same selection semantics.
 */
export function handleSelectionClick(
  event: { shiftKey: boolean; metaKey: boolean; ctrlKey: boolean },
  id: string,
  orderedIds: readonly string[],
  actions: SelectionActions,
): void {
  if (event.shiftKey) {
    actions.range(id, orderedIds);
  } else if (event.metaKey || event.ctrlKey) {
    actions.toggle(id);
  } else {
    actions.replace(id);
  }
}
