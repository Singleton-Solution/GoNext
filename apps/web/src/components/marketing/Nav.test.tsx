/**
 * Marketing nav — smoke + brand contract.
 */
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';

import { MarketingNav } from './Nav';

describe('<MarketingNav>', () => {
  it('renders the wordmark linked to /', () => {
    const { container } = render(<MarketingNav />);
    const wordmark = container.querySelector('a[aria-label="GoNext"]');
    expect(wordmark).toBeNull(); // Wordmark is a span here
    const homeLink = container.querySelector('a[href="/"]');
    expect(homeLink?.textContent).toContain('GoNext');
  });

  it('renders the CTA to start a site', () => {
    const { container } = render(<MarketingNav />);
    const cta = Array.from(container.querySelectorAll('a')).find(
      (a) => a.textContent?.trim().startsWith('Start a site'),
    );
    expect(cta).toBeTruthy();
    expect(cta?.getAttribute('href')).toBe('/start');
  });

  it('paints the nav on a forest surface', () => {
    const { container } = render(<MarketingNav />);
    const nav = container.querySelector('nav');
    expect(nav?.getAttribute('data-surface')).toBe('forest');
    expect(nav?.getAttribute('aria-label')).toBe('Primary');
  });
});
