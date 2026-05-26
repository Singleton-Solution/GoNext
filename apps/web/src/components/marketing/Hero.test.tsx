/**
 * Marketing hero — smoke + brand-contract assertions.
 *
 * We don't snapshot the entire HTML (it'll churn whenever copy
 * changes), but we pin the three contracts that matter:
 *  - The signature headline composes "Sites that *live* and grow."
 *    with an <em> on "live" so the italic-accent rule fires.
 *  - The emerald primary CTA is labelled "Start a site".
 *  - The PulseVisual ships the 38ms metric on a forest surface.
 */
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';

import { MarketingHero } from './Hero';

describe('<MarketingHero>', () => {
  it('paints the signature headline with the italic accent', () => {
    const { container } = render(<MarketingHero />);
    const h1 = container.querySelector('h1');
    expect(h1?.textContent).toContain('Sites that');
    expect(h1?.textContent).toContain('and grow');
    const em = h1?.querySelector('em');
    expect(em?.textContent).toBe('live');
  });

  it('renders the emerald primary CTA with the brand copy', () => {
    const { container } = render(<MarketingHero />);
    const cta = Array.from(container.querySelectorAll('a')).find(
      (a) => a.textContent?.trim().startsWith('Start a site'),
    );
    expect(cta).toBeTruthy();
  });

  it('paints the 38ms TTFB visual on a forest surface', () => {
    const { container } = render(<MarketingHero />);
    const surface = container.querySelector('[data-surface="forest"]');
    expect(surface).toBeTruthy();
    expect(surface?.textContent).toContain('38');
    expect(surface?.textContent).toContain('p50 TTFB');
  });
});
