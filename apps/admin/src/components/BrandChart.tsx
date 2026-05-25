/**
 * BrandChart — small SVG bar-chart primitive in the Living-Systems
 * data-viz language.
 *
 * The Living-Systems brand uses two charting moves (see
 * `docs/design/ui_kits/admin/pulse.html`):
 *
 *   1. Lavender bars on a forest surface for histogram-style data
 *      (RUM percentiles, throughput histograms). The "peak" bar
 *      flips to `emerald-bright` so the brand emerald stays
 *      reserved for the headline value.
 *   2. Mono-tone axis labels and a dashed gridline so the chrome
 *      reads as clinical, not decorative.
 *
 * This component renders a single bar group. It is deliberately
 * tiny — no scales, tooltips, or animation — because the admin
 * surfaces that use it (Performance) ship three numbers per metric,
 * not a streaming time series. When a heavier chart lands, swap in
 * a charting library inside this shell; the BrandChart prop contract
 * stays a frozen surface for the rest of the admin.
 *
 * Surface: defaults to "cream" (paper-2 wells) but switches to
 * "forest" when nested under `data-surface="forest"` — same
 * inheritance contract as `<Headline>`. Forest renders the dark
 * panel from the pulse handoff; cream renders a soft paper well.
 */
import type { ReactElement } from 'react';

import { cn } from '@/lib/utils';

export interface BrandBar {
  /** Short label shown under the bar (e.g. "p50"). */
  label: string;
  /** Numeric height; the chart normalises against the max. */
  value: number;
  /** Already-formatted value text shown above the bar. */
  display: string;
  /**
   * Optional emphasis. When set the bar tints emerald-bright; use
   * for the "peak" / "headline" reading. Without it the bar tints
   * lavender, the brand's data-viz secondary.
   */
  emphasis?: boolean;
}

export interface BrandChartProps {
  /** Bars rendered left-to-right. */
  bars: readonly BrandBar[];
  /**
   * Fixed visual height of the bar canvas in pixels. The bars scale
   * to fill the canvas — anything taller than the highest bar is
   * blank space at the top of the chart.
   */
  height?: number;
  /**
   * "cream" (default) — paper-2 well, lavender bars.
   * "forest" — forest-2 dark panel, lavender bars on dark.
   * Most admin surfaces use cream; the forest variant is reserved
   * for hero/dashboard "pulse" sections.
   */
  surface?: 'cream' | 'forest';
  /** Optional test-id passthrough. */
  testId?: string;
  /** Extra classes for the outer wrapper. */
  className?: string;
}

/**
 * BrandChart renders a row of vertical bars sharing a single
 * normalised scale. Used by the Performance page to visualise
 * p50/p75/p95 per Core Web Vitals metric.
 */
export function BrandChart({
  bars,
  height = 140,
  surface = 'cream',
  testId,
  className,
}: BrandChartProps): ReactElement {
  // Bars normalise against the running max so a single tall bar
  // doesn't squash the rest into invisibility. A floor of 1 keeps
  // the divisor positive when every value is 0 (e.g. "no samples").
  const max = Math.max(1, ...bars.map((b) => b.value));

  const wrapClasses =
    surface === 'forest'
      ? 'rounded-md border border-forest-border bg-forest-2 p-3'
      : 'rounded-md border border-border-subtle bg-paper-3 p-3';

  const axisColor =
    surface === 'forest' ? 'text-fg-on-forest-muted' : 'text-fg-subtle';

  return (
    <div
      data-testid={testId}
      data-surface={surface === 'forest' ? 'forest' : undefined}
      className={cn('flex flex-col gap-2', wrapClasses, className)}
      role="img"
      aria-label={`Bar chart: ${bars
        .map((b) => `${b.label} ${b.display}`)
        .join(', ')}`}
    >
      <div
        className="flex items-end justify-between gap-2"
        style={{ height }}
      >
        {bars.map((b) => {
          const h = max === 0 ? 0 : Math.max(4, (b.value / max) * height);
          const barColor = b.emphasis ? 'bg-emerald' : 'bg-lavender';
          return (
            <div
              key={b.label}
              className="flex flex-1 flex-col items-center justify-end gap-1"
            >
              <span
                className={cn(
                  'font-mono text-[10px] font-semibold tabular-nums',
                  surface === 'forest' ? 'text-fg-on-forest' : 'text-ink',
                )}
              >
                {b.display}
              </span>
              <div
                aria-hidden="true"
                className={cn(
                  'w-full max-w-[36px] rounded-t-sm transition-all duration-[260ms] ease-brand',
                  barColor,
                  b.emphasis ? 'hover:bg-emerald-deep' : 'hover:bg-lavender-deep',
                )}
                style={{ height: h }}
              />
            </div>
          );
        })}
      </div>
      <div className="flex justify-between gap-2 px-1">
        {bars.map((b) => (
          <span
            key={b.label}
            className={cn(
              'flex-1 text-center font-mono text-[10px] uppercase tracking-wide',
              axisColor,
            )}
          >
            {b.label}
          </span>
        ))}
      </div>
    </div>
  );
}
