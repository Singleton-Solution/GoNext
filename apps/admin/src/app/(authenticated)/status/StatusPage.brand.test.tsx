/**
 * StatusPage — brand-application snapshot tests.
 *
 * The first regression target for this layer is "does the
 * Living-Systems brand vocabulary still reach the DOM?" — Headline
 * with the italic-accent rule, tone-tinted StatusCard surfaces,
 * mono "Refreshed Ns ago" badge, emerald Copy button. We pin three
 * narrow snapshots rather than a full-tree dump so a routine code
 * change doesn't churn a multi-hundred-line snapshot file.
 *
 * The non-brand contracts (data fetching, error banners, refresh
 * cadence) are covered by StatusPage.test.tsx; this file only
 * asserts on the visual chrome.
 */
import { render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const fetchStatusReportMock = vi.fn();
vi.mock('./api', () => ({
  fetchStatusReport: (signal?: AbortSignal) => fetchStatusReportMock(signal),
}));

import { StatusPage } from './StatusPage';
import type { StatusReport } from './types';

function makeReport(overrides: Partial<StatusReport> = {}): StatusReport {
  return {
    version: 'v1.2.3',
    commit: 'abc123',
    build_date: '2026-05-17T00:00:00Z',
    go_version: 'go1.25.0',
    os: 'linux',
    arch: 'amd64',
    generated: '2026-05-17T12:00:00Z',
    database: {
      ok: true,
      version: 'PostgreSQL 16.2',
      max_conns: 25,
      in_use: 4,
      idle: 21,
      response_time_ms: 1,
    },
    redis: { ok: true, version: '7.2.4', response_time_ms: 1 },
    migrations: { current_version: 42, dirty: false, total_count: 42 },
    queues: [
      { name: 'critical', pending: 2, active: 1, processed_24h: 100, failed_24h: 0 },
    ],
    theme: { active_name: 'gn-pro', version: 'v1', parts_count: 4, templates_count: 3 },
    plugins: { installed: 5, active: 4, errored: 0, last_install: '2026-05-10T00:00:00Z' },
    disk: { theme_dir_bytes: 12345, media_dir_bytes: 67890123 },
    ...overrides,
  };
}

describe('StatusPage brand', () => {
  beforeEach(() => {
    fetchStatusReportMock.mockReset();
  });

  it('renders the brand Headline with the italic <em>status</em> accent', async () => {
    fetchStatusReportMock.mockResolvedValueOnce(makeReport());
    render(<StatusPage />);
    await waitFor(() =>
      expect(screen.getByTestId('card-build')).toBeInTheDocument(),
    );

    const heading = screen.getByRole('heading', { level: 1 });
    // Outer tag is the Archivo display.
    expect(heading.className).toContain('font-display');
    expect(heading.className).toContain('font-extrabold');
    // Italic-accent rule is wired into the Headline className.
    expect(heading.className).toContain('[&_em]:font-serif');
    expect(heading.className).toContain('[&_em]:text-emerald-deep');
    // The accented word survives rendering.
    expect(heading.querySelector('em')?.textContent).toBe('status');
  });

  it('renders the mono "Refreshed Ns ago" auto-refresh badge', async () => {
    fetchStatusReportMock.mockResolvedValueOnce(makeReport());
    render(<StatusPage />);
    await waitFor(() =>
      expect(screen.getByTestId('card-build')).toBeInTheDocument(),
    );

    const chip = screen.getByTestId('status-refreshed-ago');
    expect(chip.className).toContain('font-mono');
    expect(chip.className).toContain('text-emerald-deep');
    // The italic-accent rule lives inside the chip on the time value.
    const em = chip.querySelector('em');
    expect(em).not.toBeNull();
    expect(em?.className).toContain('font-serif');
    expect(em?.className).toContain('italic');
  });

  it('paints StatusCard surfaces with brand tone tints', async () => {
    fetchStatusReportMock.mockResolvedValueOnce(makeReport());
    render(<StatusPage />);
    await waitFor(() =>
      expect(screen.getByTestId('card-database')).toBeInTheDocument(),
    );

    const ok = screen.getByTestId('card-database');
    // Healthy database paints emerald-soft.
    expect(ok.className).toContain('bg-emerald-soft');
    // The card carries its tone as a data attribute for cascading
    // styles downstream.
    expect(ok.getAttribute('data-tone')).toBe('ok');
  });
});
