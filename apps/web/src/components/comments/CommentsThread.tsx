'use client';

/**
 * CommentsThread — public-facing comments display + reply UI.
 *
 * Composition:
 *  - Renders the initial list (passed in from the server-rendered
 *    post page) as a recursive tree.
 *  - Owns "currently replying to" state. Clicking a "Reply" button
 *    scrolls to the form and seeds parent_id on the next submit.
 *  - On a successful submit:
 *      * approved: appends the new comment to the local state so it
 *                  appears immediately without a refetch.
 *      * pending: shows the awaiting-moderation notice for one
 *                  render cycle, then keeps the form open so the
 *                  visitor can post another.
 *
 * The component is a client component because it owns interactive
 * state (replying target, optimistic insert, notice variant). The
 * server-rendered post page hands us the initial list as a prop so
 * search engines and no-JS visitors still see the comments — the
 * "use client" only changes the hydration behaviour, not the SSR'd
 * HTML.
 */
import type { ReactElement } from 'react';
import { useCallback, useMemo, useRef, useState } from 'react';
import { CommentForm } from './CommentForm';
import { CommentNotice } from './CommentNotice';
import { buildThread, formatTimestamp } from './thread';
import type { PublicComment, SubmitResponse, ThreadNode } from './types';

interface CommentsThreadProps {
  /** Owning post id — required for the submit endpoint URL. */
  postId: string;
  /** API base URL; falls back to NEXT_PUBLIC_API_URL. */
  apiBaseUrl?: string;
  /** SSR-fetched initial list of comments. */
  initialComments: PublicComment[];
  /** Whether the visitor is logged in. */
  isAuthenticated?: boolean;
  /** When false, hide the form entirely (post has comments closed). */
  commentsOpen?: boolean;
}

const DEFAULT_API_BASE_URL =
  (typeof process !== 'undefined' && process.env.NEXT_PUBLIC_API_URL) || 'http://localhost:8080';

interface RenderedNodeProps {
  node: ThreadNode;
  onReply: (id: string) => void;
}

/**
 * Recursive node renderer. We keep this as a plain function (no
 * useState) so React doesn't re-render the entire subtree when a
 * sibling's optimistic insert lands.
 */
function RenderedNode({ node, onReply }: RenderedNodeProps): ReactElement {
  const { comment, children } = node;
  // Cap depth's visual indent at 6 levels — matches the ltree
  // 6-level cap from docs/01-core-cms.md §6.2. Deeper threads are
  // possible but they look ugly past this.
  const indent = Math.min(comment.depth - 1, 5);
  return (
    <li
      className="gn-comment"
      data-gn-comment-id={comment.id}
      data-gn-comment-depth={comment.depth}
      style={{ marginInlineStart: `${indent * 1.5}rem` }}
    >
      <article className="gn-comment-body">
        <header className="gn-comment-meta">
          <span className="gn-comment-author">{comment.author_display_name}</span>
          <time className="gn-comment-time" dateTime={comment.created_at}>
            {formatTimestamp(comment.created_at, 'en-US')}
          </time>
        </header>
        <div
          className="gn-comment-content"
          // The server pre-sanitised content; safe to render as HTML.
          // eslint-disable-next-line react/no-danger
          dangerouslySetInnerHTML={{ __html: contentToHtml(comment.content) }}
        />
        <footer className="gn-comment-actions">
          <button
            type="button"
            className="gn-comment-reply-button"
            onClick={(): void => onReply(comment.id)}
            data-gn-comment-reply-button={comment.id}
          >
            Reply
          </button>
        </footer>
      </article>
      {children.length > 0 && (
        <ul className="gn-comment-children" data-gn-comment-children>
          {children.map((child) => (
            <RenderedNode key={child.comment.id} node={child} onReply={onReply} />
          ))}
        </ul>
      )}
    </li>
  );
}

/**
 * Linkify naive newlines to <br>. Run after the server already
 * sanitised the content; we don't introduce any new HTML besides
 * the explicit <br/>.
 */
function contentToHtml(content: string): string {
  return content.replace(/\n/g, '<br/>');
}

export function CommentsThread({
  postId,
  apiBaseUrl = DEFAULT_API_BASE_URL,
  initialComments,
  isAuthenticated,
  commentsOpen = true,
}: CommentsThreadProps): ReactElement {
  const [comments, setComments] = useState<PublicComment[]>(initialComments);
  const [replyTo, setReplyTo] = useState<string | undefined>(undefined);
  const [notice, setNotice] = useState<'pending' | 'approved' | null>(null);
  const formRef = useRef<HTMLDivElement>(null);

  const tree = useMemo(() => buildThread(comments), [comments]);

  const handleReply = useCallback((id: string): void => {
    setReplyTo(id);
    // Scroll into view so the visitor finds the form even on a long page.
    if (formRef.current) {
      formRef.current.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }
  }, []);

  const handleSubmitted = useCallback((response: SubmitResponse): void => {
    if (response.pending) {
      setNotice('pending');
      // Don't append to the visible list — moderators may approve it
      // later, at which point a fresh fetch surfaces it.
    } else {
      setNotice('approved');
      setComments((prev) => [...prev, response.comment]);
    }
    setReplyTo(undefined);
  }, []);

  return (
    <section className="gn-comments" data-gn-comments aria-label="Comments">
      <h2 className="gn-comments-heading">
        {comments.length === 0 ? 'Comments' : `${comments.length} comment${comments.length === 1 ? '' : 's'}`}
      </h2>

      {comments.length === 0 ? (
        <p className="gn-comments-empty" data-gn-comments-empty>
          Be the first to leave a comment.
        </p>
      ) : (
        <ul className="gn-comments-list" data-gn-comments-list>
          {tree.map((node) => (
            <RenderedNode key={node.comment.id} node={node} onReply={handleReply} />
          ))}
        </ul>
      )}

      {commentsOpen ? (
        <div className="gn-comments-form-wrap" ref={formRef} data-gn-comments-form-wrap>
          {notice && <CommentNotice variant={notice} />}
          <CommentForm
            postId={postId}
            apiBaseUrl={apiBaseUrl}
            parentId={replyTo}
            onCancelReply={(): void => setReplyTo(undefined)}
            isAuthenticated={isAuthenticated}
            onSubmitted={handleSubmitted}
          />
        </div>
      ) : (
        <p className="gn-comments-closed" data-gn-comments-closed>
          Comments are closed.
        </p>
      )}
    </section>
  );
}
