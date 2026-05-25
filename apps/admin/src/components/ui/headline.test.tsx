/**
 * Headline — visual contract tests.
 *
 * The italic-accent rule is the brand's signature move; we pin
 * three behaviours so a refactor can't silently regress it:
 *
 *   1. Cream-surface render with <em> → outer Archivo, inner serif
 *      with emerald-deep colour token.
 *   2. Forest-surface render with <em> → outer text re-tunes to
 *      fg-on-forest, inner accent re-tunes to emerald-bright.
 *   3. Plain string without <em> → no serif descendant is created,
 *      tag is still the chosen heading element.
 *
 * We assert on the Tailwind class strings rather than rendered
 * pixels because jsdom doesn't apply Tailwind's compiled CSS;
 * matching the class contract is the closest proxy to "the right
 * styles will reach the browser".
 */
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { Headline } from './headline';

describe('<Headline>', () => {
  it('renders Archivo on cream with an emerald-deep italic accent', () => {
    render(
      <Headline as="h1" size="page">
        Welcome <em>back</em>.
      </Headline>,
    );

    const heading = screen.getByRole('heading', { level: 1 });
    // Outer tag is the heavy grotesque display.
    expect(heading.className).toContain('font-display');
    expect(heading.className).toContain('font-extrabold');
    // Italic-accent rule is wired in. The contract is the descendant
    // selector — we check the canonical class fragments are present.
    expect(heading.className).toContain('[&_em]:font-serif');
    expect(heading.className).toContain('[&_em]:italic');
    expect(heading.className).toContain('[&_em]:text-emerald-deep');
    // Page size resolves to the clamp() page scale.
    expect(heading.className).toContain('leading-[1.0]');
    // The <em> survives rendering.
    expect(heading.querySelector('em')).not.toBeNull();
    expect(heading.querySelector('em')?.textContent).toBe('back');
  });

  it('renders on a forest surface with emerald-bright italic accent', () => {
    render(
      <Headline as="h2" size="section" surface="forest">
        Sites that <em>live</em> and grow.
      </Headline>,
    );

    const heading = screen.getByRole('heading', { level: 2 });
    expect(heading.getAttribute('data-surface')).toBe('forest');
    // Forest surface promotes the headline text to fg-on-forest and
    // the italic accent to emerald-bright (vs. emerald-deep on cream).
    expect(heading.className).toContain('data-[surface=forest]:text-fg-on-forest');
    expect(heading.className).toContain(
      'data-[surface=forest]:[&_em]:text-emerald-bright',
    );
    // Italic accent is still rendered.
    expect(heading.querySelector('em')?.textContent).toBe('live');
  });

  it('renders a plain headline without an italic accent', () => {
    render(
      <Headline as="h3" size="sub">
        Plain headline
      </Headline>,
    );

    const heading = screen.getByRole('heading', { level: 3 });
    // No <em>, no serif accent in the DOM — but the descendant
    // selectors are still in the className (they're applied to
    // children, not to the heading itself).
    expect(heading.querySelector('em')).toBeNull();
    expect(heading.textContent).toBe('Plain headline');
    // The "sub" size resolves to 32px.
    expect(heading.className).toContain('text-[32px]');
  });
});
