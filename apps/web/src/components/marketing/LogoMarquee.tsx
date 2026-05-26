/**
 * Logo marquee — the strip just under the hero showing wordmarks of
 * teams using GoNext. The kit calls this the "logos strip" and stamps
 * the line "Built by, and for, teams who treat their sites as *living
 * things*" above it.
 *
 * The "logos" are not real partner logos here — we use wordmark
 * compositions in the brand type (Archivo + Instrument Serif italic)
 * because partner logos vary in colour and would fight the cream
 * surface. Once a real customer wall lands these become <Image>s.
 */
import type { ReactElement } from 'react';

const LOGOS: ReadonlyArray<{ before: string; em?: string; after?: string }> = [
  { before: 'Brick', em: 'Mortar' },
  { before: 'Untitled', em: 'Studio' },
  { before: 'Quiet', em: 'Co' },
  { before: 'Cobalt' },
  { before: 'Ortho', em: 'Type' },
  { before: 'Riemann' },
];

export function MarketingLogos(): ReactElement {
  return (
    <section className="border-y border-border py-14">
      <div className="mx-auto max-w-[1240px] px-8">
        <p className="mb-7 text-center text-sm tracking-wide text-fg-subtle">
          Built by, and for, teams who treat their sites as{' '}
          <em className="font-serif italic font-normal text-emerald-deep">
            living things
          </em>
        </p>
        <ul className="flex flex-wrap items-center justify-center gap-x-14 gap-y-6">
          {LOGOS.map((logo, idx) => (
            <li
              key={`${logo.before}-${idx}`}
              className="font-display text-[22px] font-bold tracking-tight text-fg-faint"
            >
              {logo.before}
              {logo.em ? (
                <em className="font-serif italic font-normal">
                  {logo.em}
                </em>
              ) : null}
              {logo.after ?? ''}
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}
