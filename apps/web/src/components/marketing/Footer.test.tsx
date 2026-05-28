/**
 * Marketing footer — link column + brand contract.
 *
 * Footer is an async Server Component (it pulls the site name +
 * tagline from the settings registry). Pre-resolve the async result
 * to its rendered ReactElement so RTL can mount it synchronously.
 */
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';

import { MarketingFooter } from './Footer';

async function renderFooter(props: Parameters<typeof MarketingFooter>[0] = {}) {
  const element = await MarketingFooter({
    // The brand mark splits on the first space; pass the two-word
    // form so the wordmark's bold + italic halves render the
    // documented "Go" + "Next" pair.
    siteName: 'Go Next',
    siteTagline:
      'An all-in-one platform for content, hosting, and commerce.',
    ...props,
  });
  return render(element);
}

describe('<MarketingFooter>', () => {
  it('renders four column headings', async () => {
    const { container } = await renderFooter();
    const headings = container.querySelectorAll('h5');
    expect(headings.length).toBe(4);
    const text = Array.from(headings).map((h) => h.textContent);
    expect(text).toEqual(['Product', 'Resources', 'Company', 'Legal']);
  });

  it('paints on a forest surface', async () => {
    const { container } = await renderFooter();
    const footer = container.querySelector('footer');
    expect(footer?.getAttribute('data-surface')).toBe('forest');
  });

  it('renders the brand wordmark in the brand-foot column', async () => {
    const { container } = await renderFooter();
    expect(container.querySelector('.wm-go')?.textContent).toBe('Go');
    expect(container.querySelector('.wm-next')?.textContent).toBe('Next');
  });

  it('uses the configured tagline in the brand column', async () => {
    const { container } = await renderFooter({
      siteTagline: 'Calm software, alive.',
    });
    expect(container.textContent).toContain('Calm software, alive.');
  });

  it('stamps the configured site name into the © line', async () => {
    const { container } = await renderFooter({ siteName: 'Acme Studio' });
    const year = new Date().getFullYear();
    expect(container.textContent).toContain(`© ${year} Acme Studio`);
  });
});
