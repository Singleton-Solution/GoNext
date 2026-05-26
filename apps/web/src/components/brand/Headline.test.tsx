/**
 * Headline brand-primitive tests.
 *
 * Mirrors apps/admin/src/components/ui/headline.test.tsx — we assert
 * the same italic-accent contract on the public-site surface so the
 * two implementations stay in lock-step. The component is a thin
 * wrapper over Archivo + Instrument Serif, so the tests focus on
 * structural concerns (tag, attributes) rather than computed styles.
 */
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';

import { Headline } from './Headline';

describe('<Headline>', () => {
  it('renders as h1 by default', () => {
    const { container } = render(<Headline>Sites</Headline>);
    expect(container.querySelector('h1')).not.toBeNull();
  });

  it('honors the `as` prop for semantic nesting', () => {
    const { container } = render(<Headline as="h2">Section</Headline>);
    expect(container.querySelector('h2')).not.toBeNull();
    expect(container.querySelector('h1')).toBeNull();
  });

  it('preserves an inner <em> for the italic-accent rule', () => {
    const { container } = render(
      <Headline>
        Sites that <em>live</em> and grow.
      </Headline>,
    );
    expect(container.querySelector('em')?.textContent).toBe('live');
  });

  it('stamps data-surface="forest" when the prop is set', () => {
    const { container } = render(
      <Headline surface="forest">
        Forest <em>headline</em>
      </Headline>,
    );
    expect(
      container.querySelector('[data-surface="forest"]'),
    ).not.toBeNull();
  });

  it('omits data-surface when cream (default)', () => {
    const { container } = render(<Headline>Cream</Headline>);
    expect(
      container.querySelector('[data-surface="forest"]'),
    ).toBeNull();
  });
});
