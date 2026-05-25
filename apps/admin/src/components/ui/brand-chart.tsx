'use client';

/**
 * BrandChart — recharts wrapper that ports the brand "Living systems"
 * data-viz language onto a real charting library.
 *
 * Why a wrapper?
 *   The brand has a strong opinion on chart appearance — emerald and
 *   lavender bars on a paper surface; emerald-bright peaks; soft
 *   gridlines; Geist Mono axis ticks. Threading those choices into
 *   every chart call site would duplicate the brand contract.
 *   This module concentrates the choices in one place so the canvas
 *   stays consistent across the dashboard, pulse, and any
 *   per-resource report screens that ship later.
 *
 * Two primitives are exported:
 *
 *   <BarChartSurface data={[{name, value, accent?}]} />
 *     Vertical bar chart on a paper-2 card. Bars are lavender by
 *     default; rows flagged `accent: 'emerald'` swap to emerald-bright
 *     — matching the "peak bars" pattern from
 *     docs/design/ui_kits/admin/pulse.html.
 *
 *   <LineChartSurface data={[{name, value, conversions?}]} />
 *     Time-series line on paper. The primary line is emerald; an
 *     optional conversions overlay renders in lavender. A soft
 *     gradient fills under the primary line.
 *
 * Both primitives use a 240px default height and stretch to fill
 * their container. They render inside ResponsiveContainer so they
 * adapt to whatever card width they're dropped into.
 */
import * as React from 'react';
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';

import { cn } from '@/lib/utils';

// Token-mirrored colour constants. These mirror docs/design/colors_and_type.css
// — when the tokens move the constants here must move too. We intentionally
// hard-code them here rather than reading CSS variables because Recharts
// computes colours during a layout pass that happens before the cascade
// resolves on the SVG primitives.
const EMERALD = '#10B981';
const EMERALD_BRIGHT = '#34D399';
const EMERALD_DEEP = '#047857';
const LAVENDER = '#A78BFA';
const LAVENDER_DEEP = '#7C3AED';
const PAPER_3 = '#E6E1D2';
const BORDER = '#D9D2C0';
const FG_MUTED = '#4A5C52';
const FG_SUBTLE = '#6B7B72';
const INK = '#0E1A14';

export interface BarChartDatum {
  name: string;
  value: number;
  /** When 'emerald', the bar renders in emerald-bright — peak emphasis. */
  accent?: 'lavender' | 'emerald';
}

export interface BarChartSurfaceProps {
  data: BarChartDatum[];
  /** Pixel height for the chart canvas. Default 240. */
  height?: number;
  /** Optional accessible label for the canvas. */
  ariaLabel?: string;
  className?: string;
}

