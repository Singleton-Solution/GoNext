/**
 * Landing page — brand-contract snapshot.
 *
 * We pin the brand contract (display headline + italic accent + two
 * CTAs + forest band + feature grid) rather than the raw HTML. The
 * actual rendering is straight JSX, so the assertions read like the
 * design handoff:
 *
 *   1. The hero headline carries the giant Archivo class hooks and an
 *      <em> with the italic-accent emerald token.
 *   2. There are exactly two primary CTAs — emerald "Read the docs"
 *      pointing into the architecture overview, and a paper-2
 *      "API reference" secondary.
 *   3. The forest band has its own headline (also with an italic
 *      accent) and exposes a card-per-subsystem feature grid.
 *
 * The CSS rules that turn these class names into pixels are unit-
 * tested implicitly by the typecheck / build pass that compiles
 * Tailwind; here we only assert the JSX contract.
 */
import { render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import LandingPage from '@/app/page';

describe('Landing page', () => {
  it('renders the hero headline with the brand italic accent', () => {
    render(<LandingPage />);

    const heading = screen.getByRole('heading', { level: 1 });
    expect(heading).toHaveTextContent(/Docs that\s+grow\s+with you/);
    expect(heading.className).toContain('landing__title');
    // The italic-accent rule lives inside <em>.
    const accent = heading.querySelector('em');
    expect(accent).not.toBeNull();
    expect(accent?.textContent).toBe('grow');
  });

  it('renders both primary and secondary CTAs', () => {
    render(<LandingPage />);

    const primary = screen.getByRole('link', { name: /read the docs/i });
    expect(primary.getAttribute('href')).toBe('/docs/00-architecture-overview');
    expect(primary.className).toContain('landing__cta--primary');

    const secondary = screen.getByRole('link', { name: /api reference/i });
    expect(secondary.getAttribute('href')).toBe('/api');
    expect(secondary.className).toContain('landing__cta--secondary');
  });

  it('renders the forest band with its own italic-accent headline', () => {
    render(<LandingPage />);

    const bandHeading = screen.getByRole('heading', { level: 2 });
    expect(bandHeading).toHaveTextContent(/One product for everything you used\s+five\s+for/);
    expect(bandHeading.className).toContain('landing__band-title');
    const accent = bandHeading.querySelector('em');
    expect(accent?.textContent).toBe('five');

    // Subsystem grid is rendered inside the band, with one link per feature.
    const band = bandHeading.closest('section');
    expect(band).not.toBeNull();
    const featureLinks = within(band as HTMLElement).getAllByRole('link');
    expect(featureLinks.length).toBeGreaterThanOrEqual(6);
  });

  it('exposes a path into the ADR list as a footnote', () => {
    render(<LandingPage />);

    const adrLink = screen.getByRole('link', { name: /architecture decision records/i });
    expect(adrLink.getAttribute('href')).toBe('/adr');
  });
});
