/**
 * Post detail / edit-metadata page tests.
 *
 * Pins the brand restyle:
 *  • Italic-accent headline ("Edit *post*.")
 *  • Inspector sidebar with the canonical panels
 *  • Crumb back-link
 *  • Status / Schedule / SEO sections all addressable by heading
 */
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('next/navigation', () => ({
  useParams: () => ({ id: 'p1' }),
  usePathname: () => '/posts/p1',
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    prefetch: vi.fn(),
    refresh: vi.fn(),
  }),
  useSearchParams: () => new URLSearchParams(),
}));

import PostDetailPage from './page';

describe('Post detail page', () => {
  it('renders the italic-accent headline', () => {
    render(<PostDetailPage />);
    const h1 = screen.getByRole('heading', { level: 1 });
    expect(h1.textContent).toMatch(/Edit\s+post\./);
    expect(h1.querySelector('em')?.textContent).toBe('post');
  });

  it('renders the inspector sidebar panels', () => {
    render(<PostDetailPage />);
    const inspector = screen.getByLabelText('Post metadata inspector');
    expect(inspector).toBeInTheDocument();
    // Status / Schedule / Categories & tags / SEO headings.
    expect(screen.getByRole('heading', { name: 'Status' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Schedule' })).toBeInTheDocument();
    expect(
      screen.getByRole('heading', { name: /Categories & tags/i }),
    ).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /SEO/i })).toBeInTheDocument();
  });

  it('renders the back link to /posts', () => {
    render(<PostDetailPage />);
    const back = screen.getByRole('link', { name: /Back to posts/i });
    expect(back).toHaveAttribute('href', '/posts');
  });

  it('shows the post id in the subhead', () => {
    render(<PostDetailPage />);
    // The id is rendered inside the subhead as "#p1".
    expect(screen.getByTestId('post-detail').textContent).toMatch(/#p1/);
  });

  it('matches the page-head snapshot', () => {
    const { container } = render(<PostDetailPage />);
    const head = container.querySelector('[data-testid="post-detail"] > div');
    expect(head).toMatchSnapshot();
  });
});
