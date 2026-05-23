'use client';

/**
 * CommentNotice — banner shown after a successful submit to confirm
 * the outcome.
 *
 * Two variants:
 *
 *  - `pending`: the comment was created but landed in 'pending'
 *    moderation. We tell the visitor without leaking the moderation
 *    policy ("a human will review").
 *  - `approved`: the comment is live. The form scrolls to its new
 *    row instead of rendering this notice, but we keep an explicit
 *    success state for the rare case where the optimistic insert
 *    can't find its target (race with revalidation).
 */
import type { ReactElement } from 'react';

interface CommentNoticeProps {
  /** Banner variant. */
  variant: 'pending' | 'approved' | 'error';
  /** Optional message override; defaults below per variant. */
  message?: string;
}

const DEFAULTS: Record<CommentNoticeProps['variant'], string> = {
  pending: 'Your comment is awaiting moderation. It will appear once a moderator approves it.',
  approved: 'Your comment was posted.',
  error: 'Your comment could not be posted. Please try again.',
};

export function CommentNotice({ variant, message }: CommentNoticeProps): ReactElement {
  const text = message ?? DEFAULTS[variant];
  return (
    <div
      className={`gn-comment-notice gn-comment-notice-${variant}`}
      role={variant === 'error' ? 'alert' : 'status'}
      data-gn-comment-notice={variant}
    >
      {text}
    </div>
  );
}
