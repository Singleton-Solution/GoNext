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
 */
import { useState, type ReactElement } from 'react';
import type { BulkAction } from '../types';
import styles from '../comments.module.css';

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
    <div className={styles.bulkBar} role="group" aria-label="Bulk actions">
      <label htmlFor="bulk-action" className="muted">
        Bulk:
      </label>
      <select
        id="bulk-action"
        className={styles.bulkSelect}
        value={action}
        onChange={(e) => setAction(e.target.value as BulkAction | '')}
        disabled={isPending}
      >
        <option value="">Choose…</option>
        {ACTIONS.map((a) => (
          <option key={a.value} value={a.value}>
            {a.label}
          </option>
        ))}
      </select>
      <button
        type="button"
        className={styles.bulkApply}
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
      </button>
    </div>
  );
}
