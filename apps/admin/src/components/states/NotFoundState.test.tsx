/**
 * NotFoundState — visual + interaction contract tests.
 *
 * 404 surfaces are *not* errors in the brand vocabulary — they're a
 * successful "no such page" outcome. These tests pin:
 *
 *   1. Defaults — "Not *found*." headline in page-scale Archivo with
 *      italic-accent `found` in emerald-deep (calm, not lavender —
 *      this isn't unexpected, it's intended).
 *   2. Default action — `<Link href="/">Back to safety</Link>` via
 *      the `asChild` Button variant.
 *   3. `onAction` callback — when supplied, the action renders a
 *      `<button>` and fires the callback on click.
 *   4. Custom title / body / eyebrow / href / actionLabel — all
 *      overrides take effect.
 *   5. role="status" (NOT "alert") because the user navigated here
 *      intentionally, even if by mistake.
 */
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { NotFoundState } from './NotFoundState';

describe('<NotFoundState>', () => {
  it('renders the default 404 surface with emerald italic-accent', () => {
    render(<NotFoundState />);

    const root = screen.getByTestId('not-found-state');
    // Status — not alert. 404 is not alarm.
    expect(root.getAttribute('role')).toBe('status');

    // Eyebrow micro-label.
    const eyebrow = screen.getByTestId('not-found-state-eyebrow');
    expect(eyebrow.textContent).toBe('404 · resource not found');
    expect(eyebrow.className).toContain('text-emerald-deep');
    expect(eyebrow.className).toContain('uppercase');

    // Title — page-scale Archivo, italic accent in emerald (not lavender,
    // because 404 is composed-not-alarmed).
    const title = screen.getByTestId('not-found-state-title');
    expect(title.tagName).toBe('H1');
    expect(title.className).toContain('font-display');
    expect(title.className).toContain('text-[44px]');
    expect(title.className).toContain('[&_em]:font-serif');
    expect(title.className).toContain('[&_em]:italic');
    expect(title.className).toContain('[&_em]:text-emerald-deep');
    expect(title.querySelector('em')?.textContent).toBe('found');

    // Body in fg-muted.
    const body = screen.getByTestId('not-found-state-body');
    expect(body.className).toContain('text-fg-muted');
    expect(body.textContent).toContain("couldn't find that page");

    // Default action is a link to "/" — `asChild` makes the Link
    // itself the rendered element (Slot pattern), so the testid
    // lands on the anchor.
    const action = screen.getByTestId('not-found-state-action');
    expect(action.tagName).toBe('A');
    expect(action.getAttribute('href')).toBe('/');
    expect(action.textContent).toContain('Back to safety');
  });

  it('accepts custom title, body, eyebrow, href and actionLabel', () => {
    render(
      <NotFoundState
        title={
          <>
            Page <em>missing</em>.
          </>
        }
        body="The post was deleted 30 days ago."
        eyebrow="404 · post"
        href="/posts"
        actionLabel="Back to posts"
      />,
    );
    expect(screen.getByTestId('not-found-state-title').textContent).toContain(
      'Page',
    );
    expect(
      screen.getByTestId('not-found-state-title').querySelector('em')?.textContent,
    ).toBe('missing');
    expect(screen.getByTestId('not-found-state-body').textContent).toContain(
      '30 days ago',
    );
    expect(screen.getByTestId('not-found-state-eyebrow').textContent).toBe(
      '404 · post',
    );
    const action = screen.getByTestId('not-found-state-action');
    expect(action.tagName).toBe('A');
    expect(action.getAttribute('href')).toBe('/posts');
    expect(action.textContent).toContain('Back to posts');
  });

  it('renders a callback button when onAction is provided (no Link)', () => {
    const handler = vi.fn();
    render(<NotFoundState onAction={handler} actionLabel="Reset filters" />);

    const action = screen.getByTestId('not-found-state-action');
    // Callback variant renders a real <button>, not a <Link>.
    expect(action.tagName).toBe('BUTTON');
    expect(action.textContent).toContain('Reset filters');
    fireEvent.click(action);
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it('suppresses the eyebrow when explicitly set to null', () => {
    render(<NotFoundState eyebrow={null} />);
    expect(
      screen.queryByTestId('not-found-state-eyebrow'),
    ).not.toBeInTheDocument();
  });

  it('passes through arbitrary HTML props', () => {
    render(<NotFoundState id="my-404" />);
    expect(screen.getByTestId('not-found-state').id).toBe('my-404');
  });
});
