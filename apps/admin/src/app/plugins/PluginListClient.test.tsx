/**
 * PluginListClient tests.
 *
 * Issue acceptance criteria coverage:
 *  - List page renders all plugins in the fetched data
 *  - Uninstall button: confirmation dialog before action fires
 *  - Disabled actions: can't uninstall an active plugin without
 *    deactivating first; cancel button on the confirm modal closes
 *    without calling the server action
 *
 * The server actions are mocked at the module boundary so we don't
 * touch real network. `next/navigation`'s `useRouter` is stubbed with
 * a deterministic spy, matching the pattern in PostListClient.test.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { act, fireEvent, render, screen, within } from '@testing-library/react';

const refreshSpy = vi.fn();
const activateSpy = vi.fn(async (_name: string) => ({ ok: true as const }));
const deactivateSpy = vi.fn(async (_name: string) => ({ ok: true as const }));
const uninstallSpy = vi.fn(async (_name: string) => ({ ok: true as const }));

vi.mock('next/navigation', () => ({
  useRouter: () => ({
    refresh: refreshSpy,
    push: vi.fn(),
    replace: vi.fn(),
    prefetch: vi.fn(),
  }),
}));

vi.mock('./actions', () => ({
  installPlugin: vi.fn(),
  activatePlugin: (name: string) => activateSpy(name),
  deactivatePlugin: (name: string) => deactivateSpy(name),
  uninstallPlugin: (name: string) => uninstallSpy(name),
}));

import { PluginListClient } from './PluginListClient';
import type { PluginRecord } from './types';

const SAMPLE: PluginRecord[] = [
  {
    name: 'akismet-clone',
    displayName: 'Akismet Clone',
    version: '1.2.0',
    state: 'active',
    installedAt: '2026-04-01T10:00:00Z',
    activatedAt: '2026-04-02T10:00:00Z',
    capabilities: ['posts.read', 'http.fetch'],
    lastError: null,
  },
  {
    name: 'seo-helper',
    displayName: 'SEO Helper',
    version: '0.4.1',
    state: 'inactive',
    installedAt: '2026-04-05T10:00:00Z',
    activatedAt: null,
    capabilities: ['posts.read', 'posts.write'],
    lastError: null,
  },
  {
    name: 'broken-plugin',
    displayName: 'Broken',
    version: '0.1.0',
    state: 'errored',
    installedAt: '2026-04-10T10:00:00Z',
    activatedAt: null,
    capabilities: [],
    lastError: { message: 'WASM module failed to load', at: '2026-04-10T11:00:00Z' },
  },
];

describe('PluginListClient', () => {
  beforeEach(() => {
    refreshSpy.mockReset();
    activateSpy.mockClear();
    deactivateSpy.mockClear();
    uninstallSpy.mockClear();
  });

  it('renders one row per plugin from the fetched data', () => {
    render(<PluginListClient plugins={SAMPLE} />);
    // 1 header + 3 body rows
    const rows = screen.getAllByRole('row');
    expect(rows).toHaveLength(1 + SAMPLE.length);
    expect(screen.getByRole('link', { name: /akismet clone/i })).toHaveAttribute(
      'href',
      '/plugins/akismet-clone',
    );
    expect(screen.getByRole('link', { name: /seo helper/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /^broken$/i })).toBeInTheDocument();
  });

  it('shows the empty state when no plugins are installed', () => {
    render(<PluginListClient plugins={[]} />);
    expect(screen.getByText(/no plugins installed yet/i)).toBeInTheDocument();
  });

  it('renders the activate button for inactive plugins and not for active ones', () => {
    render(<PluginListClient plugins={SAMPLE} />);
    expect(
      screen.getByRole('button', { name: /^activate seo-helper$/i }),
    ).toBeInTheDocument();
    // Active plugins don't get an activate button — they get deactivate.
    expect(
      screen.queryByRole('button', { name: /^activate akismet-clone$/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: /^deactivate akismet-clone$/i }),
    ).toBeInTheDocument();
  });

  it('disables the uninstall button for an active plugin until it is deactivated', () => {
    render(<PluginListClient plugins={SAMPLE} />);
    const activeRow = screen.getByRole('row', {
      name: /akismet clone/i,
    });
    const uninstallBtn = within(activeRow).getByRole('button', {
      name: /uninstall akismet-clone/i,
    });
    expect(uninstallBtn).toBeDisabled();
    expect(uninstallBtn).toHaveAttribute(
      'title',
      'Deactivate the plugin before uninstalling',
    );
  });

  it('opens a confirmation dialog before uninstalling, and cancel does not call the server action', () => {
    render(<PluginListClient plugins={SAMPLE} />);
    const inactiveRow = screen.getByRole('row', { name: /seo helper/i });
    const uninstallBtn = within(inactiveRow).getByRole('button', {
      name: /uninstall seo-helper/i,
    });
    fireEvent.click(uninstallBtn);

    // Dialog appears
    const dialog = screen.getByRole('dialog');
    expect(dialog).toBeInTheDocument();
    expect(
      within(dialog).getByText(/uninstall "seo-helper"\?/i),
    ).toBeInTheDocument();

    // Cancel closes without firing the action
    fireEvent.click(within(dialog).getByRole('button', { name: /cancel/i }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(uninstallSpy).not.toHaveBeenCalled();
  });

  it('confirms the uninstall and calls the server action', async () => {
    render(<PluginListClient plugins={SAMPLE} />);
    const inactiveRow = screen.getByRole('row', { name: /seo helper/i });
    fireEvent.click(
      within(inactiveRow).getByRole('button', { name: /uninstall seo-helper/i }),
    );
    const dialog = screen.getByRole('dialog');
    await act(async () => {
      fireEvent.click(
        within(dialog).getByRole('button', {
          name: /confirm uninstall seo-helper/i,
        }),
      );
    });
    expect(uninstallSpy).toHaveBeenCalledWith('seo-helper');
  });

  it('lets an errored plugin be uninstalled (after confirmation)', () => {
    render(<PluginListClient plugins={SAMPLE} />);
    const btn = screen.getByRole('button', {
      name: /^uninstall broken-plugin$/i,
    });
    expect(btn).not.toBeDisabled();
    fireEvent.click(btn);
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  it('filters plugins by status', () => {
    render(<PluginListClient plugins={SAMPLE} />);
    const inactiveChip = screen.getByRole('button', { name: /^inactive$/i });
    fireEvent.click(inactiveChip);
    expect(screen.getByRole('link', { name: /seo helper/i })).toBeInTheDocument();
    expect(
      screen.queryByRole('link', { name: /akismet clone/i }),
    ).not.toBeInTheDocument();
  });

  it('searches plugins by name', () => {
    render(<PluginListClient plugins={SAMPLE} />);
    const search = screen.getByLabelText(/search plugins/i);
    fireEvent.change(search, { target: { value: 'seo' } });
    expect(screen.getByRole('link', { name: /seo helper/i })).toBeInTheDocument();
    expect(
      screen.queryByRole('link', { name: /akismet clone/i }),
    ).not.toBeInTheDocument();
  });
});
