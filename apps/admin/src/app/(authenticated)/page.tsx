/**
 * Dashboard / Pulse — the landing page of the admin app.
 *
 * "Site pulse." — the brand's vital-signs view. Mirrors the layout
 * patterns from docs/design/ui_kits/admin/index.html and
 * docs/design/ui_kits/admin/pulse.html:
 *
 *   1. Display headline with italic-serif accent ("Site *pulse*.").
 *   2. Live indicator chip (emerald-soft, animated dot).
 *   3. Four-up stat tiles on paper-2 surfaces — each carries a
 *      label icon, an Archivo number, a colour-coded delta
 *      ("+12.4%") badge, and a sparkline (emerald or lavender).
 *   4. A 24-hour reader-flow line chart in a paper-2 card with the
 *      brand's emerald-area + lavender-overlay aesthetic.
 *   5. A "Where they linger" lavender histogram (sessions × time)
 *      with peak bars in emerald-bright — the moodboard's
 *      signature data viz.
 *
 * The chart data is static seed values for now. Once the analytics
 * endpoint (issue #132 / RUM beacon) lands the seed module gets
 * swapped for the live aggregate response without touching the
 * presentation here.
 *
 * `dynamic = 'force-dynamic'` is preserved from the scaffold — the
 * dashboard reads live data, so caching the rendered HTML between
 * requests would defeat the point.
 */
import type { ReactElement } from 'react';
import { Activity, Clock, Eye, FileCheck2, PenLine } from 'lucide-react';
import { Headline } from '@/components/ui/headline';
import {
  BarChartSurface,
  LineChartSurface,
  Sparkline,
} from '@/components/ui/brand-chart';
import { QuickDraftCard } from './_components/QuickDraftCard';

export const dynamic = 'force-dynamic';

/** Seed values for the line chart. 24 hourly buckets, monotonically
 * trending up — characteristic of a readership graph that picks up
 * through the day. Real data lands once the RUM beacon ships. */
const HOURLY_VIEWS: ReadonlyArray<{
  name: string;
  value: number;
  conversions: number;
}> = [
  { name: '00:00', value: 120, conversions: 8 },
  { name: '02:00', value: 95, conversions: 6 },
  { name: '04:00', value: 88, conversions: 4 },
  { name: '06:00', value: 132, conversions: 9 },
  { name: '08:00', value: 248, conversions: 18 },
  { name: '10:00', value: 410, conversions: 32 },
  { name: '12:00', value: 524, conversions: 41 },
  { name: '14:00', value: 612, conversions: 48 },
  { name: '16:00', value: 718, conversions: 55 },
  { name: '18:00', value: 805, conversions: 62 },
  { name: '20:00', value: 742, conversions: 58 },
  { name: '22:00', value: 581, conversions: 44 },
];

/** Session-duration histogram seed — log-shaped distribution with a
 * peak around the 2-3 minute mark, matching the moodboard. */
const SESSION_BUCKETS: ReadonlyArray<{
  name: string;
  value: number;
  accent?: 'lavender' | 'emerald';
}> = [
  { name: '10s', value: 220 },
  { name: '30s', value: 340 },
  { name: '1m', value: 520 },
  { name: '2m', value: 780, accent: 'emerald' },
  { name: '3m', value: 920, accent: 'emerald' },
  { name: '5m', value: 720 },
  { name: '10m', value: 480 },
  { name: '20m', value: 280 },
  { name: '30m+', value: 120 },
];

/** Tile descriptor. Pure data so the file stays scannable; the
 * actual render lives in {@link StatTile} below. */
interface StatTile {
  label: string;
  Icon: typeof Eye;
  value: string;
  unit?: string;
  delta?: { direction: 'up' | 'down'; text: string; vs: string };
  spark?: { values: number[]; accent: 'emerald' | 'lavender' };
}

const STAT_TILES: readonly StatTile[] = [
  {
    label: 'Published, 30d',
    Icon: FileCheck2,
    value: '24',
    delta: { direction: 'up', text: '+8', vs: 'vs. previous 30d' },
  },
  {
    label: 'Views, 30d',
    Icon: Eye,
    value: '48,247',
    delta: { direction: 'up', text: '12.4%', vs: 'vs. previous 30d' },
    spark: {
      values: [40, 55, 48, 72, 65, 80, 70, 100],
      accent: 'emerald',
    },
  },
  {
    label: 'Avg. read time',
    Icon: Clock,
    value: '3:14',
    unit: '/reader',
    delta: { direction: 'down', text: '−8s', vs: 'vs. previous 30d' },
  },
  {
    label: 'Drafts in progress',
    Icon: PenLine,
    value: '7',
    spark: {
      values: [30, 60, 45, 70, 50, 80, 40, 55],
      accent: 'lavender',
    },
  },
];

