/**
 * RedirectsListClient — brand-restyle contract tests.
 *
 * We don't snapshot the rendered pixels (jsdom doesn't apply the
 * Tailwind compiled CSS), so the contract here is the class /
 * data-attribute fragments that the styling system reads. If a
 * future refactor strips the brand tokens these assertions break
 * loudly enough to catch the regression.
 *
 * Three behaviours we pin:
 *   1. The three tabs — "All rules" / "Recent hits" / "Bulk import" —
 *      are present, in that order, and rendered as Radix tab triggers.
 *   2. A 301 row gets the emerald-soft Badge variant, a 307 row gets
 *      lavender-soft, the path columns are rendered with the mono
 *      font, and the regex switch slot is reflected as a chip.
 *   3. The Bulk-import tab content is the disabled paper-3 placeholder
 *      (CSV-upload backend lands in a follow-up).
 */
import { render, screen, fireEvent } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { RedirectsListClient } from './RedirectsListClient';
import type { Redirect, RedirectListResponse } from './types';

vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: vi.fn(), refresh: vi.fn() }),
}));

vi.mock('./actions', () => ({
  deleteRedirect: vi.fn().mockResolvedValue(undefined),
  listRedirects: vi.fn(),
  listTopRedirects: vi.fn(),
}));

const fixture = (overrides: Partial<Redirect> = {}): Redirect => ({
  id: overrides.id ?? 'r-1',
  source_path: overrides.source_path ?? '/legacy/path',
  destination_path: overrides.destination_path ?? '/new-home',
  status: overrides.status ?? 301,
  is_regex: overrides.is_regex ?? false,
  hit_count: overrides.hit_count ?? 42,
  last_hit_at: overrides.last_hit_at ?? new Date().toISOString(),
  created_at: overrides.created_at ?? new Date().toISOString(),
});

const initialData: RedirectListResponse = {
  data: [
    fixture({ id: 'a', status: 301 }),
    fixture({ id: 'b', status: 307, is_regex: true, source_path: '/blog/(.+)' }),
  ],
  pagination: { next_cursor: '' },
};

describe('<RedirectsListClient>', () => {
  it('renders the three brand tabs in order', () => {
    render(<RedirectsListClient initialData={initialData} />);

    const tablist = screen.getByTestId('redirects-tablist');
    const tabs = tablist.querySelectorAll('[role="tab"]');
    expect(tabs).toHaveLength(3);
    expect(tabs[0]?.textContent).toBe('All rules');
    expect(tabs[1]?.textContent).toBe('Recent hits');
    expect(tabs[2]?.textContent).toBe('Bulk import');
  });

  it('paints 301 rows with the emerald-soft Badge variant and mono paths', () => {
    render(<RedirectsListClient initialData={initialData} />);

    const row301 = screen.getByTestId('redirect-row-a');
    const emerald = row301.querySelector('.bg-emerald-soft');
    expect(emerald).not.toBeNull();
    expect(emerald?.textContent).toContain('301');

    // Source / destination both render through font-mono <code>.
    const codes = row301.querySelectorAll('code.font-mono');
    expect(codes.length).toBeGreaterThanOrEqual(2);
  });

  it('paints 307 rows with the lavender Badge variant + regex chip', () => {
    render(<RedirectsListClient initialData={initialData} />);

    const row307 = screen.getByTestId('redirect-row-b');
    const lavender = row307.querySelector('.bg-lavender-soft');
    expect(lavender).not.toBeNull();
    expect(lavender?.textContent).toContain('307');
    // The "regex" badge replaces the "literal" chip.
    expect(row307.textContent).toContain('regex');
  });

  it('shows the disabled CSV well when switching to Bulk import', async () => {
    render(<RedirectsListClient initialData={initialData} />);

    const importTab = screen.getByTestId('tab-import');
    // Radix Tab triggers respond to a pointer-down + pointer-up cycle
    // rather than a synthetic click in jsdom. Drive the activation by
    // firing both so the lazy panel content mounts.
    fireEvent.pointerDown(importTab, { pointerType: 'mouse', button: 0 });
    fireEvent.mouseDown(importTab, { button: 0 });
    fireEvent.pointerUp(importTab, { pointerType: 'mouse', button: 0 });
    fireEvent.click(importTab);

    const panel = await screen.findByTestId('bulk-import-panel');
    expect(panel.textContent).toContain('Bulk import from CSV');
    const button = panel.querySelector('button');
    expect(button?.disabled).toBe(true);
  });
});
