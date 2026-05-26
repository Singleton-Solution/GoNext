/**
 * Snapshot pins for the brand-styled PluginStatusBadge.
 *
 * Each state renders with its canonical token palette (emerald-soft
 * for active, paper-3 for inactive, danger-soft for errored, etc.).
 * Locking the rendered tree means a future palette drift fails the
 * snapshot and forces a deliberate update.
 */
import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { PluginStatusBadge } from './PluginStatusBadge';
import type { PluginState } from '../types';

const STATES: PluginState[] = [
  'active',
  'inactive',
  'installed',
  'pending_uninstall',
  'errored',
];

describe('PluginStatusBadge snapshots', () => {
  for (const state of STATES) {
    it(`pins the brand-styled badge for state=${state}`, () => {
      const { asFragment } = render(<PluginStatusBadge state={state} />);
      expect(asFragment()).toMatchSnapshot();
    });
  }
});
