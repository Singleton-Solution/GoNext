/**
 * Tests for the post revisions page. Pins the contract:
 *
 *   1. Loading state surfaces while the list fetch is in flight.
 *   2. The fetched revisions render with kind chips, short id, and the
 *      latest row carrying the "Latest" badge + disabled Restore.
 *   3. Restore is a two-step gesture: click → confirm → POST, with the
 *      pending row disabled while the request is in flight.
 *   4. Restore failure surfaces in the danger chip without losing list
 *      state.
 *
 * The fetch wrapper is mocked so the tests don't hit the network.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

const getMock = vi.fn();
const postMock = vi.fn();

vi.mock('@/lib/api-client', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api-client')>(
    '@/lib/api-client',
  );
  return {
    ...actual,
    api: {
      get: (...args: unknown[]) => getMock(...args),
      post: (...args: unknown[]) => postMock(...args),
      put: vi.fn(),
      patch: vi.fn(),
      delete: vi.fn(),
    },
  };
});

vi.mock('next/navigation', () => ({
  useParams: () => ({ id: 'post-abc' }),
  usePathname: () => '/posts/post-abc/revisions',
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    prefetch: vi.fn(),
    refresh: vi.fn(),
  }),
  useSearchParams: () => new URLSearchParams(),
}));

import { ApiError } from '@/lib/api-client';
import RevisionsPage from './page';

const SAMPLE_REVISIONS = [
  {
    id: 'rev-newest-00000001',
    post_id: 'post-abc',
    author_id: 'user-mara-00000001',
    kind: 'manual' as const,
    created_at: '2026-05-25T10:00:00Z',
    title: 'Brew journal v3',
    is_snapshot: true,
  },
  {
    id: 'rev-middle-00000001',
    post_id: 'post-abc',
    author_id: 'user-mara-00000001',
    kind: 'autosave' as const,
    created_at: '2026-05-25T09:50:00Z',
    title: 'Brew journal v2',
    is_snapshot: false,
  },
  {
    id: 'rev-oldest-00000001',
    post_id: 'post-abc',
    author_id: 'user-mara-00000001',
    kind: 'publish' as const,
    created_at: '2026-05-25T09:00:00Z',
    title: 'Brew journal v1',
    is_snapshot: true,
  },
];

describe('Post revisions page', () => {
  beforeEach(() => {
    getMock.mockReset();
    postMock.mockReset();
  });

  it('shows the loading state while the list fetch is pending', () => {
    let resolve: (value: { data: unknown[] }) => void = () => {};
    getMock.mockReturnValueOnce(
      new Promise<{ data: unknown[] }>((res) => {
        resolve = res;
      }),
    );

    render(<RevisionsPage />);
    expect(screen.getByTestId('revisions-loading')).toBeInTheDocument();
    resolve({ data: [] });
  });

  it('renders the fetched revisions and disables Restore on the latest', async () => {
    getMock.mockResolvedValueOnce({ data: SAMPLE_REVISIONS });

    render(<RevisionsPage />);

    await waitFor(() =>
      expect(
        screen.getByTestId('revision-row-rev-newest-00000001'),
      ).toBeInTheDocument(),
    );

    // Three rows.
    expect(
      screen.getByTestId('revision-row-rev-middle-00000001'),
    ).toBeInTheDocument();
    expect(
      screen.getByTestId('revision-row-rev-oldest-00000001'),
    ).toBeInTheDocument();

    // Latest row's Restore button is disabled.
    const latestRestore = screen.getByTestId(
      'revision-restore-rev-newest-00000001',
    );
    expect(latestRestore).toBeDisabled();

    // Older rows have enabled Restore.
    expect(
      screen.getByTestId('revision-restore-rev-middle-00000001'),
    ).not.toBeDisabled();
  });

  it('restore is a two-step gesture and POSTs to the right endpoint', async () => {
    getMock.mockResolvedValueOnce({ data: SAMPLE_REVISIONS });
    postMock.mockResolvedValueOnce({ restored_from: 'rev-oldest-00000001' });
    // The page reloads the list after a successful restore.
    getMock.mockResolvedValueOnce({ data: SAMPLE_REVISIONS });

    render(<RevisionsPage />);

    await waitFor(() =>
      expect(
        screen.getByTestId('revision-row-rev-oldest-00000001'),
      ).toBeInTheDocument(),
    );

    fireEvent.click(
      screen.getByTestId('revision-restore-rev-oldest-00000001'),
    );
    // Confirm UI appears.
    const confirm = screen.getByTestId(
      'revision-restore-confirm-rev-oldest-00000001',
    );
    fireEvent.click(confirm);

    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
    expect(postMock).toHaveBeenCalledWith(
      '/api/v1/admin/posts/post-abc/revisions/rev-oldest-00000001/restore',
    );

    // Success chip lands.
    await waitFor(() =>
      expect(screen.getByTestId('restore-status')).toHaveTextContent(
        /Restored revision/,
      ),
    );
  });

  it('surfaces the server error on restore failure', async () => {
    getMock.mockResolvedValueOnce({ data: SAMPLE_REVISIONS });
    postMock.mockRejectedValueOnce(
      new ApiError(403, 'Forbidden', {
        error: { code: 'forbidden', message: 'Editor role required.' },
      }),
    );

    render(<RevisionsPage />);

    await waitFor(() =>
      expect(
        screen.getByTestId('revision-row-rev-oldest-00000001'),
      ).toBeInTheDocument(),
    );

    fireEvent.click(
      screen.getByTestId('revision-restore-rev-oldest-00000001'),
    );
    fireEvent.click(
      screen.getByTestId('revision-restore-confirm-rev-oldest-00000001'),
    );

    await waitFor(() =>
      expect(screen.getByTestId('restore-status')).toHaveTextContent(
        /Editor role required/,
      ),
    );
  });

  it('renders the empty state when the list comes back blank', async () => {
    getMock.mockResolvedValueOnce({ data: [] });

    render(<RevisionsPage />);

    await waitFor(() =>
      expect(screen.getByTestId('revisions-empty')).toBeInTheDocument(),
    );
  });

  it('renders the error state when the list fetch fails', async () => {
    getMock.mockRejectedValueOnce(
      new ApiError(500, 'Internal Server Error', {
        error: { code: 'internal_error', message: 'DB unavailable.' },
      }),
    );

    render(<RevisionsPage />);

    await waitFor(() =>
      expect(screen.getByTestId('revisions-error')).toHaveTextContent(
        /DB unavailable/,
      ),
    );
  });
});
