/**
 * Page detail tests — sibling of posts/[id]/page.test.tsx (issue #506).
 *
 * Pins:
 *  • Italic-accent headline ("Edit *page*.")
 *  • Inspector sidebar with the canonical panels
 *  • Save handler PATCHes the API with the right body and surfaces
 *    a success pip on 2xx / an inline error on ApiError.
 */
import {
  describe,
  expect,
  it,
  vi,
  beforeEach,
} from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';

vi.mock('next/navigation', () => ({
  useParams: () => ({ id: 'about' }),
  usePathname: () => '/pages/about',
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    prefetch: vi.fn(),
    refresh: vi.fn(),
  }),
  useSearchParams: () => new URLSearchParams(),
}));

const apiPatchMock = vi.fn();
vi.mock('@/lib/api-client', async () => {
  const actual =
    await vi.importActual<typeof import('@/lib/api-client')>(
      '@/lib/api-client',
    );
  return {
    ...actual,
    api: {
      ...actual.api,
      patch: (...args: unknown[]) => apiPatchMock(...args),
    },
  };
});

import PageDetailPage from './page';
import { ApiError } from '@/lib/api-client';

beforeEach(() => {
  apiPatchMock.mockReset();
});

describe('Page detail page', () => {
  it('renders the italic-accent headline', () => {
    render(<PageDetailPage />);
    const h1 = screen.getByRole('heading', { level: 1 });
    expect(h1.textContent).toMatch(/Edit\s+page\./);
    expect(h1.querySelector('em')?.textContent).toBe('page');
  });

  it('renders the inspector sidebar', () => {
    render(<PageDetailPage />);
    expect(
      screen.getByLabelText('Page metadata inspector'),
    ).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Status' })).toBeInTheDocument();
    expect(
      screen.getByRole('heading', { name: 'Metadata' }),
    ).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /SEO/i })).toBeInTheDocument();
  });

  it('renders the back link to /pages', () => {
    render(<PageDetailPage />);
    const back = screen.getByRole('link', { name: /Back to pages/i });
    expect(back).toHaveAttribute('href', '/pages');
  });

  it('PATCHes /api/v1/posts/{id} with the metadata body on save', async () => {
    apiPatchMock.mockResolvedValueOnce({});
    render(<PageDetailPage />);

    // Title input has a default of "Untitled page" — flip it so we
    // can assert the body carries the operator's edit verbatim.
    fireEvent.change(screen.getByLabelText(/^Title$/), {
      target: { value: 'About Us' },
    });

    await act(async () => {
      fireEvent.click(screen.getByTestId('page-save'));
    });

    // URL is /api/v1/posts/{encodedId} because pages live in the
    // posts table and the dedicated /api/v1/pages mount isn't wired.
    expect(apiPatchMock).toHaveBeenCalledWith(
      '/api/v1/posts/about',
      expect.objectContaining({
        title: 'About Us',
        // The default status is "draft" and the API speaks "draft" —
        // no mapping needed for that arm, but explicit here so the
        // contract is pinned.
        status: 'draft',
      }),
    );

    // Success indicator surfaces.
    expect(screen.getByTestId('page-saved')).toBeInTheDocument();
  });

  it('normalises the publish status to the API "published" label', async () => {
    apiPatchMock.mockResolvedValueOnce({});
    render(<PageDetailPage />);

    fireEvent.change(screen.getByLabelText(/Change to/i), {
      target: { value: 'publish' },
    });
    await act(async () => {
      fireEvent.click(screen.getByTestId('page-save'));
    });

    expect(apiPatchMock).toHaveBeenCalledWith(
      '/api/v1/posts/about',
      expect.objectContaining({ status: 'published' }),
    );
  });

  it('renders the ApiError detail when the save rejects', async () => {
    apiPatchMock.mockRejectedValueOnce(
      new ApiError(412, 'Precondition Failed', null),
    );
    render(<PageDetailPage />);

    await act(async () => {
      fireEvent.click(screen.getByTestId('page-save'));
    });

    const banner = screen.getByTestId('page-save-error');
    expect(banner).toHaveTextContent(/HTTP 412/);
    // No success pip on error.
    expect(screen.queryByTestId('page-saved')).not.toBeInTheDocument();
  });

  it('renders a generic error message when the rejection is not an ApiError', async () => {
    apiPatchMock.mockRejectedValueOnce(new Error('network down'));
    render(<PageDetailPage />);

    await act(async () => {
      fireEvent.click(screen.getByTestId('page-save'));
    });

    expect(screen.getByTestId('page-save-error')).toHaveTextContent(
      /network down/,
    );
  });

  it('matches the page-head snapshot', () => {
    const { container } = render(<PageDetailPage />);
    const head = container.querySelector('[data-testid="page-detail"] > div');
    expect(head).toMatchSnapshot();
  });
});
