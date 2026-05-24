/**
 * StatusBadge — small coloured pill that visualises a comment's
 * moderation state.
 *
 * Kept as its own component (rather than inlined into the list
 * row) because the comment detail surface needs the same badge,
 * and the future "thread view" will also render it.
 */
import type { ReactElement } from 'react';
import type { CommentStatus } from '../types';
import styles from '../comments.module.css';

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

function classFor(status: CommentStatus): string {
  // CSS module lookups return string | undefined under
  // `noUncheckedIndexedAccess`. A switch keeps the strict-typed
  // expression while staying readable.
  switch (status) {
    case 'pending':
      return styles.badgePending ?? '';
    case 'approved':
      return styles.badgeApproved ?? '';
    case 'spam':
      return styles.badgeSpam ?? '';
    case 'trash':
      return styles.badgeTrash ?? '';
  }
}

export function StatusBadge({ status }: { status: CommentStatus }): ReactElement {
  const label = labelFor(status);
  const klass = classFor(status);
  return (
    <span
      className={`${styles.badge ?? ''} ${klass}`}
      aria-label={`Status: ${label}`}
    >
      {label}
    </span>
  );
}
