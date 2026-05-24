/**
 * Performance API client.
 *
 * Thin wrappers around `apiRequest` that surface the RUM percentile +
 * slow-routes shapes. Kept separate from the page so tests can mock the
 * fetcher independently of the React tree.
 */
import { api } from '@/lib/api-client';
import type {
  PercentileResult,
  RUMMetric,
  RUMPeriod,
  SlowRoutesResponse,
} from './types';

const PERCENTILES_PATH = '/api/v1/admin/rum/percentiles';
const SLOW_ROUTES_PATH = '/api/v1/admin/rum/slow-routes';

/**
 * Fetch percentile aggregates for one (metric, path, period) tuple.
 *
 * The signal is forwarded so the page can abort an in-flight refresh
 * when the operator picks a new period or unmounts the page.
 */
export function fetchPercentiles(
  args: { metric: RUMMetric; path?: string; period: RUMPeriod },
  signal?: AbortSignal,
): Promise<PercentileResult> {
  const params = new URLSearchParams();
  params.set('metric', args.metric);
  params.set('period', args.period);
  if (args.path) {
    params.set('path', args.path);
  }
  return api.get<PercentileResult>(`${PERCENTILES_PATH}?${params.toString()}`, { signal });
}

/**
 * Fetch the top-N slowest routes for a metric over the given window.
 */
export function fetchSlowRoutes(
  args: { metric: RUMMetric; period: RUMPeriod; limit?: number },
  signal?: AbortSignal,
): Promise<SlowRoutesResponse> {
  const params = new URLSearchParams();
  params.set('metric', args.metric);
  params.set('period', args.period);
  if (typeof args.limit === 'number') {
    params.set('limit', String(args.limit));
  }
  return api.get<SlowRoutesResponse>(`${SLOW_ROUTES_PATH}?${params.toString()}`, { signal });
}
