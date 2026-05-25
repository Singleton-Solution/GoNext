/**
 * PerformancePage — brand-application snapshot tests.
 *
 * Asserts the Living-Systems vocabulary reaches the DOM on the
 * Performance surface: Headline with italic-accent, BrandChart
 * primitive rendered per metric (lavender p50/p95 bars + emerald
 * p75 emphasis), monospace window chip toolbar, paper-2 slowest-
 * routes table chrome.
 *
 * The non-brand contracts (data fetching, sample-0 placeholder,
 * window-selector re-fetch) are covered by PerformancePage.test.tsx.
 */
import { render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { PerformancePage } from './PerformancePage';
import type {
  PercentileResult,
  RUMMetric,
  RUMPeriod,
  SlowRoutesResponse,
} from './types';

type PercentileArgs = { metric: RUMMetric; path?: string; period: RUMPeriod };
type SlowRoutesArgs = { metric: RUMMetric; period: RUMPeriod; limit?: number };

function makePercentile(
  metric: RUMMetric,
  p75: number,
  sample = 100,
): PercentileResult {
  return {
    metric,
    period: '24h',
    from: '2026-05-17T00:00:00Z',
    to: '2026-05-18T00:00:00Z',
    p50: p75 * 0.7,
    p75,
    p95: p75 * 1.3,
    sample,
  };
}

function makeSlowRoutes(): SlowRoutesResponse {
  return {
    metric: 'LCP',
    period: '24h',
    from: '2026-05-17T00:00:00Z',
    to: '2026-05-18T00:00:00Z',
    routes: [{ path: '/heavy', metric: 'LCP', p75: 5200, sample: 18 }],
  };
}

describe('PerformancePage brand', () => {
  it('renders the brand Headline with italic <em>performance</em> accent', async () => {
    const percentiles = vi.fn(async (args: PercentileArgs) =>
      makePercentile(args.metric, 2000),
    );
    const slowRoutes = vi.fn(async (_args: SlowRoutesArgs) => makeSlowRoutes());

    render(<PerformancePage fetchers={{ percentiles, slowRoutes }} />);
    await waitFor(() =>
      expect(screen.getByText('Largest Contentful Paint')).toBeInTheDocument(),
    );

    const heading = screen.getByRole('heading', { level: 1 });
    expect(heading.className).toContain('font-display');
    expect(heading.className).toContain('[&_em]:font-serif');
    expect(heading.className).toContain('[&_em]:text-emerald-deep');
    expect(heading.querySelector('em')?.textContent).toBe('performance');
  });

  it('renders a BrandChart with lavender bars and an emerald p75 emphasis', async () => {
    const percentiles = vi.fn(async (args: PercentileArgs) =>
      makePercentile(args.metric, 2000),
    );
    const slowRoutes = vi.fn(async (_args: SlowRoutesArgs) => makeSlowRoutes());

    const { container } = render(
      <PerformancePage fetchers={{ percentiles, slowRoutes }} />,
    );
    await waitFor(() =>
      expect(screen.getByText('Largest Contentful Paint')).toBeInTheDocument(),
    );

    // The BrandChart marks bars with bg-lavender; the p75 bar (the
    // emphasised reading) flips to bg-emerald. Both classes must
    // reach the DOM for the brand chart to be visually correct.
    expect(container.querySelector('.bg-lavender')).not.toBeNull();
    expect(container.querySelector('.bg-emerald')).not.toBeNull();
  });

  it('renders the mono window-range toolbar with the active period highlighted', async () => {
    const percentiles = vi.fn(async (args: PercentileArgs) =>
      makePercentile(args.metric, 2000),
    );
    const slowRoutes = vi.fn(async (_args: SlowRoutesArgs) => makeSlowRoutes());

    render(<PerformancePage fetchers={{ percentiles, slowRoutes }} />);
    await waitFor(() =>
      expect(screen.getByText('Largest Contentful Paint')).toBeInTheDocument(),
    );

    // 24h is the default window — its chip is pressed and carries
    // the active paper surface paint.
    const chip = screen.getByRole('button', { name: '24h' });
    expect(chip.getAttribute('aria-pressed')).toBe('true');
    expect(chip.className).toContain('font-mono');
  });

  it('wraps the slowest-routes table in paper-2 chrome', async () => {
    const percentiles = vi.fn(async (args: PercentileArgs) =>
      makePercentile(args.metric, 2000),
    );
    const slowRoutes = vi.fn(async (_args: SlowRoutesArgs) => makeSlowRoutes());

    render(<PerformancePage fetchers={{ percentiles, slowRoutes }} />);
    await waitFor(() =>
      expect(screen.getByText('/heavy')).toBeInTheDocument(),
    );

    // The /heavy row sits inside a paper-2 table panel; walk up to
    // the wrapper and verify the brand surface class is present.
    const cell = screen.getByText('/heavy');
    const wrapper = cell.closest('.bg-paper-2');
    expect(wrapper).not.toBeNull();
  });
});
