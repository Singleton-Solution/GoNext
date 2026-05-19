/**
 * Tests for the public comments UI.
 *
 * Covers:
 *  - Empty state renders the "be the first to comment" copy.
 *  - Populated state renders each comment with the right author + body.
 *  - Reply button switches the form into reply mode (parent_id sent).
 *  - A successful "pending" submit surfaces the awaiting-moderation notice.
 *  - A successful "approved" submit appends to the visible list.
 *  - "Comments closed" hides the form.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import { CommentsThread } from './CommentsThread';
import type { PublicComment } from './types';

function mk(overrides: Partial<PublicComment> = {}): PublicComment {
  return {
    id: 'c-1',
    post_id: 'p-1',
    path: 'c_1',
    depth: 1,
    author_display_name: 'Jane',
    content: 'hello world',
    created_at: '2026-05-17T09:00:00Z',
    ...overrides,
  };
}

function stubFetchOnce(response: unknown, status = 201): void {
  // @ts-expect-error reassigning fetch in tests
  globalThis.fetch = vi.fn(async () => ({
    ok: status >= 200 && status < 300,
    status,
    json: async () => response,
  }));
}

beforeEach(() => {
  // jsdom doesn't implement scrollIntoView; stub it.
  Element.prototype.scrollIntoView = vi.fn();
});

describe('CommentsThread', () => {
  it('shows the empty state when there are no comments', () => {
    render(<CommentsThread postId="p1" initialComments={[]} />);
    expect(screen.getByText(/Be the first/i)).toBeInTheDocument();
  });

  it('renders each comment with its author and content', () => {
    render(
      <CommentsThread
        postId="p1"
        initialComments={[
          mk({ id: 'a', path: 'a', author_display_name: 'Alice', content: 'first' }),
          mk({ id: 'b', path: 'b', author_display_name: 'Bob', content: 'second' }),
        ]}
      />,
    );
    expect(screen.getByText('Alice')).toBeInTheDocument();
    expect(screen.getByText('Bob')).toBeInTheDocument();
    expect(screen.getByText('first')).toBeInTheDocument();
    expect(screen.getByText('second')).toBeInTheDocument();
  });

  it('renders the heading with the comment count', () => {
    render(
      <CommentsThread
        postId="p1"
        initialComments={[mk({ id: 'a', path: 'a' }), mk({ id: 'b', path: 'b' })]}
      />,
    );
    expect(screen.getByText('2 comments')).toBeInTheDocument();
  });

  it('switches the form to reply mode when Reply is clicked', () => {
    render(
      <CommentsThread
        postId="p1"
        initialComments={[mk({ id: 'a', path: 'a' })]}
      />,
    );
    fireEvent.click(screen.getByText('Reply'));
    const form = screen.getByRole('button', { name: /post comment/i }).closest('form');
    expect(form?.getAttribute('data-gn-comment-form-reply')).toBe('true');
    expect(screen.getByText(/Replying to a comment/i)).toBeInTheDocument();
  });

  it('hides the form when comments are closed', () => {
    render(
      <CommentsThread postId="p1" initialComments={[]} commentsOpen={false} />,
    );
    expect(screen.queryByRole('button', { name: /post comment/i })).toBeNull();
    expect(screen.getByText(/Comments are closed/i)).toBeInTheDocument();
  });

  it('surfaces the awaiting-moderation notice on pending submit', async () => {
    const pendingComment = mk({ id: 'new', path: 'new' });
    stubFetchOnce({ comment: pendingComment, pending: true });
    render(
      <CommentsThread postId="p1" initialComments={[]} />,
    );

    fireEvent.change(screen.getByLabelText(/Name/i), { target: { value: 'Jane' } });
    fireEvent.change(screen.getByLabelText(/^Comment$/i), { target: { value: 'hello' } });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /post comment/i }));
    });

    await waitFor(() => {
      expect(screen.getByText(/awaiting moderation/i)).toBeInTheDocument();
    });
    // Pending row not optimistically added.
    expect(screen.queryByText('hello')).toBeNull();
  });

  it('optimistically appends on approved submit', async () => {
    const newComment = mk({
      id: 'new',
      path: 'new',
      author_display_name: 'Jane',
      content: 'optimistic body',
    });
    stubFetchOnce({ comment: newComment, pending: false });
    render(
      <CommentsThread postId="p1" initialComments={[]} />,
    );

    fireEvent.change(screen.getByLabelText(/Name/i), { target: { value: 'Jane' } });
    fireEvent.change(screen.getByLabelText(/^Comment$/i), { target: { value: 'optimistic body' } });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /post comment/i }));
    });

    await waitFor(() => {
      expect(screen.getByText('optimistic body')).toBeInTheDocument();
    });
    expect(screen.queryByText(/awaiting moderation/i)).toBeNull();
  });

  it('hides name and email fields for authenticated users', () => {
    render(
      <CommentsThread
        postId="p1"
        initialComments={[]}
        isAuthenticated={true}
      />,
    );
    expect(screen.queryByLabelText(/Name/i)).toBeNull();
    expect(screen.queryByLabelText(/Email/i)).toBeNull();
    expect(screen.getByLabelText(/^Comment$/i)).toBeInTheDocument();
  });

  it('sends parent_id on a reply submit', async () => {
    let capturedBody: unknown = null;
    const newComment = mk({ id: 'child', path: 'a.child' });
    // @ts-expect-error reassigning fetch in tests
    globalThis.fetch = vi.fn(async (_url, init) => {
      const reqInit = init as RequestInit;
      capturedBody = JSON.parse(reqInit.body as string);
      return {
        ok: true,
        status: 201,
        json: async () => ({ comment: newComment, pending: false }),
      };
    });
    render(
      <CommentsThread
        postId="p1"
        initialComments={[mk({ id: 'a', path: 'a' })]}
      />,
    );
    fireEvent.click(screen.getByText('Reply'));
    fireEvent.change(screen.getByLabelText(/Name/i), { target: { value: 'Jane' } });
    fireEvent.change(screen.getByLabelText(/^Comment$/i), { target: { value: 'reply body' } });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /post comment/i }));
    });
    await waitFor(() => {
      expect(capturedBody).not.toBeNull();
    });
    expect((capturedBody as { parent_id?: string }).parent_id).toBe('a');
  });
});
