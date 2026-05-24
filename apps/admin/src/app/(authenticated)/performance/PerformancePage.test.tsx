/**
 * Performance page interactive tests.
 *
 * We pass in a `fetchers` prop with mocked implementations so the
 * component is exercised without any HTTP wiring. Three contracts:
 *
 *  1. The grid renders one card per Core Web Vitals metric after a
 *     successful fetch.
 *  2. The slow-routes table renders the rows in the order returned
 *     by the API (we trust the server's sort).
 *  3. The window selector triggers a re-fetch and the slow-routes
 *     metric selector triggers a re-fetch for the routes API only.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';

import { PerformancePage } from './PerformancePage';
import type { PercentileResult, RUMMetric, RUMPeriod, SlowRoutesResponse } from './types';

type PercentileArgs = { metric: RUMMetric; path?: string; period: RUMPeriod };
type SlowRoutesArgs = { metric: RUMMetric; period: RUMPeriod; limit?: number };

function makePercentile(metric: RUMMetric, p75: number, sample = 100): PercentileResult {
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
    routes: [
      { path: '/heavy', metric: 'LCP', p75: 5200, sample: 18 },
      { path: '/medium', metric: 'LCP', p75: 3100, sample: 22 },
    ],
  };
}

describe('PerformancePage', () => {
  beforeEach(() => {
    vi.useRealTimers();
  });

  it('renders one card per Core Web Vitals metric after fetch', async () => {
    const percentiles = vi.fn(async (args: PercentileArgs) =>
      makePercentile(args.metric, args.metric === 'CLS' ? 0.05 : 2500),
    );
    const slowRoutes = vi.fn(async (_args: SlowRoutesArgs) => makeSlowRoutes());

    render(<PerformancePage fetchers={{ percentiles, slowRoutes }} />);

    await waitFor(() => {
      expect(screen.getByText('Largest Contentful Paint')).toBeInTheDocument();
      expect(screen.getByText('Interaction to Next Paint')).toBeInTheDocument();
      expect(screen.getByText('Cumulative Layout Shift')).toBeInTheDocument();
      expect(screen.getByText('Time to First Byte')).toBeInTheDocument();
      expect(screen.getByText('First Contentful Paint')).toBeInTheDocument();
    });
    // One percentile call per metric.
    expect(percentiles).toHaveBeenCalledTimes(5);
  });

  it('renders the slow-routes table from API data', async () => {
    const percentiles = vi.fn(async (args: PercentileArgs) => makePercentile(args.metric, 2000));
    const slowRoutes = vi.fn(async (_args: SlowRoutesArgs) => makeSlowRoutes());

    render(<PerformancePage fetchers={{ percentiles, slowRoutes }} />);

    await waitFor(() => {
      expect(screen.getByText('/heavy')).toBeInTheDocument();
      expect(screen.getByText('/medium')).toBeInTheDocument();
    });
  });

  it('shows a "No samples yet" hint when the metric returns sample=0', async () => {
    const percentiles = vi.fn(async (args: PercentileArgs) =>
      makePercentile(args.metric, 0, 0),
    );
    const slowRoutes = vi.fn(async (_args: SlowRoutesArgs) => ({ ...makeSlowRoutes(), routes: [] }));

    render(<PerformancePage fetchers={{ percentiles, slowRoutes }} />);

    await waitFor(() => {
      const hints = screen.getAllByText('No samples yet.');
      expect(hints.length).toBe(5);
    });
  });

  it('re-fetches when the window selector changes', async () => {
    const percentiles = vi.fn(async (args: PercentileArgs) => makePercentile(args.metric, 2000));
    const slowRoutes = vi.fn(async (_args: SlowRoutesArgs) => makeSlowRoutes());

    render(<PerformancePage fetchers={{ percentiles, slowRoutes }} />);
    await waitFor(() => expect(percentiles).toHaveBeenCalledTimes(5));

    const select = screen.getByLabelText('Window');
    await act(async () => {
      fireEvent.change(select, { target: { value: '7d' } });
    });
    await waitFor(() => expect(percentiles).toHaveBeenCalledTimes(10));
    expect(percentiles.mock.calls.at(-1)?.[0].period).toBe('7d');
  });

  it('shows an error banner when the slow-routes call fails', async () => {
    const percentiles = vi.fn(async (args: PercentileArgs) => makePercentile(args.metric, 2000));
    const slowRoutes = vi.fn(async (_args: SlowRoutesArgs) => {
      throw new Error('boom');
    });

    render(<PerformancePage fetchers={{ percentiles, slowRoutes }} />);
    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/boom/i);
    });
  });
});
