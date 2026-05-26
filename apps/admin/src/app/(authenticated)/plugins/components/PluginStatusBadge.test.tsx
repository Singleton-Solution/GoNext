/**
 * PluginStatusBadge tests.
 *
 * Pins the badge's label + data-state attribute for each lifecycle
 * state, and asserts the styles diverge so the operator sees
 * different colour treatments for the different states.
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';

import { PluginStatusBadge } from './PluginStatusBadge';

describe('PluginStatusBadge', () => {
  it.each([
    ['active', /active/i],
    ['inactive', /inactive/i],
    ['installed', /installed/i],
    ['errored', /errored/i],
    ['pending_uninstall', /uninstalling/i],
  ] as const)(
    'renders the canonical label for state=%s',
    (state, labelPattern) => {
      render(<PluginStatusBadge state={state} />);
      const badge = screen.getByLabelText(/status:/i);
      expect(badge).toHaveAttribute('data-state', state);
      expect(badge).toHaveTextContent(labelPattern);
    },
  );

  it('renders different background colours for errored vs active', () => {
    render(
      <div>
        <PluginStatusBadge state="active" />
        <PluginStatusBadge state="errored" />
      </div>,
    );
    // Brand tokens land as CSS variables, which jsdom's style parser
    // can't resolve to a concrete colour. Assert the per-state styling
    // diverges via the (unparsed) `background` shorthand string and
    // the data-state attribute the badge carries.
    const active = screen
      .getByLabelText(/status: active/i) as HTMLElement;
    const errored = screen
      .getByLabelText(/status: errored/i) as HTMLElement;
    expect(active.dataset.state).toBe('active');
    expect(errored.dataset.state).toBe('errored');
    expect(active.style.background).toBeTruthy();
    expect(errored.style.background).toBeTruthy();
    expect(active.style.background).not.toBe(errored.style.background);
  });
});
