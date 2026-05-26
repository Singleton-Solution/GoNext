/**
 * Alive band — the forest section sitting between the features grid
 * and the comparison table. Mirrors the kit's `.alive-band`: a wide
 * dark surface with two radial-gradient glows (emerald top-right,
 * lavender bottom-left) and four large stats in a 4-column grid.
 *
 * Stats use Geist Mono for the unit suffix per the kit, and the
 * italic-accent rule fires on the dramatic "zero PHP" stat.
 */
import type { ReactElement } from 'react';

import { Headline } from '@/components/brand/Headline';

interface StatProps {
  label: string;
  /** Value markup — pass `<em>` for the italic-accent rule. */
  value: ReactElement | string;
  description: string;
}

function Stat({ label, value, description }: StatProps): ReactElement {
  return (
    <div className="rounded-lg border border-forest-border bg-white/[0.04] p-6">
      <div className="text-xs font-medium uppercase tracking-[0.1em] text-fg-on-forest-muted">
        {label}
      </div>
      <div className="mt-3 font-display text-5xl font-extrabold leading-none tracking-tight text-fg-on-forest [&_em]:font-serif [&_em]:font-normal [&_em]:italic [&_em]:text-emerald-bright">
        {value}
      </div>
      <p className="mt-2.5 text-xs leading-[1.5] text-fg-on-forest-muted">
        {description}
      </p>
    </div>
  );
}

export function MarketingAliveBand(): ReactElement {
  return (
    <section
      data-surface="forest"
      className="relative overflow-hidden bg-forest py-[120px] text-fg-on-forest"
    >
      {/* Emerald glow */}
      <div
        aria-hidden
        className="pointer-events-none absolute -top-1/2 right-[-10%] size-[800px]"
        style={{
          background:
            'radial-gradient(circle, rgba(16,185,129,0.16) 0%, transparent 60%)',
        }}
      />
      {/* Lavender glow */}
      <div
        aria-hidden
        className="pointer-events-none absolute -bottom-1/2 left-[-10%] size-[800px]"
        style={{
          background:
            'radial-gradient(circle, rgba(167,139,250,0.10) 0%, transparent 60%)',
        }}
      />

      <div className="relative mx-auto max-w-[1240px] px-8">
        <div className="mx-auto mb-14 max-w-[760px] text-center">
          <span className="mb-3.5 inline-block text-xs font-medium uppercase tracking-[0.12em] text-emerald-bright">
            A platform that responds
          </span>
          <Headline size="section" className="text-[clamp(40px,5vw,64px)]">
            The site you ship is the <em>same</em> site that grows.
          </Headline>
          <p className="mt-4 text-md leading-[1.55] text-fg-on-forest-muted">
            Every reader, every order, every search query feeds back.
            GoNext surfaces what&apos;s working and quietly retires what
            isn&apos;t.
          </p>
        </div>

        <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Stat
            label="Median TTFB"
            value={
              <>
                38<em>ms</em>
              </>
            }
            description="Across 24 edge regions. WordPress baseline: ~480ms."
          />
          <Stat
            label="Active sites"
            value="12,847"
            description="From solo writers to multi-brand agencies."
          />
          <Stat
            label="Lines of PHP"
            value={<em>zero</em>}
            description="Go on the backend, Next.js on the front. Both are good ideas."
          />
          <Stat
            label="Uptime · 12mo"
            value={
              <>
                99.99
                <em className="text-[0.5em] text-fg-on-forest-muted ml-0.5">
                  %
                </em>
              </>
            }
            description="SLA-backed on Studio and above."
          />
        </div>
      </div>
    </section>
  );
}
