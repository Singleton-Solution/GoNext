/**
 * Page detail tests — sibling of posts/[id]/page.test.tsx.
 */
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

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

import PageDetailPage from './page';

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
    expect(screen.getByRole('heading', { name: 'Metadata' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /SEO/i })).toBeInTheDocument();
  });

  it('renders the back link to /pages', () => {
    render(<PageDetailPage />);
    const back = screen.getByRole('link', { name: /Back to pages/i });
    expect(back).toHaveAttribute('href', '/pages');
  });

  it('matches the page-head snapshot', () => {
    const { container } = render(<PageDetailPage />);
    const head = container.querySelector('[data-testid="page-detail"] > div');
    expect(head).toMatchSnapshot();
  });
});
