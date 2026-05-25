/**
 * ThemeBrowser tests.
 *
 * The browser is a presentation-heavy surface; the assertions here
 * pin the brand contract:
 *
 *   1. Every theme entry renders a card with the slug-based test id.
 *   2. The active theme is decorated with the "Active" badge and the
 *      emerald border (we assert via the data-active attribute the
 *      card sets when it lights up).
 *   3. Activate buttons surface the right accessible name and clicking
 *      a non-active card transfers the badge to that card.
 *   4. The headline uses the brand italic-accent rule (an <em> tag
 *      with the word "appearance" lands inside the h1).
 *   5. The descriptions render their `*word*` accents as serif italic
 *      emphasis spans the brand stylesheet picks up.
 *   6. A snapshot captures the gallery shape so theme-card layout
 *      changes have to be deliberate.
 */
import { describe, expect, it } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { ThemeBrowser, type ThemeCard } from './ThemeBrowser';

const FIXTURE_THEMES: readonly ThemeCard[] = [
  {
    slug: 'gn-hello',
    name: 'Hello',
    description: 'A *living* starter theme.',
    tags: ['Editorial'],
    preview: 'editorial',
  },
  {
    slug: 'counter',
    name: 'Counter',
    description: 'Commerce that *grows*.',
    tags: ['Commerce'],
    preview: 'shop',
  },
];

describe('ThemeBrowser', () => {
  it('renders one card per theme', () => {
    render(<ThemeBrowser themes={FIXTURE_THEMES} activeSlug="gn-hello" />);
    expect(screen.getByTestId('theme-card-gn-hello')).toBeInTheDocument();
    expect(screen.getByTestId('theme-card-counter')).toBeInTheDocument();
  });

  it('marks only the active theme with the Active badge', () => {
    render(<ThemeBrowser themes={FIXTURE_THEMES} activeSlug="gn-hello" />);
    expect(screen.getByTestId('theme-badge-gn-hello')).toBeInTheDocument();
    expect(screen.queryByTestId('theme-badge-counter')).toBeNull();
  });

  it('exposes the data-active attribute on the active card so the brand border lights up', () => {
    render(<ThemeBrowser themes={FIXTURE_THEMES} activeSlug="counter" />);
    expect(screen.getByTestId('theme-card-counter')).toHaveAttribute(
      'data-active',
      'true',
    );
    expect(screen.getByTestId('theme-card-gn-hello')).not.toHaveAttribute(
      'data-active',
    );
  });

  it('moves the active state to the card the operator activates', () => {
    render(<ThemeBrowser themes={FIXTURE_THEMES} activeSlug="gn-hello" />);
    const activateCounter = screen.getByTestId('theme-activate-counter');
    fireEvent.click(activateCounter);
    expect(screen.getByTestId('theme-badge-counter')).toBeInTheDocument();
    expect(screen.queryByTestId('theme-badge-gn-hello')).toBeNull();
  });

  it('renders the brand headline with an italic-accent <em>', () => {
    render(<ThemeBrowser themes={FIXTURE_THEMES} activeSlug="gn-hello" />);
    const heading = screen.getByRole('heading', { level: 1 });
    expect(heading.textContent).toContain('Themes');
    expect(heading.textContent).toContain('appearance');
    expect(heading.querySelector('em')).not.toBeNull();
    expect(heading.querySelector('em')?.textContent).toBe('appearance');
  });

  it('renders *accented* description fragments as serif italic spans', () => {
    render(<ThemeBrowser themes={FIXTURE_THEMES} activeSlug="gn-hello" />);
    const card = screen.getByTestId('theme-card-gn-hello');
    const accent = card.querySelector('p em');
    expect(accent).not.toBeNull();
    expect(accent?.textContent).toBe('living');
  });

  it('renders an emerald Activate CTA on non-active cards', () => {
    render(<ThemeBrowser themes={FIXTURE_THEMES} activeSlug="gn-hello" />);
    const button = screen.getByTestId('theme-activate-counter');
    expect(button).toHaveAccessibleName('Activate Counter');
    expect(button.textContent).toMatch(/activate/i);
  });

  it('disables the Activate CTA on the active card', () => {
    render(<ThemeBrowser themes={FIXTURE_THEMES} activeSlug="counter" />);
    const button = screen.getByTestId('theme-activate-counter') as HTMLButtonElement;
    expect(button.disabled).toBe(true);
  });

  it('snapshots the gallery shape so visual structure is locked in', () => {
    const { asFragment } = render(
      <ThemeBrowser themes={FIXTURE_THEMES} activeSlug="gn-hello" />,
    );
    expect(asFragment()).toMatchSnapshot();
  });
});