export function BarChartSurface({
  data,
  height = 240,
  ariaLabel,
  className,
}: BarChartSurfaceProps): React.ReactElement {
  return (
    <div
      className={cn('w-full', className)}
      role="img"
      aria-label={ariaLabel ?? 'Bar chart'}
      style={{ height }}
    >
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={data} margin={{ top: 4, right: 8, left: -16, bottom: 0 }}>
          <CartesianGrid
            stroke={BORDER}
            strokeDasharray="2 4"
            vertical={false}
          />
          <XAxis
            dataKey="name"
            stroke={FG_SUBTLE}
            tickLine={false}
            axisLine={{ stroke: BORDER }}
            tick={{
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              fill: FG_SUBTLE,
            }}
          />
          <YAxis
            stroke={FG_SUBTLE}
            tickLine={false}
            axisLine={false}
            width={32}
            tick={{
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              fill: FG_SUBTLE,
            }}
          />
          <Tooltip
            cursor={{ fill: PAPER_3, opacity: 0.5 }}
            contentStyle={{
              background: '#FFFFFF',
              border: `1px solid ${BORDER}`,
              borderRadius: 8,
              fontFamily: 'var(--font-sans)',
              fontSize: 12,
              color: INK,
              boxShadow:
                '0 6px 14px -4px rgba(14, 26, 20, 0.08), 0 2px 6px -2px rgba(14, 26, 20, 0.04)',
            }}
            labelStyle={{ color: FG_MUTED, fontWeight: 500 }}
            itemStyle={{ color: INK }}
          />
          <Bar dataKey="value" radius={[2, 2, 0, 0]}>
            {data.map((entry, index) => (
              <Cell
                key={`cell-${index}`}
                fill={entry.accent === 'emerald' ? EMERALD_BRIGHT : LAVENDER}
              />
            ))}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}

export interface LineChartDatum {
  name: string;
  value: number;
  conversions?: number;
}

export interface LineChartSurfaceProps {
  data: LineChartDatum[];
  height?: number;
  /** When true, draw a lavender conversions overlay using `data.conversions`. */
  showConversions?: boolean;
  ariaLabel?: string;
  className?: string;
}

export function LineChartSurface({
  data,
  height = 240,
  showConversions = false,
  ariaLabel,
  className,
}: LineChartSurfaceProps): React.ReactElement {
  const gradientId = React.useId();
  return (
    <div
      className={cn('w-full', className)}
      role="img"
      aria-label={ariaLabel ?? 'Line chart'}
      style={{ height }}
    >
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 8, right: 8, left: -16, bottom: 0 }}>
          <defs>
            <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={EMERALD_BRIGHT} stopOpacity={0.35} />
              <stop offset="100%" stopColor={EMERALD_BRIGHT} stopOpacity={0} />
            </linearGradient>
          </defs>
          <CartesianGrid
            stroke={BORDER}
            strokeDasharray="2 4"
            vertical={false}
          />
          <XAxis
            dataKey="name"
            stroke={FG_SUBTLE}
            tickLine={false}
            axisLine={{ stroke: BORDER }}
            tick={{
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              fill: FG_SUBTLE,
            }}
          />
          <YAxis
            stroke={FG_SUBTLE}
            tickLine={false}
            axisLine={false}
            width={32}
            tick={{
              fontFamily: 'var(--font-mono)',
              fontSize: 10,
              fill: FG_SUBTLE,
            }}
          />
          <Tooltip
            cursor={{ stroke: EMERALD, strokeWidth: 1, strokeDasharray: '2 3' }}
            contentStyle={{
              background: '#FFFFFF',
              border: `1px solid ${BORDER}`,
              borderRadius: 8,
              fontFamily: 'var(--font-sans)',
              fontSize: 12,
              color: INK,
              boxShadow:
                '0 6px 14px -4px rgba(14, 26, 20, 0.08), 0 2px 6px -2px rgba(14, 26, 20, 0.04)',
            }}
            labelStyle={{ color: FG_MUTED, fontWeight: 500 }}
          />
          <Area
            type="monotone"
            dataKey="value"
            stroke={EMERALD}
            strokeWidth={1.75}
            fill={`url(#${gradientId})`}
            dot={false}
            activeDot={{ fill: EMERALD, r: 3, strokeWidth: 0 }}
          />
          {showConversions ? (
            <Area
              type="monotone"
              dataKey="conversions"
              stroke={LAVENDER}
              strokeWidth={1.75}
              fill="transparent"
              dot={false}
              activeDot={{ fill: LAVENDER_DEEP, r: 3, strokeWidth: 0 }}
            />
          ) : null}
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}

/**
 * Sparkline — micro bar chart for inline stat tiles. Pure SVG (no
 * recharts) so it stays cheap to render dozens at a time. Bars are
 * paper-4 with the highest bar(s) in emerald (or lavender if
 * `accent='lavender'`).
 */
export interface SparklineProps {
  /** Array of bar heights as percentages (0 to 100). */
  values: number[];
  /** Highlight the top N tallest bars in the accent colour. Default 1. */
  peakCount?: number;
  /** Colour used for peak bars. Default emerald. */
  accent?: 'emerald' | 'lavender';
  className?: string;
  ariaLabel?: string;
}

export function Sparkline({
  values,
  peakCount = 1,
  accent = 'emerald',
  className,
  ariaLabel,
}: SparklineProps): React.ReactElement {
  // Find the N highest values so we know which bars get the accent.
  const peakSet = React.useMemo(() => {
    const sorted = [...values]
      .map((v, i) => ({ v, i }))
      .sort((a, b) => b.v - a.v)
      .slice(0, Math.max(0, peakCount))
      .map((x) => x.i);
    return new Set(sorted);
  }, [values, peakCount]);

  const peakColour = accent === 'emerald' ? EMERALD : LAVENDER;
  return (
    <div
      className={cn('flex h-8 items-end gap-[3px]', className)}
      role="img"
      aria-label={ariaLabel ?? 'Sparkline'}
    >
      {values.map((value, index) => (
        <span
          key={index}
          className="flex-1 rounded-[1px]"
          style={{
            height: `${Math.max(2, Math.min(100, value))}%`,
            background: peakSet.has(index) ? peakColour : '#DAD3BD',
          }}
        />
      ))}
    </div>
  );
}
