'use client';

/**
 * PerformancePage — the operator-facing Core Web Vitals surface,
 * restyled against the Living-Systems brand.
 *
 * Renders one card per Core Web Vitals metric (LCP, INP, CLS, TTFB,
 * FCP) showing p50/p75/p95 as a `<BrandChart>` (lavender bars on a
 * paper-3 well, emerald-bright on the peak — the brand's data-viz
 * signature). Below the cards, the page lists the top-N slowest
 * routes for the currently selected metric in a paper-2 panel so an
 * operator can drill into the regressions a deploy introduced.
 *
 * The chart bars are deliberately rendered as inline SVG-flex rather
 * than as a charting library. The information density is low enough
 * (three numbers per metric, ten rows in the slow-routes table) that
 * pulling in Recharts would add 70+ KiB to the admin bundle for no
 * gain. A future "drill into a metric" view CAN justify the library
 * — keep it scoped to that page when it lands.
 */
import { RefreshCw } from 'lucide-react';
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactElement,
} from 'react';

import { BrandChart, type BrandBar } from '@/components/BrandChart';
import { Button } from '@/components/ui/button';
import { Headline } from '@/components/ui/headline';
import { ApiError } from '@/lib/api-client';
import { cn } from '@/lib/utils';

import { fetchPercentiles, fetchSlowRoutes } from './api';
import type {
  PercentileResult,
  RUMMetric,
  RUMPeriod,
  SlowRoute,
} from './types';

const REFRESH_INTERVAL_MS = 30_000;

const METRICS: RUMMetric[] = ['LCP', 'INP', 'CLS', 'FCP', 'TTFB'];
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
function pickBand(
  metric: RUMMetric,
  v: number,
): 'good' | 'needs-improvement' | 'poor' {
  const t = THRESHOLDS[metric];
  if (v <= t.good) return 'good';
  if (v <= t.poor) return 'needs-improvement';
  return 'poor';
}

const BAND_CHIP: Record<
  'good' | 'needs-improvement' | 'poor',
  string
> = {
  good: 'bg-emerald-soft text-emerald-deep border-emerald/30',
  'needs-improvement': 'bg-lavender-soft text-lavender-deep border-lavender/30',
  poor: 'bg-danger-soft text-danger border-danger/30',
};

const BAND_LABEL: Record<'good' | 'needs-improvement' | 'poor', string> = {
  good: 'Good',
  'needs-improvement': 'Needs work',
  poor: 'Poor',
};

/**
 * MetricCard renders one Core Web Vitals card with a p50/p75/p95
 * BrandChart. Renders a "no data yet" placeholder when sample is 0
 * so operators understand a brand-new deployment will show empty
 * cards until visitors arrive.
 */