function StatTileCard({ tile }: { tile: StatTile }): ReactElement {
  const { Icon, label, value, unit, delta, spark } = tile;
  return (
    <article
      className="rounded-lg border border-border bg-paper-2 p-5 shadow-xs transition-colors hover:border-border-strong"
      aria-label={label}
    >
      <div className="flex items-center gap-[6px] text-xs font-medium text-fg-muted">
        <Icon
          aria-hidden="true"
          width={13}
          height={13}
          className="text-fg-subtle"
        />
        <span>{label}</span>
      </div>
      <div className="mt-3 font-display text-4xl font-bold leading-none tracking-tight tabular-nums text-ink">
        {value}
        {unit ? (
          <span className="ml-[2px] font-serif text-[0.5em] italic font-normal text-fg-subtle">
            {' '}
            {unit}
          </span>
        ) : null}
      </div>
      {delta ? (
        <div className="mt-[10px] flex items-center gap-[6px] text-xs">
          <span
            className={
              delta.direction === 'up'
                ? 'inline-flex items-center gap-[2px] rounded-xs bg-emerald-soft px-[6px] py-[1px] font-semibold text-emerald-deep'
                : 'inline-flex items-center gap-[2px] rounded-xs bg-danger-soft px-[6px] py-[1px] font-semibold text-danger'
            }
          >
            {delta.text}
          </span>
          <span className="text-fg-subtle">{delta.vs}</span>
        </div>
      ) : (
        <div className="mt-[10px] text-xs text-fg-subtle">
          3 scheduled this week
        </div>
      )}
      {spark ? (
        <div className="mt-3">
          <Sparkline
            values={spark.values}
            peakCount={spark.accent === 'emerald' ? 1 : 3}
            accent={spark.accent}
            ariaLabel={`${label} trend`}
          />
        </div>
      ) : null}
    </article>
  );
}

