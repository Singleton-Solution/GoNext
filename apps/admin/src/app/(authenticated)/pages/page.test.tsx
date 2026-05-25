/**
 * Pages list snapshot tests.
 *
 * Pins the brand chrome (italic-accent headline, filter chip strip,
 * table structure) without touching the underlying data — the data
 * is a static seed until the pages REST endpoint ships.
 */
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('next/navigation', () => ({
  usePathname: () => '/pages',
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    prefetch: vi.fn(),
    refresh: vi.fn(),
  }),
  useSearchParams: () => new URLSearchParams(),
}));

import PagesPage from './page';

describe('Pages list page', () => {
  it('renders the italic-accent headline', () => {
    render(<PagesPage />);
    const h1 = screen.getByRole('heading', { level: 1 });
    expect(h1.textContent).toMatch(/All\s+pages\./);
    expect(h1.querySelector('em')?.textContent).toBe('pages');
  });

  it('renders the New page primary CTA', () => {
    render(<PagesPage />);
    const cta = screen.getByRole('link', { name: /New page/i });
    expect(cta).toHaveAttribute('href', '/pages/new');
  });

  it('renders the filter chip strip', () => {
    render(<PagesPage />);
    const group = screen.getByRole('group', { name: /Filter by status/i });
    expect(group).toBeInTheDocument();
  });

  it('renders the pages table', () => {
    render(<PagesPage />);
    const table = screen.getByRole('table', { name: 'Pages' });
    expect(table).toBeInTheDocument();
  });

  it('matches the page-head snapshot', () => {
    const { container } = render(<PagesPage />);
    const head = container.querySelector('[data-testid="pages-page"] > div');
    expect(head).toMatchSnapshot();
  });
});
