/**
 * Wordmark — snapshot of the composite "Go" + italic "Next" mark.
 */
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';

import { Wordmark } from './Wordmark';

describe('<Wordmark>', () => {
  it('renders both halves of the composite mark', () => {
    const { container } = render(<Wordmark />);
    const go = container.querySelector('.wm-go');
    const next = container.querySelector('.wm-next');
    expect(go?.textContent).toBe('Go');
    expect(next?.textContent).toBe('Next');
  });

  it('exposes an aria-label so the composite reads as "GoNext" to AT', () => {
    const { container } = render(<Wordmark />);
    expect(container.firstChild).toHaveAttribute('aria-label', 'GoNext');
  });

  it('applies the forest swap when surface="forest"', () => {
    const { container } = render(<Wordmark surface="forest" />);
    const next = container.querySelector('.wm-next');
    // On forest, the italic half lifts to emerald-bright — surfaced
    // through a Tailwind class. We don't compute styles in jsdom, so
    // we just assert the class is present.
    expect(next?.className).toContain('text-emerald-bright');
  });
});