export default function DashboardPage(): ReactElement {
  return (
    <section className="flex flex-col gap-7" data-testid="dashboard-page">
      {/* ─── Page head ─── */}
      <div className="flex flex-wrap items-end justify-between gap-6 border-b border-border pb-6">
        <div>
          <Headline as="h1" size="page" className="text-[clamp(40px,5vw,52px)]">
            Site <em>pulse</em>.
          </Headline>
          <p className="mt-[10px] max-w-[480px] text-sm text-fg-muted">
            A vital-signs view of the workspace. Drafts, readers, and revenue —
            updated as the site lives and grows.
          </p>
        </div>
        <span
          className="inline-flex items-center gap-2 rounded-pill border border-emerald/35 bg-emerald-soft/60 px-3 py-[6px] text-xs font-medium text-emerald-deep"
          aria-label="Live status"
        >
          <span
            aria-hidden="true"
            className="h-[6px] w-[6px] animate-pulse rounded-pill bg-emerald"
          />
          Live · updated just now
        </span>
      </div>

      {/* ─── Stat tiles ─── */}
      <div className="grid grid-cols-1 gap-[14px] sm:grid-cols-2 xl:grid-cols-4">
        {STAT_TILES.map((tile) => (
          <StatTileCard key={tile.label} tile={tile} />
        ))}
      </div>

      {/* ─── Reader-flow forest pulse card ─── */}
      <div
        data-surface="forest"
        className="relative overflow-hidden rounded-lg bg-forest p-6 text-fg-on-forest"
      >
        {/* Organic radial-glow on a dark surface — the brand's
            signature backdrop pattern. */}
        <div
          aria-hidden="true"
          className="pointer-events-none absolute -top-[40%] -right-[10%] h-[480px] w-[480px] rounded-pill"
          style={{
            background:
              'radial-gradient(circle, rgba(16, 185, 129, 0.18) 0%, transparent 60%)',
          }}
        />
        <div className="relative grid gap-6 lg:grid-cols-[320px_1fr_240px] lg:items-end">
          <div>
            <div className="text-xs font-medium uppercase tracking-[0.12em] text-emerald-bright">
              Live · Last 5 minutes
            </div>
            <Headline
              as="h2"
              size="sub"
              surface="forest"
              className="mt-2 text-[26px] leading-snug"
            >
              142 readers, <em>now</em>.
            </Headline>
            <p className="mt-2 max-w-[300px] text-sm text-fg-on-forest-muted">
              Reader flow over the last 24 hours, with conversions overlaid in
              lavender.
            </p>
            <div className="mt-4 flex gap-[14px] text-xs text-fg-on-forest-muted">
              <span className="inline-flex items-center gap-[6px]">
                <span className="h-2 w-2 rounded-sm bg-emerald-bright" />
                Views
              </span>
              <span className="inline-flex items-center gap-[6px]">
                <span className="h-2 w-2 rounded-sm bg-lavender" />
                Conversions
              </span>
            </div>
          </div>
          <div className="rounded-md border border-forest-border bg-white/[0.03] p-3">
            <LineChartSurface
              data={[...HOURLY_VIEWS]}
              height={200}
              showConversions
              ariaLabel="Views and conversions over the last 24 hours"
            />
          </div>
          <dl className="flex flex-col gap-2 text-xs">
            <div className="flex justify-between">
              <dt className="text-fg-on-forest-muted">Top post</dt>
              <dd className="font-medium tabular-nums text-fg-on-forest">
                Single-origin beans
              </dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-fg-on-forest-muted">Avg. session</dt>
              <dd className="font-mono tabular-nums text-fg-on-forest">2m 47s</dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-fg-on-forest-muted">p50 TTFB</dt>
              <dd className="font-mono tabular-nums text-fg-on-forest">
                38<span className="font-serif italic text-fg-on-forest-muted">ms</span>
              </dd>
            </div>
          </dl>
        </div>
      </div>

      {/* ─── Histogram + sidebar ─── */}
      <div className="grid grid-cols-1 gap-[14px] lg:grid-cols-[1.4fr_1fr]">
        <div className="rounded-lg border border-border bg-paper-2 p-6 shadow-xs">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <div className="text-xs font-medium uppercase tracking-[0.08em] text-fg-subtle">
                Reader sessions · 24h
              </div>
              <Headline as="h3" size="sub" className="mt-1 text-[22px]">
                Where they <em>linger</em>.
              </Headline>
            </div>
            <div className="flex items-center gap-2 text-[10px] font-mono uppercase tracking-wide">
              <span className="rounded-xs border border-border bg-paper-3 px-[6px] py-[2px] text-fg-subtle">
                count
              </span>
              <span className="rounded-xs border border-border bg-paper-3 px-[6px] py-[2px] text-fg-subtle">
                7,842 sessions
              </span>
            </div>
          </div>
          <div className="mt-5">
            <BarChartSurface
              data={[...SESSION_BUCKETS]}
              height={220}
              ariaLabel="Reader session lengths over the last 24 hours"
            />
          </div>
          <p className="mt-2 text-xs text-fg-muted">
            Lavender bars are the session-length distribution; peaks in
            <span className="text-emerald-deep"> emerald</span> mark where most
            readers spend their time.
          </p>
        </div>

        <div className="flex flex-col gap-4">
        <QuickDraftCard />
        <div className="flex flex-col gap-3 rounded-lg border border-border bg-paper-2 p-6 shadow-xs">
          <div className="text-xs font-medium uppercase tracking-[0.08em] text-fg-subtle">
            Recent activity
          </div>
          <Headline as="h3" size="sub" className="text-[22px]">
            What just <em>happened</em>.
          </Headline>
          <ul className="mt-2 flex flex-col gap-3" aria-label="Recent activity">
            <li className="flex items-start gap-3">
              <span className="mt-[2px] flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-sm bg-emerald-soft text-emerald-deep">
                <Activity aria-hidden="true" width={13} height={13} />
              </span>
              <span className="text-sm leading-snug text-ink">
                <strong className="font-semibold">Deploy succeeded</strong> ·
                v1.2.4 → <em className="font-serif italic text-emerald-deep">production</em>
                <span className="block font-mono text-[10px] text-fg-subtle">
                  14 routes invalidated · TTFB unchanged
                </span>
              </span>
            </li>
            <li className="flex items-start gap-3">
              <span className="mt-[2px] flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-sm bg-lavender-soft text-lavender-deep">
                <Eye aria-hidden="true" width={13} height={13} />
              </span>
              <span className="text-sm leading-snug text-ink">
                Reader from <strong>Tokyo</strong> opened
                <em className="font-serif italic text-emerald-deep"> Drip vs. pour-over</em>
                <span className="block font-mono text-[10px] text-fg-subtle">
                  Mobile Safari · 3rd session
                </span>
              </span>
            </li>
            <li className="flex items-start gap-3">
              <span className="mt-[2px] flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-sm bg-paper-3 text-fg-muted">
                <PenLine aria-hidden="true" width={13} height={13} />
              </span>
              <span className="text-sm leading-snug text-ink">
                New draft <strong>Holiday hours</strong> picked up by Mara
                <span className="block font-mono text-[10px] text-fg-subtle">
                  Auto-save · 12 minutes ago
                </span>
              </span>
            </li>
          </ul>
        </div>
        </div>
      </div>
    </section>
  );
}
