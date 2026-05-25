/**
 * Dashboard page — render + snapshot test.
 *
 * Pins the brand restyle from the "Living systems" pulse view:
 *  • The page head carries the italic-accent headline ("Site pulse.").
 *  • A live indicator badge with animated dot.
 *  • Four stat tiles (Published, Views, Avg. read time, Drafts) each
 *    rendered as an article element with the correct accessible label.
 *  • The "142 readers, now." pulse card on the forest surface.
 *
 * Recharts/ResizeObserver are stubbed for jsdom — the recharts library
 * relies on DOM features the test environment doesn't ship.
 */
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

class ResizeObserverStub {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}
// jsdom doesn't ship ResizeObserver; recharts' ResponsiveContainer needs it.
globalThis.ResizeObserver = ResizeObserverStub as unknown as typeof ResizeObserver;

vi.mock('next/navigation', () => ({
  usePathname: () => '/',
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    prefetch: vi.fn(),
    refresh: vi.fn(),
  }),
  useSearchParams: () => new URLSearchParams(),
}));

import DashboardPage from './page';

describe('Dashboard page', () => {
  it('renders the brand pulse headline with the italic accent', () => {
    render(<DashboardPage />);
    const headline = screen.getByRole('heading', { level: 1 });
    expect(headline.textContent).toMatch(/Site\s+pulse\./);
    // The italic accent rule lives on <em> inside the headline.
    expect(headline.querySelector('em')?.textContent).toBe('pulse');
  });

  it('renders the four stat tiles', () => {
    render(<DashboardPage />);
    const tiles = [
      'Published, 30d',
      'Views, 30d',
      'Avg. read time',
      'Drafts in progress',
    ];
    for (const label of tiles) {
      expect(screen.getByLabelText(label)).toBeInTheDocument();
    }
  });

  it('renders the live indicator', () => {
    render(<DashboardPage />);
    expect(screen.getByLabelText('Live status')).toBeInTheDocument();
  });

  it('renders the forest "142 readers, now." card', () => {
    render(<DashboardPage />);
    const liveHeading = screen.getByRole('heading', {
      name: /142 readers, now/i,
    });
    expect(liveHeading).toBeInTheDocument();
  });

  it('matches the dashboard structure snapshot', () => {
    const { container } = render(<DashboardPage />);
    // We only snapshot the page-head — the chart subtree is recharts-
    // owned DOM that's unstable across recharts versions, so we
    // intentionally pin only the brand chrome the team controls.
    const head = container.querySelector('[data-testid="dashboard-page"] > div');
    expect(head).toMatchSnapshot();
  });
});
