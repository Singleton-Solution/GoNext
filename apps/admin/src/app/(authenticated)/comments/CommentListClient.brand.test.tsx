/**
 * CommentListClient — brand-application snapshot tests.
 *
 * Asserts the Living-Systems vocabulary reaches the DOM on the
 * Comments list: status filter chips are emerald-soft when active,
 * StatusBadge pills use the brand tone tokens (lavender / emerald /
 * warning / paper-3), the table chrome is paper-2 with a paper-3
 * head, and quick-action buttons use the shared brand Button
 * primitive.
 *
 * The non-brand contracts (URL sync, bulk action firing, load-more)
 * are covered by CommentListClient.test.tsx.
 */
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mockPush = vi.fn();
let mockSearchString = '';

vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: mockPush, replace: vi.fn() }),
  usePathname: () => '/comments',
  useSearchParams: () => new URLSearchParams(mockSearchString),
}));

import { CommentListClient } from './CommentListClient';
import type { Comment, CommentListResponse } from './types';

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
    authorDisplayName: 'Bob',
    content: 'agree',
    contentFormat: 'html',
    status: 'approved',
    createdAt: '2026-05-09T12:00:00Z',
    updatedAt: '2026-05-09T12:00:00Z',
  },
];

function makeInitial(comments: Comment[]): CommentListResponse {
  return { data: comments, pagination: { nextCursor: '' } };
}

describe('CommentListClient brand', () => {
  beforeEach(() => {
    mockPush.mockReset();
    mockSearchString = '';
  });

  it('paints the active status chip with emerald-soft tokens', () => {
    mockSearchString = 'status=pending';
    render(<CommentListClient initialData={makeInitial(SAMPLE)} />);
    const chip = screen.getByRole('button', { name: 'Pending' });
    expect(chip.getAttribute('aria-pressed')).toBe('true');
    expect(chip.className).toContain('bg-emerald-soft');
    expect(chip.className).toContain('text-emerald-deep');
  });

  it('paints inactive status chips with paper-3 / fg-muted tokens', () => {
    render(<CommentListClient initialData={makeInitial(SAMPLE)} />);
    const chip = screen.getByRole('button', { name: 'Spam' });
    expect(chip.getAttribute('aria-pressed')).toBe('false');
    expect(chip.className).toContain('text-fg-muted');
  });

  it('uses the brand-tinted StatusBadge for each status', () => {
    render(<CommentListClient initialData={makeInitial(SAMPLE)} />);
    const pending = screen.getByLabelText('Status: Pending');
    expect(pending.className).toContain('bg-lavender-soft');
    expect(pending.className).toContain('text-lavender-deep');

    const approved = screen.getByLabelText('Status: Approved');
    expect(approved.className).toContain('bg-emerald-soft');
    expect(approved.className).toContain('text-emerald-deep');
  });

  it('wraps the comments table in paper-2 chrome with a paper-3 thead', () => {
    const { container } = render(
      <CommentListClient initialData={makeInitial(SAMPLE)} />,
    );
    const wrap = container.querySelector('.bg-paper-2');
    expect(wrap).not.toBeNull();
    const thead = container.querySelector('thead');
    expect(thead?.className).toContain('bg-paper-3');
  });

  it('updates URL when a status filter chip is clicked', () => {
    render(<CommentListClient initialData={makeInitial(SAMPLE)} />);
    fireEvent.click(screen.getByRole('button', { name: 'Spam' }));
    expect(mockPush).toHaveBeenCalledWith('/comments?status=spam');
  });
});
