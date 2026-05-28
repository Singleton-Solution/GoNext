/**
 * /posts/new smoke tests.
 *
 * The full submit flow is covered indirectly via the api-client (mocked
 * with `vi.mock`); here we keep the assertions on the brand surface +
 * happy-path navigation + error-render branch. Issue #507.
 */
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';

// next/navigation hooks live in a module that throws when imported
// outside a Next runtime; stub the surface this page uses.
const pushMock = vi.fn();
vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: pushMock, replace: vi.fn(), prefetch: vi.fn() }),
}));

// Mock the api-client module so the test doesn't hit the network. We
// rebind the resolve / reject value per-test.
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

import NewPostPage, { slugify } from './page';
import { ApiError } from '@/lib/api-client';

beforeEach(() => {
  pushMock.mockReset();
  apiPostMock.mockReset();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('slugify', () => {
  it('lowercases and replaces non-alphanumerics with hyphens', () => {
    expect(slugify('Hello, World!')).toBe('hello-world');
  });
  it('collapses runs and trims edge hyphens', () => {
    expect(slugify('  --  Foo // Bar  --')).toBe('foo-bar');
  });
  it('returns an empty string for an input with no alphanumerics', () => {
    expect(slugify('!!!')).toBe('');
  });
});

describe('NewPostPage — render', () => {
  it('renders without crashing', () => {
    render(<NewPostPage />);
    expect(screen.getByTestId('new-post-page')).toBeInTheDocument();
  });

  it('renders the italic-accent headline ("Write your next post.")', () => {
    render(<NewPostPage />);
    const h1 = screen.getByRole('heading', { level: 1 });
    expect(h1.textContent).toMatch(/Write\s+your\s+next\s+post\./);
    expect(h1.querySelector('em')?.textContent).toBe('next');
  });

  it('shows the create-draft submit button', () => {
    render(<NewPostPage />);
    expect(screen.getByTestId('new-post-submit')).toHaveTextContent(/Create draft/);
  });
});

describe('NewPostPage — submit', () => {
  it('blocks submit when title is empty and surfaces an error', () => {
    render(<NewPostPage />);
    fireEvent.click(screen.getByTestId('new-post-submit'));
    expect(screen.getByTestId('new-post-error')).toHaveTextContent(/title/i);
    expect(apiPostMock).not.toHaveBeenCalled();
  });

  it('POSTs the title/slug/status and routes to /posts/{id} on success', async () => {
    apiPostMock.mockResolvedValueOnce({ id: 'abc-123' });
    render(<NewPostPage />);

    fireEvent.change(screen.getByLabelText(/Title/i), {
      target: { value: 'Hello, World!' },
    });

    await act(async () => {
      fireEvent.click(screen.getByTestId('new-post-submit'));
    });

    expect(apiPostMock).toHaveBeenCalledWith('/api/v1/posts', {
      title: 'Hello, World!',
      slug: 'hello-world',
      status: 'draft',
      content_blocks: [],
    });
    expect(pushMock).toHaveBeenCalledWith('/posts/abc-123');
  });

  it('renders the ApiError message on failure', async () => {
    apiPostMock.mockRejectedValueOnce(
      new ApiError(409, 'Conflict', { message: 'slug taken' }),
    );
    render(<NewPostPage />);
    fireEvent.change(screen.getByLabelText(/Title/i), {
      target: { value: 'Repeat' },
    });

    await act(async () => {
      fireEvent.click(screen.getByTestId('new-post-submit'));
    });

    expect(screen.getByTestId('new-post-error')).toHaveTextContent(/HTTP 409/);
    expect(pushMock).not.toHaveBeenCalled();
  });
});
