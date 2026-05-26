/**
 * Closing CTA — the giant centered headline + two-button row that
 * caps the marketing page. Mirrors the kit's `.cta-block`.
 */
import Link from 'next/link';
import { ArrowRight } from 'lucide-react';
import type { ReactElement } from 'react';

import { Headline } from '@/components/brand/Headline';

export function MarketingCtaBlock(): ReactElement {
  return (
    <section className="py-[120px] text-center">
      <div className="mx-auto max-w-[1240px] px-8">
        <Headline size="display" as="h2" className="leading-[0.95]">
          Ship something that <em>lives</em>.
        </Headline>
        <p className="mx-auto mb-8 mt-5 max-w-[540px] text-lg leading-[1.5] text-fg-muted">
          Spin up a real site in under two minutes. Free, no credit card,
          your WordPress export is welcome.
        </p>
        <div className="inline-flex flex-wrap items-center justify-center gap-2.5">
          <Link
            href="/start"
            className="inline-flex items-center justify-center gap-1.5 rounded-md border border-ink bg-ink px-5 py-3 text-base font-medium text-paper no-underline shadow-xs transition-colors duration-DEFAULT ease-brand hover:border-forest-2 hover:bg-forest-2"
          >
            Start a site
            <ArrowRight className="size-[14px]" aria-hidden />
          </Link>
          <Link
            href="/sales"
            className="inline-flex items-center justify-center gap-1.5 rounded-md border border-border bg-paper-2 px-5 py-3 text-base font-medium text-ink no-underline shadow-xs transition-colors duration-DEFAULT ease-brand hover:border-border-strong hover:bg-paper-3"
          >
            Talk to sales
          </Link>
        </div>
      </div>
    </section>
  );
}
