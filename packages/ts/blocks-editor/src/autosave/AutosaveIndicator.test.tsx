/**
 * Tests for <AutosaveIndicator>.
 *
 * The indicator is purely a function of the AutosaveState the host
 * passes in. We pin `nowFn` so the relative-time formatting is
 * deterministic and assert on:
 *
 *   1. Status → dot colour + label mapping (idle / saving / saved / error).
 *   2. The "saved" label carries an Instrument Serif italic relative
 *      timestamp (Fig 01-style brand accent).
 *   3. Surface tokens (forest pill by default; cream when requested).
 *   4. Snapshot for the canonical "saved 12s ago" state.
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { AutosaveIndicator } from './AutosaveIndicator.tsx';
import type { AutosaveState } from './types.ts';

function state(over: Partial<AutosaveState> = {}): AutosaveState {
  return {
    status: 'idle',
    lastSavedAt: null,
    error: null,
    ...over,
  };
}

describe('<AutosaveIndicator>', () => {
  it('renders the idle/draft label when status is idle', () => {
    render(<AutosaveIndicator state={state()} />);

    const label = screen.getByTestId('autosave-indicator-label');
    expect(label).toHaveTextContent('Draft');
    expect(
      screen.getByTestId('autosave-indicator').getAttribute('data-status'),
    ).toBe('idle');
  });

  it('renders the saving label with a pulsing emerald dot', () => {
    render(<AutosaveIndicator state={state({ status: 'saving' })} />);

    expect(screen.getByTestId('autosave-indicator-label')).toHaveTextContent(
      'Saving',
    );
    const dot = screen.getByTestId('autosave-indicator-dot');
    expect(dot.getAttribute('style')).toMatch(/--emerald-bright/);
    expect(dot.getAttribute('style')).toMatch(/animation/);
  });

  it('renders "Saved" with an italic relative timestamp when saved', () => {
    const now = Date.parse('2026-05-22T12:00:00.000Z');
    const savedAt = new Date(now - 12_000); // 12s ago
    render(
      <AutosaveIndicator
        state={state({ status: 'saved', lastSavedAt: savedAt })}
        nowFn={() => now}
      />,
    );

    const stamp = screen.getByTestId('autosave-indicator-timestamp');
    expect(stamp).toHaveTextContent('12s ago');
    // The italic timestamp uses the Instrument Serif token, italic
    // style, and the +5% scale-up the brand prescribes.
    expect(stamp.getAttribute('style')).toMatch(/font-style: italic/);
    expect(stamp.getAttribute('style')).toMatch(/--font-serif/);
  });

  it('renders the danger dot + label when status is error', () => {
    render(
      <AutosaveIndicator state={state({ status: 'error', error: 'boom' })} />,
    );
    expect(screen.getByTestId('autosave-indicator-label')).toHaveTextContent(
      'Save failed',
    );
    expect(
      screen.getByTestId('autosave-indicator-dot').getAttribute('style'),
    ).toMatch(/--danger/);
    expect(
      screen.getByTestId('autosave-indicator').getAttribute('aria-label'),
    ).toBe('boom');
  });

  it('switches to the cream surface palette when requested', () => {
    render(
      <AutosaveIndicator state={state({ status: 'idle' })} surface="cream" />,
    );
    const pill = screen.getByTestId('autosave-indicator');
    expect(pill.getAttribute('data-surface')).toBe('cream');
    expect(pill.getAttribute('style')).toMatch(/--paper-2/);
  });

  it('matches the snapshot for the saved 12s state', () => {
    const now = Date.parse('2026-05-22T12:00:00.000Z');
    const savedAt = new Date(now - 12_000);
    const { container } = render(
      <AutosaveIndicator
        state={state({ status: 'saved', lastSavedAt: savedAt })}
        nowFn={() => now}
      />,
    );
    expect(container.firstChild).toMatchSnapshot();
  });
});
