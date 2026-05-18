/**
 * PluginDetailView tests.
 *
 * Issue acceptance criteria coverage:
 *  - Detail page shows capabilities formatted as human-readable text
 *  - Dependency status renders with satisfied / unsatisfied chips
 *  - Last-error block shows when the plugin is in `errored` state
 */
import { describe, expect, it } from 'vitest';
import { render, screen, within } from '@testing-library/react';

import { PluginDetailView } from './PluginDetailView';
import type { PluginRecord } from '../types';

const SAMPLE: PluginRecord = {
  name: 'mailchimp-sync',
  displayName: 'Mailchimp Sync',
  version: '2.3.1',
  state: 'active',
  installedAt: '2026-04-01T10:00:00Z',
  activatedAt: '2026-04-02T10:00:00Z',
  capabilities: ['posts.read', 'email.send', 'http.fetch'],
  lastError: null,
  manifest: {
    apiVersion: 'gonext.io/v1',
    name: 'mailchimp-sync',
    version: '2.3.1',
    displayName: 'Mailchimp Sync',
    description: 'Syncs published posts to Mailchimp campaigns.',
    author: 'Acme Co.',
    homepage: 'https://example.com/mailchimp-sync',
    entry: 'main.wasm',
    capabilities: ['posts.read', 'email.send', 'http.fetch'],
    depends: [{ name: 'core-utils', version: '^1.0.0' }],
    requires: { abi: 1 },
  },
  manifestRaw: '{"name":"mailchimp-sync"}',
  dependenciesStatus: [
    {
      name: 'core-utils',
      version: '^1.0.0',
      satisfied: true,
      state: 'active',
      resolvedVersion: '1.2.3',
    },
  ],
};

describe('PluginDetailView', () => {
  it('renders capabilities as human-readable text in declaration order', () => {
    render(<PluginDetailView plugin={SAMPLE} />);
    const list = screen.getByTestId('detail-capability-list');
    const items = within(list).getAllByRole('listitem');
    expect(items).toHaveLength(3);
    // Verbatim wording from the local capability registry.
    expect(items[0]).toHaveTextContent('Read all posts on this site.');
    expect(items[1]).toHaveTextContent(
      'Send transactional email on behalf of this site.',
    );
    expect(items[2]).toHaveTextContent(
      'Make outbound HTTP requests to any URL — including third-party services.',
    );
  });

  it('flags sensitive capabilities with a visible Sensitive marker', () => {
    render(<PluginDetailView plugin={SAMPLE} />);
    const list = screen.getByTestId('detail-capability-list');
    const sensitiveMarkers = within(list).getAllByText(/sensitive/i);
    // email.send and http.fetch are both sensitive.
    expect(sensitiveMarkers.length).toBeGreaterThanOrEqual(2);
  });

  it('renders dependency status with the satisfied chip', () => {
    render(<PluginDetailView plugin={SAMPLE} />);
    const deps = screen.getByTestId('detail-dependency-list');
    const row = within(deps).getByText(/core-utils/i).closest('li')!;
    expect(row).toHaveAttribute('data-dep-satisfied', 'true');
    expect(within(row).getByText(/satisfied/i)).toBeInTheDocument();
  });

  it('renders an unsatisfied dependency with a warning chip', () => {
    const plugin: PluginRecord = {
      ...SAMPLE,
      dependenciesStatus: [
        {
          name: 'core-utils',
          version: '^1.0.0',
          satisfied: false,
          state: null,
          resolvedVersion: null,
          reason: 'not installed',
        },
      ],
    };
    render(<PluginDetailView plugin={plugin} />);
    const deps = screen.getByTestId('detail-dependency-list');
    const row = within(deps).getByText(/core-utils/i).closest('li')!;
    expect(row).toHaveAttribute('data-dep-satisfied', 'false');
    expect(within(row).getByText(/unsatisfied/i)).toBeInTheDocument();
    // The reason text appears alongside the "not installed" status string.
    expect(within(row).getAllByText(/not installed/i).length).toBeGreaterThan(0);
  });

  it('shows the last-error block for errored plugins', () => {
    const errored: PluginRecord = {
      ...SAMPLE,
      state: 'errored',
      lastError: {
        message: 'WASM module failed to load: invalid magic number',
        at: '2026-05-01T12:00:00Z',
      },
    };
    render(<PluginDetailView plugin={errored} />);
    const block = screen.getByRole('alert');
    expect(block).toHaveTextContent(/wasm module failed to load/i);
    expect(block).toHaveTextContent(/invalid magic number/i);
  });

  it('renders empty state when the plugin has no capabilities', () => {
    const plain: PluginRecord = {
      ...SAMPLE,
      capabilities: [],
      manifest: { ...SAMPLE.manifest!, capabilities: [] },
    };
    render(<PluginDetailView plugin={plain} />);
    expect(
      screen.getByText(/doesn’t request any special permissions/i),
    ).toBeInTheDocument();
  });
});
