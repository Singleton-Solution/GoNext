/**
 * Tests for the Comments list client island.
 *
 * Coverage:
 *   - renders one row per status with the correct chip / badge.
 *   - status chip click syncs to URL.
 *   - selection toggling + bulk action firing.
 *   - quick-action button calls the patcher.
 *   - load-more error path renders inline.
 *
 * `next/navigation` is stubbed because the App Router hooks aren't
 * implemented in jsdom — same pattern as PostListClient.test.tsx.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import type { Comment, CommentListResponse } from './types';

const mockPush = vi.fn();
const mockReplace = vi.fn();
let mockSearchString = '';

vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: mockPush, replace: mockReplace }),
  usePathname: () => '/comments',
  useSearchParams: () => new URLSearchParams(mockSearchString),
}));

import { CommentListClient } from './CommentListClient';

const SAMPLE: Comment[] = [
  {
    id: 'c1',
    postId: 'p1',
    postTitle: 'Hello world',
    path: 'c1',
    authorUserId: 'u1',
    authorDisplayName: 'Alice',
    content: 'great post!',
    contentFormat: 'html',
    status: 'pending',
    createdAt: '2026-05-10T12:00:00Z',
    updatedAt: '2026-05-10T12:00:00Z',
  },
  {
    id: 'c2',
    postId: 'p1',
    postTitle: 'Hello world',
    path: 'c2',
    authorDisplayName: 'Anonymous',
    content: 'totally agree',
    contentFormat: 'html',
    status: 'approved',
    createdAt: '2026-05-09T12:00:00Z',
    updatedAt: '2026-05-09T12:00:00Z',
  },
  {
    id: 'c3',
    postId: 'p2',
    postTitle: 'Second',
    path: 'c3',
    authorDisplayName: 'Spammer',
    content: 'BUY MY THINGS',
    contentFormat: 'html',
    status: 'spam',
    createdAt: '2026-05-08T12:00:00Z',
    updatedAt: '2026-05-08T12:00:00Z',
  },
  {
    id: 'c4',
    postId: 'p2',
    postTitle: 'Second',
    path: 'c4',
    authorDisplayName: 'Carol',
    content: 'deleted thread',
    contentFormat: 'html',
    status: 'trash',
    createdAt: '2026-05-07T12:00:00Z',
    updatedAt: '2026-05-07T12:00:00Z',
  },
];

function makeInitial(comments: Comment[]): CommentListResponse {
  return {
    data: comments,
    pagination: { nextCursor: '' },
  };
}

describe('CommentListClient', () => {
  beforeEach(() => {
    mockPush.mockReset();
    mockReplace.mockReset();
    mockSearchString = '';
  });

  it('renders the empty state when comments is empty', () => {
    render(<CommentListClient initialData={makeInitial([])} />);
    expect(
      screen.getByRole('heading', { name: /no comments yet/i }),
    ).toBeInTheDocument();
  });

  it('renders all statuses with correct badges', () => {
    render(<CommentListClient initialData={makeInitial(SAMPLE)} />);
    expect(screen.getByLabelText('Status: Pending')).toBeInTheDocument();
    expect(screen.getByLabelText('Status: Approved')).toBeInTheDocument();
    expect(screen.getByLabelText('Status: Spam')).toBeInTheDocument();
    expect(screen.getByLabelText('Status: Trash')).toBeInTheDocument();
  });

  it('updates URL when a status filter chip is clicked', () => {
    render(<CommentListClient initialData={makeInitial(SAMPLE)} />);
    fireEvent.click(screen.getByRole('button', { name: 'Pending' }));
    expect(mockPush).toHaveBeenCalledWith('/comments?status=pending');
  });

  it('clears status param when "All" is clicked', () => {
    mockSearchString = 'status=pending';
    render(<CommentListClient initialData={makeInitial(SAMPLE)} />);
    fireEvent.click(screen.getByRole('button', { name: 'All' }));
    expect(mockPush).toHaveBeenCalledWith('/comments');
  });

  it('bulk Apply is disabled until selection + action chosen', () => {
    render(<CommentListClient initialData={makeInitial(SAMPLE)} />);
    const apply = screen.getByRole('button', { name: /^apply/i });
    expect(apply).toBeDisabled();

    fireEvent.change(screen.getByLabelText('Bulk:'), {
      target: { value: 'approve' },
    });
    expect(apply).toBeDisabled();

    fireEvent.click(screen.getByLabelText(/select comment by alice/i));
    expect(apply).not.toBeDisabled();
  });

  it('fires bulk action via the supplied bulker', async () => {
    const bulker = vi.fn(async (ids: string[]) =>
      ids.map((id) => ({
        ...SAMPLE.find((c) => c.id === id)!,
        status: 'approved' as const,
      })),
    );
    render(
      <CommentListClient initialData={makeInitial(SAMPLE)} bulker={bulker} />,
    );

    fireEvent.click(screen.getByLabelText(/select comment by alice/i));
    fireEvent.change(screen.getByLabelText('Bulk:'), {
      target: { value: 'approve' },
    });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /^apply/i }));
    });

    expect(bulker).toHaveBeenCalledTimes(1);
    expect(bulker).toHaveBeenCalledWith(['c1'], 'approve');
  });

  it('quick action button calls patcher with the chosen status', async () => {
    const patcher = vi.fn(async (id: string, status: string) => ({
      ...SAMPLE.find((c) => c.id === id)!,
      status: status as Comment['status'],
    }));
    render(
      <CommentListClient initialData={makeInitial(SAMPLE)} patcher={patcher} />,
    );

    // Alice is currently 'pending'; her Spam button is enabled.
    const spamBtn = screen.getByRole('button', {
      name: /^spam comment by alice$/i,
    });
    await act(async () => {
      fireEvent.click(spamBtn);
    });
    expect(patcher).toHaveBeenCalledWith('c1', 'spam');
  });

  it('quick action button is disabled when the comment is already in that status', () => {
    render(<CommentListClient initialData={makeInitial(SAMPLE)} />);
    // c2 is already approved; the Approve button on that row is disabled.
    expect(
      screen.getByRole('button', { name: /approve comment by anonymous/i }),
    ).toBeDisabled();
  });

  it('shows inline error when load-more rejects', async () => {
    const fetcher = vi.fn(async () => {
      throw new Error('boom');
    });
    render(
      <CommentListClient
        initialData={{
          data: SAMPLE,
          pagination: { nextCursor: 'cursor-1' },
        }}
        fetcher={fetcher}
      />,
    );
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /load more/i }));
    });
    expect(screen.getByRole('alert')).toHaveTextContent(/couldn.?t load more/i);
  });

  it('appends comments returned by the fetcher when load-more is clicked', async () => {
    const more: Comment[] = [
      {
        id: 'c5',
        postId: 'p3',
        postTitle: 'Third post',
        path: 'c5',
        authorDisplayName: 'Dan',
        content: 'thanks for sharing',
        contentFormat: 'html',
        status: 'approved',
        createdAt: '2026-05-06T12:00:00Z',
        updatedAt: '2026-05-06T12:00:00Z',
      },
    ];
    const fetcher = vi.fn(async () => ({
      data: more,
      pagination: { nextCursor: '' },
    }));
    render(
      <CommentListClient
        initialData={{
          data: SAMPLE,
          pagination: { nextCursor: 'cursor-1' },
        }}
        fetcher={fetcher}
      />,
    );
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /load more/i }));
    });
    expect(fetcher).toHaveBeenCalledTimes(1);
    // Dan's content is now visible.
    expect(screen.getByText(/thanks for sharing/i)).toBeInTheDocument();
  });

  it('all-rows checkbox selects every row', () => {
    render(<CommentListClient initialData={makeInitial(SAMPLE)} />);
    const selectAll = screen.getByLabelText(/select all comments/i);
    fireEvent.click(selectAll);
    fireEvent.change(screen.getByLabelText('Bulk:'), {
      target: { value: 'trash' },
    });
    // Apply now shows the selected count (4).
    expect(
      screen.getByRole('button', { name: /^apply \(4\)/i }),
    ).toBeInTheDocument();
  });
});
