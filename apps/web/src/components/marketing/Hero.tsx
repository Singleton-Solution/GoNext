/**
 * Marketing hero — cream surface, massive headline + emerald CTA, dark
 * "Pulse" visual card on the right.
 *
 * Mirrors the `hero` section of docs/design/ui_kits/marketing/index.html
 * down to the meta-row checks, the chart curve, and the three live
 * stats. The chart shape is an inline SVG matching the kit verbatim;
 * the dot at the right end pulses via the `brand-pulse` keyframe
 * declared in tailwind.config.ts.
 *
 * The italic-accent rule fires twice: once in the headline ("live")
 * and once in the visual card ("ms" suffix on 38ms TTFB). One italic
 * word per headline is the law — two are used here only because the
 * visual card sits on a forest surface where the second italic carries
 * different colour weight (emerald-bright instead of emerald-deep).
 */
import Link from 'next/link';
import { ArrowRight, Check, Play } from 'lucide-react';
import type { ReactElement } from 'react';

import { Headline } from '@/components/brand/Headline';

export function MarketingHero(): ReactElement {
  return (
    <section className="pb-[60px] pt-[80px]">
      <div className="mx-auto grid max-w-[1240px] grid-cols-1 items-center gap-9 px-8 lg:grid-cols-[1.3fr_1fr]">
        <div>
          <span className="mb-3.5 inline-block text-xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
            GoNext v1.0 — now generally available
          </span>
          <Headline size="display" as="h1" className="leading-[0.9]">
            Sites that
            <br />
            <em>live</em> and grow.
          </Headline>
          <p className="mb-8 mt-7 max-w-[480px] text-[19px] leading-[1.5] text-fg-muted">
            An all-in-one alternative to WordPress — content, hosting, and
            commerce in one product. Built on Go and Next.js, with
            intelligence woven in.
          </p>
          <div className="flex flex-wrap items-center gap-2.5">
            <Link
              href="/start"
              className="inline-flex items-center justify-center gap-1.5 rounded-md border border-emerald bg-emerald px-5 py-3 text-base font-medium text-emerald-ink no-underline shadow-xs transition-colors duration-DEFAULT ease-brand hover:border-emerald-deep hover:bg-emerald-deep hover:text-paper"
            >
              Start a site
              <ArrowRight className="size-[14px]" aria-hidden />
            </Link>
            <Link
              href="/demo"
              className="inline-flex items-center justify-center gap-1.5 rounded-md border border-border bg-paper-2 px-5 py-3 text-base font-medium text-ink no-underline shadow-xs transition-colors duration-DEFAULT ease-brand hover:border-border-strong hover:bg-paper-3"
            >
              <Play className="size-[14px]" aria-hidden />
              Watch demo · 2 min
            </Link>
          </div>
          <ul className="mt-7 flex flex-wrap gap-5 text-xs text-fg-subtle">
            <li className="flex items-center gap-1.5">
              <Check className="size-[13px] text-emerald-deep" aria-hidden />
              No credit card
            </li>
            <li className="flex items-center gap-1.5">
              <Check className="size-[13px] text-emerald-deep" aria-hidden />
              WordPress importer included
            </li>
            <li className="flex items-center gap-1.5">
              <Check className="size-[13px] text-emerald-deep" aria-hidden />
              SOC 2 · GDPR · CCPA
            </li>
          </ul>
        </div>

        <PulseVisual />
      </div>
    </section>
  );
}

/**
 * The dark "Pulse" visual card sitting in the right-hand column of the
 * hero. Self-contained — pulled out so the hero markup stays readable
 * and so the visual can be reused on the /pulse landing if/when we
 * surface a separate analytics-marketing page.
 *
 * The radial-gradient glows are pure CSS pseudo-elements; the chart
 * SVG is verbatim from the marketing kit so the curve and gradient
 * stops match pixel-for-pixel.
 */
