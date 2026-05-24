'use client';

/**
 * PerformancePage — the operator-facing Core Web Vitals surface.
 *
 * Renders one card per Core Web Vitals metric (LCP, INP, CLS, TTFB,
 * FCP) showing p50/p75/p95 over a selectable window (1h/6h/24h/7d).
 * Below the cards, the page lists the top-N slowest routes for the
 * currently selected metric so an operator can drill into the
 * regressions a deploy introduced.
 *
 * The chart bands are deliberately rendered as inline CSS bars rather
 * than as a charting library. The information density is low enough
 * (three numbers per metric, ten rows in the slow-routes table) that
 * pulling in Recharts would add 70+ KiB to the admin bundle for no
 * gain. A future "drill into a metric" view CAN justify the library
 * — keep it scoped to that page when it lands.
 */
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type ReactElement,
} from 'react';
import { ApiError } from '@/lib/api-client';
import { fetchPercentiles, fetchSlowRoutes } from './api';
import type {
  PercentileResult,
  RUMMetric,
  RUMPeriod,
  SlowRoute,
} from './types';

const REFRESH_INTERVAL_MS = 30_000;

const METRICS: RUMMetric[] = ['LCP', 'INP', 'CLS', 'TTFB', 'FCP'];
const PERIODS: RUMPeriod[] = ['1h', '6h', '24h', '7d'];

const METRIC_LABEL: Record<RUMMetric, string> = {
  LCP: 'Largest Contentful Paint',
  INP: 'Interaction to Next Paint',
  CLS: 'Cumulative Layout Shift',
  TTFB: 'Time to First Byte',
  FCP: 'First Contentful Paint',
};

const METRIC_UNIT: Record<RUMMetric, 'ms' | 'score'> = {
  LCP: 'ms',
  INP: 'ms',
  CLS: 'score',
  TTFB: 'ms',
  FCP: 'ms',
};

/**
 * Web-vitals threshold table. Used to pick the band colour for the
 * p75 value (the operator-canonical "is this site healthy" number).
 * Kept in sync with https://web.dev/vitals; the thresholds drift
 * upstream every couple of years.
 */
const THRESHOLDS: Record<RUMMetric, { good: number; poor: number }> = {
  LCP: { good: 2500, poor: 4000 },
  INP: { good: 200, poor: 500 },
  CLS: { good: 0.1, poor: 0.25 },
  TTFB: { good: 800, poor: 1800 },
  FCP: { good: 1800, poor: 3000 },
};

const styles: Record<string, CSSProperties> = {
  toolbar: { display: 'flex', gap: 8, alignItems: 'center', marginBottom: 16, flexWrap: 'wrap' },
  grid: { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 12 },
  card: {
    padding: 16,
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 8,
    background: 'var(--color-surface, #fff)',
    display: 'flex',
    flexDirection: 'column',
    gap: 8,
  },
  cardLabel: { fontSize: 12, color: 'var(--color-text-muted, #6b7280)', textTransform: 'uppercase', letterSpacing: '0.04em' },
  cardMetric: { fontSize: 14, fontWeight: 600 },
  bandsRow: { display: 'flex', gap: 8, marginTop: 4 },
  band: { flex: 1, padding: 6, borderRadius: 4, fontSize: 12, textAlign: 'center' },
  sectionHeading: { fontSize: 14, fontWeight: 600, margin: '24px 0 8px', color: 'var(--color-text-muted, #6b7280)', textTransform: 'uppercase', letterSpacing: '0.04em' },
  errorBanner: { padding: '10px 12px', marginBottom: 12, border: '1px solid #fecaca', background: '#fef2f2', color: '#991b1b', borderRadius: 6, fontSize: 13 },
  emptyHint: { padding: 12, color: 'var(--color-text-muted, #6b7280)', fontSize: 13 },
  table: { width: '100%', borderCollapse: 'collapse', fontSize: 13 },
  th: { textAlign: 'left', padding: '8px 12px', borderBottom: '1px solid var(--color-border, #e4e6ea)', color: 'var(--color-text-muted, #6b7280)', fontWeight: 600, fontSize: 12, textTransform: 'uppercase', letterSpacing: '0.04em' },
  td: { padding: '8px 12px', borderBottom: '1px solid var(--color-border, #e4e6ea)' },
};

