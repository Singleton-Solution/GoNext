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
    const active = screen.getByText(/^active$/i);
    const errored = screen.getByText(/^errored$/i);
    expect((active as HTMLElement).style.backgroundColor).toBeTruthy();
    expect((errored as HTMLElement).style.backgroundColor).toBeTruthy();
    expect((active as HTMLElement).style.backgroundColor).not.toBe(
      (errored as HTMLElement).style.backgroundColor,
    );
  });
});
