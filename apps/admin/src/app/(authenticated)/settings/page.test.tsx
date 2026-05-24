/**
 * Settings overview page test.
 *
 * Asserts the overview renders the four canonical group cards (General,
 * Reading, Writing, Permalinks) each linking to its sub-route. Mirrors the
 * existing Sidebar test idiom: `next/link` is React-rendered fine in jsdom,
 * but the Pathname hook isn't needed here since the overview is a server
 * component with no navigation state.
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';

import SettingsOverviewPage from './page';

describe('SettingsOverviewPage', () => {
  it('renders four cards linking to each settings group', () => {
    render(<SettingsOverviewPage />);

    const expected: Array<[label: string, href: string]> = [
      ['General', '/settings/general'],
      ['Reading', '/settings/reading'],
      ['Writing', '/settings/writing'],
      ['Permalinks', '/settings/permalinks'],
    ];

    for (const [label, href] of expected) {
      const link = screen.getByRole('link', { name: new RegExp(`${label} settings`, 'i') });
      expect(link).toBeInTheDocument();
      expect(link).toHaveAttribute('href', href);
    }
  });

  it('exposes exactly four cards in the grid', () => {
    render(<SettingsOverviewPage />);
    const grid = screen.getByTestId('settings-overview-grid');
    const cards = grid.querySelectorAll('a');
    expect(cards).toHaveLength(4);
  });
});