/**
 * formatValue converts a raw metric value to the operator-facing
 * display string. CLS is unitless and rendered to 3 decimals; the
 * other metrics are millisecond integers rounded to the nearest 10
 * to avoid false-precision noise on a small sample.
 */
function formatValue(metric: RUMMetric, v: number): string {
  if (METRIC_UNIT[metric] === 'score') {
    return v.toFixed(3);
  }
  if (v < 1000) {
    return `${Math.round(v / 10) * 10} ms`;
  }
  return `${(v / 1000).toFixed(2)} s`;
}

/**
 * pickBand returns the band classifier for a value against its
 * metric's thresholds. The admin surface uses the same buckets as
 * web-vitals.js so the rating an operator sees here matches what a
 * developer would see in DevTools.
 */
function pickBand(metric: RUMMetric, v: number): 'good' | 'needs-improvement' | 'poor' {
  const t = THRESHOLDS[metric];
  if (v <= t.good) return 'good';
  if (v <= t.poor) return 'needs-improvement';
  return 'poor';
}

const BAND_COLORS: Record<'good' | 'needs-improvement' | 'poor', { bg: string; fg: string }> = {
  good: { bg: '#dcfce7', fg: '#15803d' },
  'needs-improvement': { bg: '#fef3c7', fg: '#a16207' },
  poor: { bg: '#fee2e2', fg: '#b91c1c' },
};

/**
 * MetricCard renders one Core Web Vitals card with p50/p75/p95
 * bands. Renders a "no data yet" placeholder when sample is 0 so
 * operators understand a brand-new deployment will show empty
 * cards until visitors arrive.
 */
function MetricCard({ metric, result }: { metric: RUMMetric; result: PercentileResult | null }): ReactElement {
  return (
    <div style={styles.card}>
      <div style={styles.cardLabel}>{metric}</div>
      <div style={styles.cardMetric}>{METRIC_LABEL[metric]}</div>
      {result && result.sample > 0 ? (
        <>
          <div style={styles.bandsRow}>
            {(['p50', 'p75', 'p95'] as const).map((k) => {
              const v = result[k];
              const band = pickBand(metric, v);
              const colors = BAND_COLORS[band];
              return (
                <div key={k} style={{ ...styles.band, background: colors.bg, color: colors.fg }}>
                  <div style={{ fontSize: 11, opacity: 0.7 }}>{k.toUpperCase()}</div>
                  <div style={{ fontWeight: 600 }}>{formatValue(metric, v)}</div>
                </div>
              );
            })}
          </div>
          <div style={{ fontSize: 11, color: 'var(--color-text-muted, #6b7280)' }}>
            n = {result.sample.toLocaleString()}
          </div>
        </>
      ) : (
        <div style={styles.emptyHint}>No samples yet.</div>
      )}
    </div>
  );
}

export interface PerformancePageProps {
  /**
   * Test seam — override the fetchers so the component can be
   * exercised without any HTTP wiring. Production calls pass
   * nothing and the production fetchers run.
   */
  fetchers?: {
    percentiles: typeof fetchPercentiles;
    slowRoutes: typeof fetchSlowRoutes;
  };
}