function PulseVisual(): ReactElement {
  return (
    <div
      data-surface="forest"
      className="relative flex aspect-[4/5] flex-col justify-between overflow-hidden rounded-xl bg-forest p-7 text-fg-on-forest"
    >
      {/* Emerald glow — top-right. */}
      <div
        aria-hidden
        className="pointer-events-none absolute -right-[20%] -top-[20%] size-[600px] rounded-pill"
        style={{
          background:
            'radial-gradient(circle, rgba(16,185,129,0.20) 0%, transparent 55%)',
        }}
      />
      {/* Lavender glow — bottom-left. */}
      <div
        aria-hidden
        className="pointer-events-none absolute -bottom-[30%] -left-[20%] size-[500px] rounded-pill"
        style={{
          background:
            'radial-gradient(circle, rgba(167,139,250,0.15) 0%, transparent 55%)',
        }}
      />

      {/* Header — live badge + domain. */}
      <div className="relative flex items-start justify-between">
        <span className="inline-flex items-center gap-1.5 rounded-pill border border-emerald-bright/40 bg-emerald-bright/15 px-2.5 py-1 text-2xs font-medium uppercase tracking-wide text-emerald-bright">
          <span className="size-[5px] animate-brand-pulse rounded-pill bg-emerald-bright" />
          Live · 142 readers
        </span>
        <span className="font-mono text-xs text-fg-on-forest-muted">
          brickmortar.co
        </span>
      </div>

      {/* Big number — p50 TTFB. */}
      <div className="relative text-center">
        <div className="text-xs font-medium uppercase tracking-[0.14em] text-emerald-bright">
          p50 TTFB
        </div>
        <div className="mt-2 font-display text-[84px] font-extrabold leading-none tracking-tight text-fg-on-forest">
          38
          <em className="font-serif text-[0.9em] not-italic italic font-normal text-emerald-bright">
            ms
          </em>
        </div>
        <div className="mt-1 text-sm text-fg-on-forest-muted">
          across 24 edge regions, right now
        </div>
      </div>

      {/* Chart — emerald area, dot pulses at the right edge. */}
      <div className="relative h-20">
        <svg
          viewBox="0 0 600 80"
          preserveAspectRatio="none"
          className="size-full"
          aria-hidden
        >
          <defs>
            <linearGradient id="hpFill" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#34D399" stopOpacity="0.5" />
              <stop offset="100%" stopColor="#34D399" stopOpacity="0" />
            </linearGradient>
          </defs>
          <path
            d="M 0 60 Q 30 45, 60 50 T 120 40 T 180 55 T 240 35 T 300 42 T 360 28 T 420 36 T 480 22 T 540 32 T 600 24 L 600 80 L 0 80 Z"
            fill="url(#hpFill)"
          />
          <path
            d="M 0 60 Q 30 45, 60 50 T 120 40 T 180 55 T 240 35 T 300 42 T 360 28 T 420 36 T 480 22 T 540 32 T 600 24"
            fill="none"
            stroke="#34D399"
            strokeWidth="1.5"
          />
          <circle cx="600" cy="24" r="4" fill="#34D399" />
          <circle cx="600" cy="24" r="8" fill="#34D399" opacity="0.3" />
        </svg>
      </div>

      {/* Stats footer. */}
      <div className="relative grid grid-cols-3 gap-3 border-t border-forest-border pt-4">
        <Stat label="Views · 24h" value="12.4k" />
        <Stat label="Revenue · 24h" value="$847" />
        <Stat label="Uptime · 30d" value="100" suffix="%" />
      </div>
    </div>
  );
}

interface StatProps {
  label: string;
  value: string;
  suffix?: string;
}

function Stat({ label, value, suffix }: StatProps): ReactElement {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-[10px] uppercase tracking-[0.1em] text-fg-on-forest-muted">
        {label}
      </span>
      <span className="font-display text-[22px] font-bold tracking-tight text-fg-on-forest">
        {value}
        {suffix ? (
          <em className="font-serif text-[0.6em] font-normal italic text-fg-on-forest-muted">
            {suffix}
          </em>
        ) : null}
      </span>
    </div>
  );
}
