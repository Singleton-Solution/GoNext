'use client';

/**
 * CommentForm — the public submit form.
 *
 * Anonymous-friendly. Logged-in users skip name+email; the API derives
 * those from the session. CSRF: the form reads the `csrf` cookie set
 * by the API's `csrf` middleware and echoes it in the
 * `X-CSRF-Token` header.
 *
 * Submit flow:
 *
 *  1. Compose the payload (parent_id, author_name, author_email, content).
 *  2. POST to /api/v1/posts/{postId}/comments with the CSRF header.
 *  3. On 201, the parent calls `onSubmitted(comment, pending)` so it
 *     can either insert the new row optimistically (pending=false)
 *     or render the awaiting-moderation notice (pending=true).
 *  4. On any non-201, surface a generic error notice. We deliberately
 *     keep the message minimal — leaking the API's reason for a
 *     rejection helps spammers learn the rules.
 *
 * The form is a client component because it owns local form state and
 * the fetch lifecycle. It does no rendering of comment bodies — the
 * thread renderer is server-component-friendly.
 */
import type { FormEvent, ReactElement } from 'react';
import { useState } from 'react';
import { readCookie } from './thread';
import type { SubmitResponse } from './types';

interface CommentFormProps {
  /** Owning post id. */
  postId: string;
  /** API base URL (typically NEXT_PUBLIC_API_URL). */
  apiBaseUrl: string;
  /** When set, the form acts as a reply to this comment id. */
  parentId?: string;
  /** Optional callback for the parent's reply-mode tracking. */
  onCancelReply?: () => void;
  /** Whether the visitor is logged in. Skips name+email when true. */
  isAuthenticated?: boolean;
  /** Submit callback invoked on 201. */
  onSubmitted?: (response: SubmitResponse) => void;
}

export function CommentForm({
  postId,
  apiBaseUrl,
  parentId,
  onCancelReply,
  isAuthenticated,
  onSubmitted,
}: CommentFormProps): ReactElement {
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [content, setContent] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (busy) return;
    setBusy(true);
    setError(null);

    // Read CSRF token from the cookie set by the API middleware.
    // SSR runs of this component never reach here ('use client'),
    // so document.cookie is safe to touch.
    const csrf = readCookie('csrf', typeof document !== 'undefined' ? document.cookie : '');

    const payload: Record<string, string> = {
      content,
    };
    if (parentId) payload.parent_id = parentId;
    if (!isAuthenticated) {
      payload.author_name = name;
      if (email) payload.author_email = email;
    }

    try {
      const res = await fetch(`${apiBaseUrl}/api/v1/posts/${encodeURIComponent(postId)}/comments`, {
        method: 'POST',
        credentials: 'include',
        headers: {
          'Content-Type': 'application/json',
          'X-CSRF-Token': csrf,
          Accept: 'application/json',
        },
        body: JSON.stringify(payload),
      });
      if (!res.ok) {
        setError('Your comment could not be posted. Please try again.');
        setBusy(false);
        return;
      }
      const body = (await res.json()) as SubmitResponse;
      onSubmitted?.(body);
      // Reset only the content field — keep name/email so a repeat
      // commenter doesn't have to retype them.
      setContent('');
    } catch {
      setError('A network error prevented your comment from being posted.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <form
      className="gn-comment-form"
      data-gn-comment-form
      data-gn-comment-form-reply={parentId ? 'true' : 'false'}
      onSubmit={handleSubmit}
      aria-busy={busy}
    >
      {parentId && (
        <div className="gn-comment-form-reply-header">
          <span>Replying to a comment.</span>
          {onCancelReply && (
            <button
              type="button"
              className="gn-comment-form-cancel"
              onClick={onCancelReply}
              data-gn-comment-form-cancel
            >
              Cancel reply
            </button>
          )}
        </div>
      )}
      {!isAuthenticated && (
        <>
          <label className="gn-comment-form-field">
            <span>Name</span>
            <input
              type="text"
              required
              maxLength={100}
              value={name}
              onChange={(event): void => setName(event.target.value)}
              autoComplete="name"
              data-gn-comment-form-name
            />
          </label>
          <label className="gn-comment-form-field">
            <span>Email (not published)</span>
            <input
              type="email"
              maxLength={254}
              value={email}
              onChange={(event): void => setEmail(event.target.value)}
              autoComplete="email"
              data-gn-comment-form-email
            />
          </label>
        </>
      )}
      <label className="gn-comment-form-field">
        <span>Comment</span>
        <textarea
          required
          minLength={1}
          maxLength={5000}
          rows={5}
          value={content}
          onChange={(event): void => setContent(event.target.value)}
          data-gn-comment-form-content
        />
      </label>
      <div className="gn-comment-form-actions">
        <button type="submit" disabled={busy} data-gn-comment-form-submit>
          {busy ? 'Posting...' : 'Post comment'}
        </button>
      </div>
      {error && (
        <div className="gn-comment-form-error" role="alert">
          {error}
        </div>
      )}
    </form>
  );
}
