/**
 * StatusBadge — small coloured pill that visualises a comment's
 * moderation state.
 *
 * Kept as its own component (rather than inlined into the list
 * row) because the comment detail surface needs the same badge,
 * and the future "thread view" will also render it.
 *
 * Brand tokens (mirrored from docs/design/colors_and_type.css):
 *   pending  → lavender-soft (in-review, awaiting attention)
 *   approved → emerald-soft (active, healthy)
 *   spam     → warning-soft (suspicious, needs caution)
 *   trash    → paper-3 (archived, neutral)
 */
import type { ReactElement } from 'react';

import { cn } from '@/lib/utils';

import type { CommentStatus } from '../types';

function labelFor(status: CommentStatus): string {
  switch (status) {
    case 'pending':
      return 'Pending';
    case 'approved':
      return 'Approved';
    case 'spam':
      return 'Spam';
    case 'trash':
      return 'Trash';
  }
}

const STATUS_CLASS: Record<CommentStatus, string> = {
  pending: 'bg-lavender-soft text-lavender-deep border-lavender/30',
  approved: 'bg-emerald-soft text-emerald-deep border-emerald/30',
  spam: 'bg-warning-soft text-warning border-warning/30',
  trash: 'bg-paper-3 text-fg-subtle border-border',
};

export function StatusBadge({ status }: { status: CommentStatus }): ReactElement {
  const label = labelFor(status);
  return (
    <span
      aria-label={`Status: ${label}`}
      className={cn(
        'inline-flex items-center gap-[6px] rounded-pill border px-2 py-[2px] font-mono text-[10px] font-semibold uppercase tracking-wide',
        STATUS_CLASS[status],
      )}
    >
      <span
        aria-hidden="true"
        className="h-[5px] w-[5px] rounded-pill bg-current"
      />
      {label}
    </span>
  );
}