export function PerformancePage({ fetchers }: PerformancePageProps): ReactElement {
  const fetchPct = fetchers?.percentiles ?? fetchPercentiles;
  const fetchSlow = fetchers?.slowRoutes ?? fetchSlowRoutes;

  const [period, setPeriod] = useState<RUMPeriod>('24h');
  const [slowMetric, setSlowMetric] = useState<RUMMetric>('LCP');
  const [percentiles, setPercentiles] = useState<Record<RUMMetric, PercentileResult | null>>(
    () => Object.fromEntries(METRICS.map((m) => [m, null])) as Record<RUMMetric, PercentileResult | null>,
  );
  const [slowRoutes, setSlowRoutes] = useState<SlowRoute[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState<boolean>(false);
  const abortRef = useRef<AbortController | null>(null);

  const refresh = useCallback(async () => {
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    setLoading(true);
    setError(null);

    try {
      const pctResults = await Promise.all(
        METRICS.map((m) =>
          fetchPct({ metric: m, period }, ctrl.signal).catch((err) => {
            // Soft-fail per-metric: a single metric returning 500
            // shouldn't blank the whole page.
            if (err instanceof ApiError && err.status === 401) throw err;
            return null;
          }),
        ),
      );
      const next: Record<RUMMetric, PercentileResult | null> = Object.fromEntries(
        METRICS.map((m, idx) => [m, pctResults[idx] as PercentileResult | null]),
      ) as Record<RUMMetric, PercentileResult | null>;
      setPercentiles(next);

      const slow = await fetchSlow({ metric: slowMetric, period, limit: 10 }, ctrl.signal);
      setSlowRoutes(slow.routes);
    } catch (err) {
      if (ctrl.signal.aborted) return;
      const message =
        err instanceof ApiError
          ? `API error ${err.status}`
          : err instanceof Error
            ? err.message
            : 'unknown error';
      setError(message);
    } finally {
      if (!ctrl.signal.aborted) {
        setLoading(false);
      }
    }
  }, [fetchPct, fetchSlow, period, slowMetric]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Lightweight auto-refresh. The 30s tick matches the server-side
  // cache TTL, so this is the lowest cost that produces fresh
  // numbers on every render.
  useEffect(() => {
    const id = setInterval(() => {
      void refresh();
    }, REFRESH_INTERVAL_MS);
    return () => clearInterval(id);
  }, [refresh]);

  // Tear down any in-flight request on unmount so we don't leak a
  // setState into an unmounted tree.
  useEffect(() => () => abortRef.current?.abort(), []);

  return (
    <section>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 16, marginBottom: 8 }}>
        <h1 style={{ margin: 0 }}>Performance</h1>
      </div>
      <p className="muted" style={{ marginTop: 0 }}>
        Core Web Vitals collected from real visitors via the in-house RUM
        beacon. Aggregates are computed server-side and cached for 30s.
      </p>

      <div style={styles.toolbar}>
        <label>
          Window:{' '}
          <select
            aria-label="Window"
            value={period}
            onChange={(e) => setPeriod(e.target.value as RUMPeriod)}
          >
            {PERIODS.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </select>
        </label>
        <button type="button" onClick={() => void refresh()} disabled={loading}>
          {loading ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>

      {error ? (
        <div role="alert" style={styles.errorBanner}>
          Couldn&apos;t load RUM data: {error}
        </div>
      ) : null}

      <div style={styles.grid}>
        {METRICS.map((m) => (
          <MetricCard key={m} metric={m} result={percentiles[m]} />
        ))}
      </div>

      <h2 style={styles.sectionHeading}>Slowest routes</h2>
      <div style={styles.toolbar}>
        <label>
          Metric:{' '}
          <select
            aria-label="Slowest metric"
            value={slowMetric}
            onChange={(e) => setSlowMetric(e.target.value as RUMMetric)}
          >
            {METRICS.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        </label>
      </div>
      {slowRoutes.length === 0 ? (
        <div style={styles.emptyHint}>No routes meet the minimum-sample threshold yet.</div>
      ) : (
        <table style={styles.table}>
          <thead>
            <tr>
              <th style={styles.th}>Path</th>
              <th style={styles.th}>p75</th>
              <th style={styles.th}>Samples</th>
            </tr>
          </thead>
          <tbody>
            {slowRoutes.map((row) => (
              <tr key={row.path}>
                <td style={styles.td}>
                  <code>{row.path}</code>
                </td>
                <td style={styles.td}>{formatValue(row.metric, row.p75)}</td>
                <td style={styles.td}>{row.sample.toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
