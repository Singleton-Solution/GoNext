/**
 * Tests for the Posts list client island.
 *
 * Coverage targets from issue #31 acceptance criteria:
 *   - empty state renders when `posts=[]`
 *   - three posts in a sample fixture render
 *   - clicking a status filter chip updates URL search params
 *   - typing into the search box debounces and updates URL search params
 *
 * We mock `next/navigation` because the App Router hooks aren't
 * implemented in jsdom — the same pattern Sidebar.test.tsx uses.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { postEditHref, type Post, type PostListResponse } from './columns';

// Hoisted router stubs so we can inspect calls from inside the
// vi.mock factory and from each test body.
const mockPush = vi.fn();
const mockReplace = vi.fn();
let mockSearchString = '';

vi.mock('next/navigation', () => ({
  useRouter: () => ({
    push: mockPush,
    replace: mockReplace,
  }),
  usePathname: () => '/posts',
  useSearchParams: () => new URLSearchParams(mockSearchString),
}));

import { PostListClient } from './PostListClient';

const SAMPLE_POSTS: Post[] = [
  {
    id: 'p1',
    title: 'Hello World',
    status: 'publish',
    date: '2024-05-10T12:00:00Z',
    author: { id: 'u1', displayName: 'admin' },
    commentsCount: 3,
  },
  {
    id: 'p2',
    title: 'Draft notes',
    status: 'draft',
    date: '2024-05-09T12:00:00Z',
    author: { id: 'u2', displayName: 'editor' },
    commentsCount: 0,
  },
  {
    id: 'p3',
    title: 'Trashed item',
    status: 'trash',
    date: '2024-05-08T12:00:00Z',
    author: { id: 'u1', displayName: 'admin' },
    commentsCount: 1,
  },
];

function makeInitialData(posts: Post[]): PostListResponse {
  return { posts, nextCursor: null, total: posts.length };
}

describe('PostListClient', () => {
  beforeEach(() => {
    mockPush.mockReset();
    mockReplace.mockReset();
    mockSearchString = '';
    vi.useRealTimers();
  });

  it('renders the brand <EmptyState> when posts is empty (first run)', () => {
    render(<PostListClient initialData={makeInitialData([])} />);

    // The brand state surface — see `src/components/states/`. We
    // pin the testid because the visible copy now uses the italic-
    // accent ("Write your *first* post.") which is matched on
    // text content elsewhere in the test below.
    const empty = screen.getByTestId('empty-state');
    expect(empty).toBeInTheDocument();
    expect(empty.textContent).toContain('first');

    // CTA is rendered through Button asChild → Link.
    const cta = screen.getByRole('link', { name: /new post/i });
    expect(cta).toBeInTheDocument();
    expect(cta).toHaveAttribute('href', '/posts/new');
  });

  it('renders three rows from the sample fixture', () => {
    render(<PostListClient initialData={makeInitialData(SAMPLE_POSTS)} />);

    // Title links are the user-facing handle to each row.
    expect(
      screen.getByRole('link', { name: /hello world/i }),
    ).toHaveAttribute('href', '/posts/p1');
    expect(
      screen.getByRole('link', { name: /draft notes/i }),
    ).toHaveAttribute('href', '/posts/p2');
    expect(
      screen.getByRole('link', { name: /trashed item/i }),
    ).toHaveAttribute('href', '/posts/p3');

    // Status badges round-trip from the post.status field.
    expect(screen.getByLabelText('Status: Published')).toBeInTheDocument();
    expect(screen.getByLabelText('Status: Draft')).toBeInTheDocument();
    expect(screen.getByLabelText('Status: Trash')).toBeInTheDocument();

    // Comments count shows up.
    expect(screen.getByRole('cell', { name: '3' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: '0' })).toBeInTheDocument();
  });

  it('updates URL search params when a status filter chip is clicked', () => {
    render(<PostListClient initialData={makeInitialData(SAMPLE_POSTS)} />);

    const draftChip = screen.getByRole('button', { name: 'Drafts' });
    fireEvent.click(draftChip);

    expect(mockPush).toHaveBeenCalledTimes(1);
    expect(mockPush).toHaveBeenCalledWith('/posts?status=draft');
  });

  it('clears the status param when "All" is clicked', () => {
    mockSearchString = 'status=draft';
    render(<PostListClient initialData={makeInitialData(SAMPLE_POSTS)} />);

    fireEvent.click(screen.getByRole('button', { name: 'All' }));

    expect(mockPush).toHaveBeenCalledWith('/posts');
  });

  it('debounces search input and updates URL via router.replace', () => {
    vi.useFakeTimers();
    render(<PostListClient initialData={makeInitialData(SAMPLE_POSTS)} />);

    const input = screen.getByLabelText(/search posts/i);

    fireEvent.change(input, { target: { value: 'h' } });
    fireEvent.change(input, { target: { value: 'he' } });
    fireEvent.change(input, { target: { value: 'hel' } });

    // No commit yet — still inside the debounce window.
    expect(mockReplace).not.toHaveBeenCalled();

    act(() => {
      vi.advanceTimersByTime(250);
    });

    // Exactly one replace, with the final value only.
    expect(mockReplace).toHaveBeenCalledTimes(1);
    expect(mockReplace).toHaveBeenCalledWith('/posts?q=hel');
  });

  it('removes the q param when the search box is cleared', () => {
    vi.useFakeTimers();
    mockSearchString = 'q=hello';
    render(<PostListClient initialData={makeInitialData(SAMPLE_POSTS)} />);

    const input = screen.getByLabelText(/search posts/i);
    fireEvent.change(input, { target: { value: '' } });

    act(() => {
      vi.advanceTimersByTime(250);
    });

    expect(mockReplace).toHaveBeenCalledWith('/posts');
  });

  it('disables Apply until a bulk action is chosen and a row is selected', () => {
    render(<PostListClient initialData={makeInitialData(SAMPLE_POSTS)} />);

    const apply = screen.getByRole('button', { name: /^apply/i });
    expect(apply).toBeDisabled();

    fireEvent.change(screen.getByLabelText(/bulk/i), {
      target: { value: 'trash' },
    });
    expect(apply).toBeDisabled();

    fireEvent.click(screen.getByLabelText('Select Hello World'));
    expect(apply).not.toBeDisabled();
  });

  it('logs the bulk action stub when applied', () => {
    const consoleSpy = vi
      .spyOn(console, 'log')
      .mockImplementation(() => undefined);

    render(<PostListClient initialData={makeInitialData(SAMPLE_POSTS)} />);

    fireEvent.click(screen.getByLabelText('Select Hello World'));
    fireEvent.click(screen.getByLabelText('Select Draft notes'));
    fireEvent.change(screen.getByLabelText(/bulk/i), {
      target: { value: 'trash' },
    });
    fireEvent.click(screen.getByRole('button', { name: /^apply/i }));

    expect(consoleSpy).toHaveBeenCalledWith(
      '[posts] bulk action',
      'trash',
      expect.arrayContaining(['p1', 'p2']),
    );

    consoleSpy.mockRestore();
  });

  it('toggles sort direction when a sortable header is clicked twice', () => {
    render(<PostListClient initialData={makeInitialData(SAMPLE_POSTS)} />);

    fireEvent.click(screen.getByRole('button', { name: /sort by title/i }));
    expect(mockPush).toHaveBeenLastCalledWith('/posts?sort=title');

    mockSearchString = 'sort=title';
    // Re-render with the new URL so the sort state reflects "asc".
    render(<PostListClient initialData={makeInitialData(SAMPLE_POSTS)} />);
    fireEvent.click(
      screen.getAllByRole('button', { name: /sort by title/i })[1]!,
    );
    expect(mockPush).toHaveBeenLastCalledWith('/posts?sort=-title');
  });

  it('shows a "Load more" button only when a cursor is present', () => {
    const { rerender } = render(
      <PostListClient initialData={makeInitialData(SAMPLE_POSTS)} />,
    );
    expect(screen.queryByRole('button', { name: /load more/i })).toBeNull();

    rerender(
      <PostListClient
        initialData={{
          posts: SAMPLE_POSTS,
          nextCursor: 'cursor-abc',
          total: 100,
        }}
      />,
    );
    expect(
      screen.getByRole('button', { name: /load more/i }),
    ).toBeInTheDocument();
  });

  it('appends posts returned by the fetcher when "Load more" is clicked', async () => {
    const more: Post[] = [
      {
        id: 'p4',
        title: 'Fourth post',
        status: 'publish',
        date: '2024-05-07T12:00:00Z',
        author: { id: 'u3', displayName: 'author3' },
        commentsCount: 0,
      },
    ];
    const fetcher = vi.fn(async () => ({
      posts: more,
      nextCursor: null,
      total: 4,
    }));

    render(
      <PostListClient
        initialData={{
          posts: SAMPLE_POSTS,
          nextCursor: 'next-1',
          total: 4,
        }}
        fetcher={fetcher}
      />,
    );

    const loadMore = screen.getByRole('button', { name: /load more/i });
    await act(async () => {
      fireEvent.click(loadMore);
    });

    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(
      screen.getByRole('link', { name: /fourth post/i }),
    ).toBeInTheDocument();
  });

  // ── Regression locks for PR #523 ──────────────────────────────────
  //
  // The post edit href was renamed in PR #523 from
  // `/posts/{id}/edit` → bare `/posts/{id}` because the single-id
  // route IS the editor (no nested /edit segment exists). The change
  // touched both the posts list and the comments table. These tests
  // pin the contract on the producer side so any call site that
  // re-adopts the `/edit` suffix fails loud.

  it('TestPostEditHref_OmitsEditSuffix_Issue523: returns bare /posts/{id} (no /edit segment)', () => {
    // Locks the literal shape postEditHref returns. A regression that
    // re-appends /edit (e.g. via a copy-paste from older code) fails
    // here, in CommentListClient.test.tsx, AND in the row-render
    // tests below.
    expect(postEditHref('p1')).toBe('/posts/p1');
    expect(postEditHref('p1')).not.toMatch(/\/edit$/);
  });

  it('TestPostEditHref_PercentEncodesSpecials_Issue523: id is URL-encoded', () => {
    // Defensive: post ids today are UUIDs but the API contract
    // tolerates arbitrary strings, so the helper must encode them.
    expect(postEditHref('a/b')).toBe('/posts/a%2Fb');
  });

  it('TestPostListRow_TitleLinkOmitsEditSuffix_Issue523: rendered row href matches postEditHref output', () => {
    // Sanity: the row in PostListClient must consume postEditHref, not
    // build its own URL. If the row goes back to template-string
    // concatenation a future refactor could re-introduce /edit.
    render(<PostListClient initialData={makeInitialData(SAMPLE_POSTS)} />);
    const link = screen.getByRole('link', { name: /hello world/i });
    expect(link.getAttribute('href')).toBe(postEditHref('p1'));
    expect(link.getAttribute('href')).not.toMatch(/\/edit$/);
  });

  it('shows an inline error when "Load more" fetcher rejects', async () => {
    const fetcher = vi.fn(async () => {
      throw new Error('boom');
    });

    render(
      <PostListClient
        initialData={{
          posts: SAMPLE_POSTS,
          nextCursor: 'next-1',
          total: 4,
        }}
        fetcher={fetcher}
      />,
    );

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /load more/i }));
    });

    expect(screen.getByRole('alert')).toHaveTextContent(/couldn.?t load more/i);
  });
});