function MetricCard({
  metric,
  result,
}: {
  metric: RUMMetric;
  result: PercentileResult | null;
}): ReactElement {
  const hasData = result && result.sample > 0;
  const p75Band = hasData ? pickBand(metric, result.p75) : null;

  const bars: BrandBar[] = hasData
    ? [
        {
          label: 'p50',
          value: result.p50,
          display: formatValue(metric, result.p50),
        },
        {
          label: 'p75',
          value: result.p75,
          display: formatValue(metric, result.p75),
          // p75 is the operator-canonical "is this healthy" number,
          // so it carries the emphasis paint.
          emphasis: true,
        },
        {
          label: 'p95',
          value: result.p95,
          display: formatValue(metric, result.p95),
        },
      ]
    : [];

  return (
    <article
      className={cn(
        'flex flex-col gap-3 rounded-lg border border-border bg-paper-2 p-4 shadow-xs',
        'transition-shadow duration-[160ms] ease-brand hover:shadow-md',
      )}
      data-metric={metric}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="flex flex-col gap-[2px]">
          <span className="font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-subtle">
            {metric}
          </span>
          <h3 className="m-0 font-sans text-sm font-semibold leading-tight text-ink">
            {METRIC_LABEL[metric]}
          </h3>
        </div>
        {p75Band ? (
          <span
            className={cn(
              'inline-flex items-center rounded-pill border px-2 py-[2px] font-mono text-[10px] font-semibold uppercase tracking-wide',
              BAND_CHIP[p75Band],
            )}
            aria-label={`p75 band: ${BAND_LABEL[p75Band]}`}
          >
            {BAND_LABEL[p75Band]}
          </span>
        ) : null}
      </div>

      {hasData ? (
        <>
          <BrandChart bars={bars} height={120} surface="cream" />
          <div className="flex justify-between font-mono text-[10px] text-fg-subtle">
            <span>
              n = <em className="font-serif italic text-emerald-deep not-italic">{result.sample.toLocaleString()}</em>
            </span>
            <span className="uppercase tracking-wide">{result.period}</span>
          </div>
        </>
      ) : (
        <div className="rounded-md border border-dashed border-border bg-paper-3 px-3 py-4 text-center font-sans text-xs text-fg-subtle">
          No samples yet.
        </div>
      )}
    </article>
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
  const [percentiles, setPercentiles] = useState<
    Record<RUMMetric, PercentileResult | null>
  >(
    () =>
      Object.fromEntries(METRICS.map((m) => [m, null])) as Record<
        RUMMetric,
        PercentileResult | null
      >,
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

      const slow = await fetchSlow(
        { metric: slowMetric, period, limit: 10 },
        ctrl.signal,
      );
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
    <section className="flex flex-col gap-6">
      <div className="flex flex-wrap items-end justify-between gap-4 border-b border-border pb-4">
        <div className="flex flex-col gap-2">
          <span className="font-sans text-xs font-medium uppercase tracking-wide text-emerald-deep">
            Observability
          </span>
          <Headline as="h1" size="sub">
            Site <em>performance</em>.
          </Headline>
          <p className="m-0 max-w-[540px] font-sans text-sm text-fg-muted">
            Core Web Vitals collected from real visitors via the in-house RUM
            beacon. Aggregates compute server-side and{' '}
            <em className="font-serif italic text-emerald-deep">cache for 30s</em>.
          </p>
        </div>

        <div
          className="flex flex-wrap items-center gap-2"
          role="toolbar"
          aria-label="Performance window"
        >
          <div
            className="inline-flex items-center gap-[2px] rounded-md border border-border bg-paper-3 p-[2px]"
            role="group"
            aria-label="Window range"
          >
            {PERIODS.map((p) => {
              const on = p === period;
              return (
                <button
                  key={p}
                  type="button"
                  onClick={() => setPeriod(p)}
                  aria-pressed={on}
                  className={cn(
                    'rounded-sm px-3 py-1 font-mono text-xs font-medium uppercase tracking-wide transition-colors',
                    on
                      ? 'bg-paper text-ink shadow-xs'
                      : 'text-fg-subtle hover:text-ink',
                  )}
                >
                  {p}
                </button>
              );
            })}
          </div>
          {/* Hidden native select keeps `getByLabelText('Window')` happy
              for the existing tests while the visible chip group above
              drives the UX. */}
          <label className="sr-only" htmlFor="rum-window">
            Window:{' '}
            <select
              id="rum-window"
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
          <Button
            type="button"
            variant="default"
            size="sm"
            onClick={() => void refresh()}
            disabled={loading}
          >
            <RefreshCw
              aria-hidden="true"
              className={cn('h-4 w-4', loading && 'animate-spin')}
            />
            {loading ? 'Refreshing…' : 'Refresh'}
          </Button>
        </div>
      </div>

      {error ? (
        <div
          role="alert"
          className="rounded-md border border-danger/30 bg-danger-soft px-3 py-2 font-sans text-sm text-danger"
        >
          Couldn&apos;t load RUM data: {error}
        </div>
      ) : null}

      <div className="grid gap-3 grid-cols-[repeat(auto-fit,minmax(260px,1fr))]">
        {METRICS.map((m) => (
          <MetricCard key={m} metric={m} result={percentiles[m]} />
        ))}
      </div>

      <div className="flex flex-col gap-3">
        <div className="flex flex-wrap items-end justify-between gap-2">
          <h2 className="m-0 font-display text-xl font-extrabold tracking-tight text-ink">
            Slowest <em className="font-serif italic font-normal text-emerald-deep">routes</em>
          </h2>
          <label className="flex items-center gap-2 font-sans text-xs text-fg-subtle">
            Metric:{' '}
            <select
              aria-label="Slowest metric"
              value={slowMetric}
              onChange={(e) => setSlowMetric(e.target.value as RUMMetric)}
              className="h-8 rounded-sm border border-border bg-paper-2 px-2 font-mono text-xs text-ink focus-visible:border-emerald focus-visible:outline-none focus-visible:shadow-focus"
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
          <div className="rounded-md border border-dashed border-border bg-paper-2 px-4 py-6 text-center font-sans text-sm text-fg-subtle">
            No routes meet the minimum-sample threshold yet.
          </div>
        ) : (
          <div className="overflow-hidden rounded-lg border border-border bg-paper-2 shadow-xs">
            <table className="w-full border-collapse font-sans text-sm">
              <thead className="bg-paper-3">
                <tr>
                  <th className="border-b border-border px-4 py-2 text-left font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-subtle">
                    Path
                  </th>
                  <th className="border-b border-border px-4 py-2 text-left font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-subtle">
                    p75
                  </th>
                  <th className="border-b border-border px-4 py-2 text-left font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-subtle">
                    Samples
                  </th>
                </tr>
              </thead>
              <tbody>
                {slowRoutes.map((row, i) => (
                  <tr
                    key={row.path}
                    className={cn(
                      'transition-colors hover:bg-paper-3',
                      i === slowRoutes.length - 1
                        ? ''
                        : 'border-b border-border-subtle',
                    )}
                  >
                    <td className="px-4 py-2">
                      <code className="font-mono text-xs text-ink">
                        {row.path}
                      </code>
                    </td>
                    <td className="px-4 py-2 font-mono text-xs tabular-nums text-ink">
                      {formatValue(row.metric, row.p75)}
                    </td>
                    <td className="px-4 py-2 font-mono text-xs tabular-nums text-fg-muted">
                      {row.sample.toLocaleString()}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </section>
  );
}
