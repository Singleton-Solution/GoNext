'use client';

/**
 * BulkActions — the action bar that appears above the grid once
 * the operator has selected one or more tiles via the checkbox
 * column.
 *
 * The bar has three behaviours:
 *   1. Show the count of selected items + a "clear" button.
 *   2. A dropdown of bulk ops (Delete, Move, Tag, AI alt-text).
 *      Each op fires the same POST /admin/media/bulk endpoint with
 *      a different `op` discriminator.
 *   3. Surface the per-op result inline so the operator can see
 *      "moved 12, failed 2" without leaving the grid.
 *
 * "Move" deliberately reuses the folder tree (it would be redundant
 * to ship a second target picker), so the operator's drag-to-folder
 * is the primary move flow. The dropdown's Move entry is here for
 * keyboard-only operators and prompts for a target slug.
 */
import { Check, ChevronDown, Loader2, X } from 'lucide-react';
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactElement,
} from 'react';
import { bulkMedia } from '../actions';
import type { BulkResult } from '../types';

export interface BulkActionsProps {
  selectedIds: string[];
  /** Operator pressed "clear" — the grid resets its selection set. */
  onClear: () => void;
  /**
   * A bulk action landed — the grid uses this to refresh the list
   * (deleted rows vanish; moved rows leave the current folder; tagged
   * / alt-text rows update in place).
   */
  onComplete?: (result: BulkResult) => void;
}

type Op = 'delete' | 'move' | 'tag' | 'ai-alt';

interface OpDescriptor {
  value: Op;
  label: string;
  /** Confirmation message the action surfaces before firing. */
  confirm?: (count: number) => string;
  /** True when the op needs an extra prompt for params. */
  needsPrompt?: boolean;
}

const OPS: readonly OpDescriptor[] = [
  {
    value: 'delete',
    label: 'Delete',
    confirm: (n) => `Delete ${n} item${n === 1 ? '' : 's'}?`,
  },
  {
    value: 'move',
    label: 'Move to folder…',
    needsPrompt: true,
  },
  {
    value: 'tag',
    label: 'Add tags…',
    needsPrompt: true,
  },
  {
    value: 'ai-alt',
    label: 'Generate AI alt-text',
    confirm: (n) =>
      `Queue AI alt-text generation for ${n} item${n === 1 ? '' : 's'}?`,
  },
];

export function BulkActions(props: BulkActionsProps): ReactElement | null {
  const { selectedIds, onClear, onComplete } = props;
  const [open, setOpen] = useState<boolean>(false);
  const [running, setRunning] = useState<Op | null>(null);
  const [result, setResult] = useState<BulkResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const ref = useRef<HTMLDivElement | null>(null);

  // Close on outside click. The dropdown sits inside the grid so a
  // click anywhere else collapses it — matching the affordance the
  // rest of the admin dropdowns use.
  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener('mousedown', onClick);
    return () => document.removeEventListener('mousedown', onClick);
  }, [open]);

  const run = useCallback(
    async (op: Op) => {
      setOpen(false);
      setError(null);
      setResult(null);
      const descriptor = OPS.find((o) => o.value === op);
      if (!descriptor) return;

      let params: Record<string, unknown> | undefined;
      if (op === 'move') {
        const target = window.prompt(
          'Target folder UUID (leave blank to move to root):',
        );
        if (target === null) return;
        params = { collection_id: target.trim() === '' ? null : target.trim() };
      } else if (op === 'tag') {
        const raw = window.prompt('Tags (comma-separated):');
        if (raw === null) return;
        const add = raw
          .split(',')
          .map((s) => s.trim())
          .filter((s) => s.length > 0);
        if (add.length === 0) return;
        params = { add };
      }

      if (descriptor.confirm && !window.confirm(descriptor.confirm(selectedIds.length))) {
        return;
      }

      setRunning(op);
      try {
        const res = await bulkMedia({ op, ids: selectedIds, params });
        setResult(res);
        if (onComplete) onComplete(res);
      } catch (err) {
        setError(err instanceof Error ? err.message : 'bulk action failed');
      } finally {
        setRunning(null);
      }
    },
    [selectedIds, onComplete],
  );

  if (selectedIds.length === 0) return null;

  return (
    <div
      data-testid="bulk-actions"
      className="flex flex-wrap items-center gap-3 rounded-lg border border-border bg-paper-2 px-3 py-2"
    >
      <span
        className="font-sans text-sm font-medium text-ink"
        data-testid="bulk-actions-count"
      >
        {selectedIds.length} selected
      </span>
      <button
        type="button"
        onClick={onClear}
        aria-label="Clear selection"
        title="Clear selection"
        data-testid="bulk-actions-clear"
        className="inline-flex h-6 w-6 items-center justify-center rounded-sm border border-border bg-paper text-fg-muted hover:border-border-strong hover:text-ink cursor-pointer"
      >
        <X width={12} height={12} aria-hidden="true" />
      </button>

      <div ref={ref} className="relative">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          disabled={running !== null}
          aria-haspopup="menu"
          aria-expanded={open}
          data-testid="bulk-actions-dropdown"
          className="inline-flex items-center gap-1 rounded-sm border border-border bg-paper px-3 py-1 font-sans text-sm text-ink hover:border-border-strong cursor-pointer disabled:opacity-50 disabled:cursor-wait"
        >
          {running ? (
            <>
              <Loader2 width={14} height={14} className="animate-spin" aria-hidden="true" />
              Working…
            </>
          ) : (
            <>
              Bulk actions
              <ChevronDown width={14} height={14} aria-hidden="true" />
            </>
          )}
        </button>
        {open && (
          <ul
            role="menu"
            data-testid="bulk-actions-menu"
            className="absolute left-0 top-[110%] z-20 m-0 list-none p-1 min-w-[200px] rounded-md border border-border bg-paper shadow-md flex flex-col gap-[2px]"
          >
            {OPS.map((op) => (
              <li key={op.value} role="none">
                <button
                  role="menuitem"
                  type="button"
                  onClick={() => run(op.value)}
                  data-testid={`bulk-action-${op.value}`}
                  className="w-full text-left rounded-sm px-2 py-1 font-sans text-sm text-ink hover:bg-paper-2 cursor-pointer bg-transparent border-0"
                >
                  {op.label}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>

      {result && (
        <span
          className="inline-flex items-center gap-1 font-sans text-xs text-emerald-deep"
          data-testid="bulk-actions-result"
        >
          <Check width={12} height={12} aria-hidden="true" />
          {result.succeeded} succeeded
          {result.failed && Object.keys(result.failed).length > 0 && (
            <span className="text-danger">
              , {Object.keys(result.failed).length} failed
            </span>
          )}
        </span>
      )}

      {error && (
        <span
          role="alert"
          className="font-sans text-xs text-danger"
          data-testid="bulk-actions-error"
        >
          {error}
        </span>
      )}
    </div>
  );
}
