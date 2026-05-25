/**
 * Features grid — the three-column section the kit titles "One product
 * for everything you used *five* for."
 *
 * Each card has a coloured icon plaque (emerald or lavender soft), an
 * Archivo headline with one italic-accent word, a short description,
 * and a bulleted feature list. Pixel measurements track the kit:
 * 28px padding, 14px gap, 40x40 icon wrapper at r-md radius.
 */
import {
  Check,
  Cloud,
  FileText,
  ShoppingBag,
  type LucideIcon,
} from 'lucide-react';
import type { ReactElement } from 'react';

import { Headline } from '@/components/brand/Headline';

interface FeatureProps {
  icon: LucideIcon;
  tone: 'emerald' | 'lavender';
  /** Heading; pass `<em>…</em>` markers inline for the italic accent. */
  heading: ReactElement;
  /** Body paragraph. */
  body: string;
  /** Three short bullets. */
  bullets: ReadonlyArray<string>;
}

function FeatureCard({
  icon: Icon,
  tone,
  heading,
  body,
  bullets,
}: FeatureProps): ReactElement {
  const iconBg = tone === 'emerald' ? 'bg-emerald-soft' : 'bg-lavender-soft';
  const iconFg =
    tone === 'emerald' ? 'text-emerald-deep' : 'text-lavender-deep';
  return (
    <article className="flex flex-col gap-3.5 rounded-lg border border-border bg-paper-2 p-7 transition-colors duration-DEFAULT ease-brand hover:border-border-strong">
      <div
        className={`flex size-10 items-center justify-center rounded-md ${iconBg}`}
      >
        <Icon className={`size-5 ${iconFg}`} aria-hidden />
      </div>
      <Headline as="h3" size="sub" className="text-[26px]">
        {heading}
      </Headline>
      <p className="text-sm leading-[1.55] text-fg-muted">{body}</p>
      <ul className="flex flex-col gap-2 border-t border-border pt-3.5">
        {bullets.map((b) => (
          <li
            key={b}
            className="flex items-center gap-2 text-sm text-ink-soft"
          >
            <Check className={`size-3.5 ${iconFg}`} aria-hidden />
            {b}
          </li>
        ))}
      </ul>
    </article>
  );
}

export function MarketingFeatures(): ReactElement {
  return (
    <section className="py-[120px]">
      <div className="mx-auto max-w-[1240px] px-8">
        <div className="mx-auto mb-14 max-w-[760px] text-center">
          <span className="mb-3.5 inline-block text-xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
            Built in, not bolted on
          </span>
          <Headline size="section" className="text-[clamp(40px,5vw,64px)]">
            One product for everything you used <em>five</em> for.
          </Headline>
          <p className="mt-4 text-md leading-[1.55] text-fg-muted">
            Content, hosting, commerce, and analytics — woven into a single
            product. No plugin marketplace required to ship a real site.
          </p>
        </div>
        <div className="grid gap-5 md:grid-cols-3">
          <FeatureCard
            icon={FileText}
            tone="emerald"
            heading={
              <>
                Living <em>content</em>
              </>
            }
            body="A modern block editor with real-time preview. Posts evolve — version history, scheduled publishing, A/B variants built in."
            bullets={[
              'Multi-author roles & review',
              'Revisions & rollback',
              'Type-safe custom fields',
            ]}
          />
          <FeatureCard
            icon={Cloud}
            tone="lavender"
            heading={
              <>
                Edge <em>hosting</em>
              </>
            }
            body="Edge-rendered Next.js on 24 regions. Auto SSL, auto cache, auto image optimization — never think about it again."
            bullets={[
              'p50 TTFB under 50ms',
              'Custom domains in 90 seconds',
              'Preview deploys per branch',
            ]}
          />
          <FeatureCard
            icon={ShoppingBag}
            tone="emerald"
            heading={
              <>
                Native <em>commerce</em>
              </>
            }
            body="Products and orders are first-class — not a plugin. Stripe-powered checkout, taxes, subscriptions, and inventory."
            bullets={[
              'Digital, physical, subscription',
              'Inventory & warehouse sync',
              'Tax engine, 40 regions',
            ]}
          />
        </div>
      </div>
    </section>
  );
}
