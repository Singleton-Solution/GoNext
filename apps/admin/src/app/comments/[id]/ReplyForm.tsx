'use client';

/**
 * ReplyForm — moderator reply to a single comment.
 *
 * Tiny client island that wraps a textarea + submit button and POSTs
 * to /api/v1/admin/comments/{id}/reply. On success the page is
 * router.refresh()'d so the thread sidebar picks up the new child
 * comment. On failure an inline alert renders with the HTTP status.
 */
import { useRouter } from 'next/navigation';
import {
  useCallback,
  useState,
  type ChangeEvent,
  type FormEvent,
  type ReactElement,
} from 'react';
import { api, ApiError } from '../../api-client';
import styles from '../comments.module.css';

export interface ReplyFormProps {
  commentId: string;
  /** Test seam: replaces the api.post call. */
  poster?: (commentId: string, content: string) => Promise<unknown>;
}

export function ReplyForm({
  commentId,
  poster,
}: ReplyFormProps): ReactElement {
  const router = useRouter();
  const [content, setContent] = useState('');
  const [isPending, setIsPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);

  const handleSubmit = useCallback(
    async (e: FormEvent<HTMLFormElement>): Promise<void> => {
      e.preventDefault();
      const trimmed = content.trim();
      if (trimmed === '' || isPending) return;
      setIsPending(true);
      setError(null);
      setSuccess(false);
      try {
        if (poster) {
          await poster(commentId, trimmed);
        } else {
          await api.post(`/api/v1/admin/comments/${commentId}/reply`, {
            content: trimmed,
          });
        }
        setContent('');
        setSuccess(true);
        router.refresh();
      } catch (err) {
        const msg =
          err instanceof ApiError
            ? `Reply failed (HTTP ${err.status})`
            : 'Reply failed';
        setError(msg);
      } finally {
        setIsPending(false);
      }
    },
    [commentId, content, isPending, poster, router],
  );

  const handleChange = (e: ChangeEvent<HTMLTextAreaElement>): void => {
    setContent(e.target.value);
    if (success) setSuccess(false);
  };

  return (
    <form className={styles.replyForm} onSubmit={handleSubmit}>
      <label htmlFor="reply-content">
        <strong>Reply</strong>
      </label>
      <textarea
        id="reply-content"
        className={styles.replyTextarea}
        value={content}
        onChange={handleChange}
        placeholder="Write a reply…"
        disabled={isPending}
      />
      {error && (
        <p role="alert" className="muted">
          {error}
        </p>
      )}
      {success && (
        <p className="muted" role="status">
          Reply posted.
        </p>
      )}
      <div className={styles.replyActions}>
        <button
          type="submit"
          className={styles.replyButton}
          disabled={isPending || content.trim() === ''}
        >
          {isPending ? 'Posting…' : 'Post reply'}
        </button>
      </div>
    </form>
  );
}
