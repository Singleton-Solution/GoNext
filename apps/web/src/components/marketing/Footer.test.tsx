/**
 * Marketing footer — link column + brand contract.
 */
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';

import { MarketingFooter } from './Footer';

describe('<MarketingFooter>', () => {
  it('renders four column headings', () => {
    const { container } = render(<MarketingFooter />);
    const headings = container.querySelectorAll('h5');
    expect(headings.length).toBe(4);
    const text = Array.from(headings).map((h) => h.textContent);
    expect(text).toEqual(['Product', 'Resources', 'Company', 'Legal']);
  });

  it('paints on a forest surface', () => {
    const { container } = render(<MarketingFooter />);
    const footer = container.querySelector('footer');
    expect(footer?.getAttribute('data-surface')).toBe('forest');
  });

  it('renders the brand wordmark in the brand-foot column', () => {
    const { container } = render(<MarketingFooter />);
    expect(container.querySelector('.wm-go')?.textContent).toBe('Go');
    expect(container.querySelector('.wm-next')?.textContent).toBe('Next');
  });
});
