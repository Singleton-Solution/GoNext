'use client';

/**
 * ReplyForm — moderator reply to a single comment, restyled against
 * the Living-Systems brand.
 *
 * Tiny client island that wraps a Textarea + Button (shadcn-style
 * brand primitives) and POSTs to /api/v1/admin/comments/{id}/reply.
 * On success the page is router.refresh()'d so the thread sidebar
 * picks up the new child comment. On failure an inline alert
 * renders with the HTTP status; on success a friendly status
 * confirmation replaces it.
 */
import { Send } from 'lucide-react';
import { useRouter } from 'next/navigation';
import {
  useCallback,
  useState,
  type ChangeEvent,
  type FormEvent,
  type ReactElement,
} from 'react';

import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { api, ApiError } from '@/lib/api-client';

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
    <form className="flex flex-col gap-2" onSubmit={handleSubmit}>
      <Label htmlFor="reply-content" className="font-sans text-sm font-semibold text-ink">
        Reply
      </Label>
      <Textarea
        id="reply-content"
        value={content}
        onChange={handleChange}
        placeholder="Write a reply…"
        disabled={isPending}
      />
      {error && (
        <p role="alert" className="m-0 font-sans text-sm text-danger">
          {error}
        </p>
      )}
      {success && (
        <p role="status" className="m-0 font-sans text-sm text-emerald-deep">
          Reply posted.
        </p>
      )}
      <div className="flex justify-end gap-2">
        <Button
          type="submit"
          variant="emerald"
          size="default"
          disabled={isPending || content.trim() === ''}
        >
          <Send aria-hidden="true" className="h-4 w-4" />
          {isPending ? 'Posting…' : 'Post reply'}
        </Button>
      </div>
    </form>
  );
}
