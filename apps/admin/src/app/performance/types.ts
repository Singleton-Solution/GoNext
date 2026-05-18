/**
 * On-wire shapes for the RUM (Real User Monitoring) admin surface.
 *
 * Mirrors the Go types in apps/api/internal/admin/rum/. Any change to
 * the server-side struct shape MUST be reflected here or the page will
 * render a confusing partial state. We keep the names snake_case where
 * they appear in JSON; the React tree converts to camelCase at the
 * binding seam.
 */

/** The supported Core Web Vitals metric names. */
export type RUMMetric = 'LCP' | 'INP' | 'CLS' | 'TTFB' | 'FCP';

/** The supported lookback periods on the read endpoints. */
export type RUMPeriod = '1h' | '6h' | '24h' | '7d';

export interface PercentileResult {
  metric: RUMMetric;
  /** Empty string when aggregated across all routes. */
  path?: string;
  period: RUMPeriod;
  from: string;
  to: string;
  p50: number;
  p75: number;
  p95: number;
  /** Underlying event count over the window. */
  sample: number;
}

export interface SlowRoute {
  path: string;
  metric: RUMMetric;
  p75: number;
  sample: number;
}

export interface SlowRoutesResponse {
  metric: RUMMetric;
  period: RUMPeriod;
  from: string;
  to: string;
  routes: SlowRoute[];
}
