/**
 * Tests for `<AutosaveIndicator>` — the status pip.
 *
 * The contract under test:
 *  1. Status maps to a recognisable label (Idle / Saving / Saved / Save failed).
 *  2. The dot carries `data-status` so the editor-theme.css can colour it.
 *  3. The relative timestamp slot honours the design-mock buckets.
 *  4. Light / dark tones flip the `data-tone` marker.
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import {
  AutosaveIndicator,
  relativeTimestamp,
} from './AutosaveIndicator.tsx';
import type { AutosaveState } from './types.ts';

const fixedNow = new Date('2026-05-25T09:00:00Z');

function state(overrides: Partial<AutosaveState>): AutosaveState {
  return {
    status: 'idle',
    lastSavedAt: null,
    error: null,
    ...overrides,
  };
}

describe('<AutosaveIndicator>', () => {
  it('renders the Idle label when there is no save state', () => {
    render(
      <AutosaveIndicator
        state={state({ status: 'idle' })}
        refreshIntervalMs={0}
        now={() => fixedNow}
      />,
    );
    expect(screen.getByTestId('autosave-indicator-label')).toHaveTextContent(
      'Idle',
    );
  });

  it('flips data-status to "saving" while a save is in flight', () => {
    render(
      <AutosaveIndicator
        state={state({ status: 'saving' })}
        refreshIntervalMs={0}
        now={() => fixedNow}
      />,
    );
    expect(
      screen.getByTestId('autosave-indicator').getAttribute('data-status'),
    ).toBe('saving');
    expect(screen.getByTestId('autosave-indicator-label')).toHaveTextContent(
      'Saving',
    );
  });

  it('renders a relative timestamp when lastSavedAt is set', () => {
    const last = new Date(fixedNow.getTime() - 12_000);
    render(
      <AutosaveIndicator
        state={state({ status: 'saved', lastSavedAt: last })}
        refreshIntervalMs={0}
        now={() => fixedNow}
      />,
    );
    expect(screen.getByTestId('autosave-indicator-time')).toHaveTextContent(
      '12s ago',
    );
  });

  it('renders "Save failed" with data-status="error"', () => {
    render(
      <AutosaveIndicator
        state={state({ status: 'error', error: 'boom' })}
        refreshIntervalMs={0}
        now={() => fixedNow}
      />,
    );
    expect(
      screen.getByTestId('autosave-indicator').getAttribute('data-status'),
    ).toBe('error');
    expect(screen.getByTestId('autosave-indicator-label')).toHaveTextContent(
      'Save failed',
    );
  });

  it('honors the tone prop (dark for the forest topbar)', () => {
    render(
      <AutosaveIndicator
        state={state({})}
        tone="dark"
        refreshIntervalMs={0}
        now={() => fixedNow}
      />,
    );
    expect(
      screen.getByTestId('autosave-indicator').getAttribute('data-tone'),
    ).toBe('dark');
  });

  it('exposes the BEM hooks the editor-theme stylesheet reads', () => {
    render(
      <AutosaveIndicator
        state={state({ status: 'saved', lastSavedAt: fixedNow })}
        refreshIntervalMs={0}
        now={() => fixedNow}
      />,
    );
    const root = screen.getByTestId('autosave-indicator');
    expect(root.className).toContain('gonext-autosave-indicator');
    expect(screen.getByTestId('autosave-indicator-dot').className).toBe(
      'gonext-autosave-indicator__dot',
    );
  });
});

describe('relativeTimestamp', () => {
  const now = new Date('2026-05-25T09:00:00Z');
  it('returns null when last is null', () => {
    expect(relativeTimestamp(null, now)).toBeNull();
  });
  it('renders "just now" inside the 5s window', () => {
    expect(
      relativeTimestamp(new Date(now.getTime() - 3_000), now),
    ).toBe('just now');
  });
  it('renders "Ns ago" in the seconds bucket', () => {
    expect(
      relativeTimestamp(new Date(now.getTime() - 12_000), now),
    ).toBe('12s ago');
  });
  it('renders "Nm ago" in the minutes bucket', () => {
    expect(
      relativeTimestamp(new Date(now.getTime() - 9 * 60_000), now),
    ).toBe('9m ago');
  });
  it('renders "Nh ago" in the hours bucket', () => {
    expect(
      relativeTimestamp(new Date(now.getTime() - 4 * 3_600_000), now),
    ).toBe('4h ago');
  });
  it('renders an ISO date for older saves', () => {
    expect(
      relativeTimestamp(new Date('2026-05-20T09:00:00Z'), now),
    ).toBe('2026-05-20');
  });
});
