/**
 * EmptyState — visual + behavioural contract tests.
 *
 * The empty state is shared by every list in the admin (and a few in
 * the public-facing surfaces). It's a "calm, calibrated, never
 * alarming" brand surface — these tests pin the rules so a refactor
 * can't quietly regress them.
 *
 * What we assert:
 *
 *   1. Default variant — renders Archivo display headline with the
 *      Instrument-Serif italic accent in --emerald-deep, plus the
 *      "living rings" decoration around the emerald-soft icon tile.
 *   2. Search variant — icon tile flips to neutral paper-3 / fg-muted
 *      because the "search returned nothing" moment is different in
 *      mood from a first-run "go for it" moment.
 *   3. Action region — renders only when `action` is non-null; the
 *      flex container has the brand-correct 8px gap.
 *   4. Title accepts JSX so the italic accent rule continues to work
 *      — string-only titles still render but lose the accent.
 *
 * We match on class-string fragments and data-attributes (the
 * Tailwind classes don't actually paint pixels in jsdom). That's the
 * closest faithful proxy to "what the browser will render".
 */
import { render, screen } from '@testing-library/react';
import { PenLine, SearchX } from 'lucide-react';
import { describe, expect, it } from 'vitest';

import { Button } from '@/components/ui/button';

import { EmptyState } from './EmptyState';

describe('<EmptyState>', () => {
  it('renders the default (emerald) variant with italic-accent title', () => {
    render(
      <EmptyState
        icon={PenLine}
        title={
          <>
            Write your <em>first</em> post.
          </>
        }
        body="An empty page is the best one."
        action={
          <Button variant="primary" data-testid="cta">
            New post
          </Button>
        }
      />,
    );

    const root = screen.getByTestId('empty-state');
    // Cream-paper card shell.
    expect(root.className).toContain('bg-paper-2');
    expect(root.className).toContain('border-border');
    // Default variant is signalled via data-variant for snapshot
    // tooling (and a fallback CSS hook if Tailwind isn't loaded).
    expect(root.getAttribute('data-variant')).toBe('default');
    // role="status" so AT users hear empty states politely.
    expect(root.getAttribute('role')).toBe('status');

    // Title — italic-accent classes are in the rendered className so
    // <em>first</em> picks up the serif treatment.
    const title = screen.getByTestId('empty-state-title');
    expect(title.tagName).toBe('H3');
    expect(title.className).toContain('font-display');
    expect(title.className).toContain('font-extrabold');
    expect(title.className).toContain('[&_em]:font-serif');
    expect(title.className).toContain('[&_em]:italic');
    expect(title.className).toContain('[&_em]:text-emerald-deep');
    // The accent word survives the render.
    expect(title.querySelector('em')?.textContent).toBe('first');

    // Body in --fg-muted, ~1.55 leading.
    const body = screen.getByTestId('empty-state-body');
    expect(body.className).toContain('text-fg-muted');
    expect(body.className).toContain('leading-[1.55]');

    // Action region exists with 8px gap.
    const actions = screen.getByTestId('empty-state-actions');
    expect(actions.className).toContain('gap-2');
    expect(screen.getByTestId('cta')).toBeInTheDocument();
  });

  it('renders the search variant with the neutral paper-3 tile', () => {
    render(
      <EmptyState
        variant="search"
        icon={SearchX}
        title={
          <>
            Nothing matched <em>pour-over</em>.
          </>
        }
        body="Try a shorter query."
      />,
    );

    const root = screen.getByTestId('empty-state');
    expect(root.getAttribute('data-variant')).toBe('search');

    // The icon tile lives a few elements deep — search the DOM for
    // either the emerald-soft (must NOT be present) or paper-3 (must
    // be present) classes.
    const tiles = root.querySelectorAll('.bg-paper-3, .bg-emerald-soft');
    const tileClasses = Array.from(tiles).map((t) => t.className);
    expect(tileClasses.some((c) => c.includes('bg-paper-3'))).toBe(true);
    expect(tileClasses.some((c) => c.includes('bg-emerald-soft'))).toBe(false);
  });

  it('omits the action region when no action prop is passed', () => {
    render(
      <EmptyState
        icon={PenLine}
        title="Plain"
        body="Nothing here."
      />,
    );
    // No action region rendered when prop is absent.
    expect(screen.queryByTestId('empty-state-actions')).not.toBeInTheDocument();
  });

  it('accepts a plain-string title without an italic accent', () => {
    render(<EmptyState icon={PenLine} title="Plain title" body="Plain body." />);
    const title = screen.getByTestId('empty-state-title');
    expect(title.querySelector('em')).toBeNull();
    expect(title.textContent).toBe('Plain title');
    // The italic-accent classes are still in the className (they're
    // descendant selectors — they only apply when an <em> is present).
    expect(title.className).toContain('[&_em]:font-serif');
  });

  it('renders the "living rings" decoration as two aria-hidden squares', () => {
    render(<EmptyState icon={PenLine} title="t" body="b" />);
    const root = screen.getByTestId('empty-state');
    // Two `aria-hidden` decorative spans live inside the icon
    // container — the inner ring (-inset-2) and outer ring (-inset-4).
    const decorations = root.querySelectorAll('span[aria-hidden="true"]');
    // Two rings + the icon's own visual wrapper might also be
    // aria-hidden depending on Lucide's SVG — the constraint we care
    // about is "at least two decoration spans with the inset classes".
    const rings = Array.from(decorations).filter((d) =>
      d.className.includes('-inset-'),
    );
    expect(rings.length).toBe(2);
    expect(rings[0]?.className).toContain('-inset-2');
    expect(rings[1]?.className).toContain('-inset-4');
  });

  it('forwards arbitrary HTML props through to the root element', () => {
    render(
      <EmptyState
        icon={PenLine}
        title="t"
        body="b"
        aria-label="empty"
        id="my-empty"
      />,
    );
    const root = screen.getByTestId('empty-state');
    expect(root.getAttribute('aria-label')).toBe('empty');
    expect(root.id).toBe('my-empty');
  });
});
