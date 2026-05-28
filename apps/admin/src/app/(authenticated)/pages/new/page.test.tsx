/**
 * /pages/new smoke tests. Mirrors the posts/new shape — issue #507.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';

const pushMock = vi.fn();
vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: pushMock, replace: vi.fn(), prefetch: vi.fn() }),
}));

const apiPostMock = vi.fn();
vi.mock('@/lib/api-client', async () => {
  const actual =
    await vi.importActual<typeof import('@/lib/api-client')>(
      '@/lib/api-client',
    );
  return {
    ...actual,
    api: { ...actual.api, post: (...args: unknown[]) => apiPostMock(...args) },
  };
});

import NewPagePage, { slugifyPage } from './page';
import { ApiError } from '@/lib/api-client';

beforeEach(() => {
  pushMock.mockReset();
  apiPostMock.mockReset();
});

describe('slugifyPage', () => {
  it('emits a leading-slash slug', () => {
    expect(slugifyPage('About Us')).toBe('/about-us');
  });
  it('returns empty for an alphanumeric-less input', () => {
    expect(slugifyPage('   ')).toBe('');
  });
});

describe('NewPagePage', () => {
  it('renders without crashing', () => {
    render(<NewPagePage />);
    expect(screen.getByTestId('new-page-page')).toBeInTheDocument();
  });

  it('renders the italic-accent headline ("Create a new page.")', () => {
    render(<NewPagePage />);
    const h1 = screen.getByRole('heading', { level: 1 });
    expect(h1.textContent).toMatch(/Create\s+a\s+new\s+page\./);
    expect(h1.querySelector('em')?.textContent).toBe('page');
  });

  it('POSTs to /api/v1/posts with post_type:"page" and redirects on success', async () => {
    apiPostMock.mockResolvedValueOnce({ id: 'about' });
    render(<NewPagePage />);

    fireEvent.change(screen.getByLabelText(/Title/i), {
      target: { value: 'About Us' },
    });

    await act(async () => {
      fireEvent.click(screen.getByTestId('new-page-submit'));
    });

    expect(apiPostMock).toHaveBeenCalledWith('/api/v1/posts', {
      title: 'About Us',
      slug: '/about-us',
      status: 'draft',
      post_type: 'page',
      content_blocks: [],
    });
    expect(pushMock).toHaveBeenCalledWith('/pages/about');
  });

  it('renders an inline error when the API rejects the create', async () => {
    apiPostMock.mockRejectedValueOnce(
      new ApiError(422, 'Unprocessable Entity', null),
    );
    render(<NewPagePage />);
    fireEvent.change(screen.getByLabelText(/Title/i), {
      target: { value: 'Bad' },
    });
    await act(async () => {
      fireEvent.click(screen.getByTestId('new-page-submit'));
    });
    expect(screen.getByTestId('new-page-error')).toHaveTextContent(/HTTP 422/);
    expect(pushMock).not.toHaveBeenCalled();
  });
});
