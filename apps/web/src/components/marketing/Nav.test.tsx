/**
 * Marketing nav — smoke + brand contract.
 *
 * The nav is an async Server Component (it pulls the site name from
 * the settings registry). To keep these unit tests synchronous we
 * pre-resolve the async result to its rendered ReactElement before
 * handing it to RTL.
 */
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';

import { MarketingNav } from './Nav';

async function renderNav(props: Parameters<typeof MarketingNav>[0] = {}) {
  // The default props path would fetch from the settings endpoint;
  // we pass siteName explicitly so the component takes the prop path
  // and never touches `globalThis.fetch`.
  const element = await MarketingNav({ siteName: 'GoNext', ...props });
  return render(element);
}

describe('<MarketingNav>', () => {
  it('renders the wordmark linked to /', async () => {
    const { container } = await renderNav();
    const wordmark = container.querySelector('a[aria-label="GoNext"]');
    expect(wordmark).toBeNull(); // Wordmark is a span here
    const homeLink = container.querySelector('a[href="/"]');
    expect(homeLink?.textContent).toContain('GoNext');
  });

  it('renders the CTA to start a site', async () => {
    const { container } = await renderNav();
    const cta = Array.from(container.querySelectorAll('a')).find(
      (a) => a.textContent?.trim().startsWith('Start a site'),
    );
    expect(cta).toBeTruthy();
    expect(cta?.getAttribute('href')).toBe('/start');
  });

  it('paints the nav on a forest surface', async () => {
    const { container } = await renderNav();
    const nav = container.querySelector('nav');
    expect(nav?.getAttribute('data-surface')).toBe('forest');
    expect(nav?.getAttribute('aria-label')).toBe('Primary');
  });

  it('splits the site name on the first space — "Acme Studio" → "Acme" + italic "Studio"', async () => {
    const { container } = await renderNav({ siteName: 'Acme Studio' });
    expect(container.querySelector('.wm-go')?.textContent).toBe('Acme');
    expect(container.querySelector('.wm-next')?.textContent).toBe('Studio');
  });

  it('renders a single-word site name in the bold half only', async () => {
    const { container } = await renderNav({ siteName: 'Acme' });
    expect(container.querySelector('.wm-go')?.textContent).toBe('Acme');
    // No italic half when the name has no second word.
    expect(container.querySelector('.wm-next')).toBeNull();
  });
});
