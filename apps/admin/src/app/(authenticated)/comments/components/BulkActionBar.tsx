'use client';

/**
 * BulkActionBar — selection-aware footer that fires bulk moderation
 * actions against the current selection.
 *
 * The bar lives below the comments table. It is intentionally a
 * separate component so the list keeps its concern (rendering rows)
 * and the toolbar keeps its (filters + bulk). When the selection is
 * empty the bar disables Apply rather than hiding itself — keeping
 * the chrome in place avoids a layout shift the moment a user
 * checks a box.
 *
 * Brand restyle: native <select> is themed to match the paper-3
 * input rest-state; the Apply control uses the emerald variant of
 * the shared Button primitive so it reads as the positive action.
 */
import { useState, type ReactElement } from 'react';

import { Button } from '@/components/ui/button';

import type { BulkAction } from '../types';

export interface BulkActionBarProps {
  /** Selection size, drives the count + disabled-state of Apply. */
  selectedCount: number;
  /** Called with the chosen verb once Apply is pressed. */
  onApply: (action: BulkAction) => void | Promise<void>;
  /** True while a bulk request is in flight; locks the bar. */
  isPending?: boolean;
}

const ACTIONS: readonly { value: BulkAction; label: string }[] = [
  { value: 'approve', label: 'Approve' },
  { value: 'spam', label: 'Mark as spam' },
  { value: 'trash', label: 'Move to trash' },
];

export function BulkActionBar({
  selectedCount,
  onApply,
  isPending = false,
}: BulkActionBarProps): ReactElement {
  const [action, setAction] = useState<BulkAction | ''>('');

  const disabled = isPending || selectedCount === 0 || action === '';

  return (
    <div
      className="inline-flex items-center gap-2"
      role="group"
      aria-label="Bulk actions"
    >
      <label
        htmlFor="bulk-action"
        className="font-sans text-xs font-medium text-fg-subtle"
      >
        Bulk:
      </label>
      <select
        id="bulk-action"
        value={action}
        onChange={(e) => setAction(e.target.value as BulkAction | '')}
        disabled={isPending}
        className="h-8 rounded-sm border border-border bg-paper-3 px-2 font-sans text-xs text-ink transition-colors focus-visible:border-emerald focus-visible:outline-none focus-visible:shadow-focus disabled:cursor-not-allowed disabled:opacity-50"
      >
        <option value="">Choose…</option>
        {ACTIONS.map((a) => (
          <option key={a.value} value={a.value}>
            {a.label}
          </option>
        ))}
      </select>
      <Button
        type="button"
        variant="emerald"
        size="sm"
        onClick={() => {
          if (action !== '') {
            void onApply(action);
          }
        }}
        disabled={disabled}
        aria-disabled={disabled}
      >
        {isPending
          ? 'Applying…'
          : `Apply${selectedCount > 0 ? ` (${selectedCount})` : ''}`}
      </Button>
    </div>
  );
}
