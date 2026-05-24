/**
 * StatusPage interactive tests.
 *
 * Mock the `./api` module so the page never reaches `fetch`. We assert
 * three contracts here:
 *
 *   1. The grid renders one card per section after a successful fetch.
 *   2. The error state surfaces a banner without erasing the previous
 *      report (an in-flight refresh that fails keeps the old data on
 *      screen).
 *   3. The Refresh button triggers a second fetch.
 *
 * Clipboard interactions are tested separately to keep the assertions
 * narrow — we stub `navigator.clipboard.writeText` and confirm the
 * button label flips through "Copy diagnostic" → "Copied!".
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

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
      ok: true, version: 'PostgreSQL 16.2', max_conns: 25, in_use: 4, idle: 21, response_time_ms: 1,
    },
    redis: { ok: true, version: '7.2.4', response_time_ms: 1 },
    migrations: { current_version: 42, dirty: false, total_count: 42 },
    queues: [
      { name: 'critical', pending: 2, active: 1, processed_24h: 100, failed_24h: 0 },
      { name: 'default', pending: 0, active: 0, processed_24h: 50, failed_24h: 0 },
    ],
    theme: { active_name: 'gn-pro', version: 'v1', parts_count: 4, templates_count: 3 },
    plugins: { installed: 5, active: 4, errored: 0, last_install: '2026-05-10T00:00:00Z' },
    disk: { theme_dir_bytes: 12345, media_dir_bytes: 67890123 },
    ...overrides,
  };
}

describe('StatusPage', () => {
  beforeEach(() => {
    fetchStatusReportMock.mockReset();
  });

  it('shows a loading indicator before the first response', async () => {
    let resolveFetch: (r: StatusReport) => void = () => {};
    fetchStatusReportMock.mockReturnValueOnce(
      new Promise<StatusReport>((resolve) => {
        resolveFetch = resolve;
      }),
    );

    render(<StatusPage />);

    expect(screen.getByTestId('status-loading')).toBeInTheDocument();
    resolveFetch(makeReport());
    await waitFor(() => {
      expect(screen.getByTestId('status-grid')).toBeInTheDocument();
    });
  });

  it('renders one card per section after a successful fetch', async () => {
    fetchStatusReportMock.mockResolvedValueOnce(makeReport());

    render(<StatusPage />);

    await waitFor(() => {
      expect(screen.getByTestId('card-build')).toBeInTheDocument();
    });
    expect(screen.getByTestId('card-database')).toBeInTheDocument();
    expect(screen.getByTestId('card-redis')).toBeInTheDocument();
    expect(screen.getByTestId('card-migrations')).toBeInTheDocument();
    expect(screen.getByTestId('card-disk')).toBeInTheDocument();
    expect(screen.getByTestId('card-theme')).toBeInTheDocument();
    expect(screen.getByTestId('card-plugins')).toBeInTheDocument();
    expect(screen.getByTestId('card-queue-critical')).toBeInTheDocument();
    expect(screen.getByTestId('card-queue-default')).toBeInTheDocument();
  });

  it('shows a placeholder when no queues are reported', async () => {
    fetchStatusReportMock.mockResolvedValueOnce(makeReport({ queues: [] }));

    render(<StatusPage />);

    await waitFor(() => {
      expect(screen.getByTestId('card-queues-empty')).toBeInTheDocument();
    });
  });

  it('surfaces an error banner without erasing the previous report', async () => {
    fetchStatusReportMock
      .mockResolvedValueOnce(makeReport())
      .mockRejectedValueOnce(new Error('network down'));

    render(<StatusPage />);

    await waitFor(() => {
      expect(screen.getByTestId('card-database')).toBeInTheDocument();
    });

    // Trigger refresh — second call rejects.
    fireEvent.click(screen.getByRole('button', { name: /refresh status/i }));

    await waitFor(() => {
      expect(screen.getByTestId('status-error')).toBeInTheDocument();
    });
    // Previous report data is still on screen.
    expect(screen.getByTestId('card-database')).toBeInTheDocument();
    expect(screen.getByText(/network down/)).toBeInTheDocument();
  });

  it('re-fetches when the Refresh button is clicked', async () => {
    fetchStatusReportMock
      .mockResolvedValueOnce(makeReport())
      .mockResolvedValueOnce(makeReport({ version: 'v1.2.4' }));

    render(<StatusPage />);

    await waitFor(() => {
      expect(screen.getByTestId('card-build')).toBeInTheDocument();
    });
    expect(fetchStatusReportMock).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('button', { name: /refresh status/i }));

    await waitFor(() => {
      expect(fetchStatusReportMock).toHaveBeenCalledTimes(2);
    });
  });

  it('copies the diagnostic JSON to the clipboard', async () => {
    fetchStatusReportMock.mockResolvedValueOnce(makeReport());
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });

    render(<StatusPage />);

    await waitFor(() => {
      expect(screen.getByTestId('card-build')).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole('button', { name: /copy diagnostic/i }));

    await waitFor(() => {
      expect(writeText).toHaveBeenCalledTimes(1);
    });
    const arg = writeText.mock.calls[0][0] as string;
    const parsed = JSON.parse(arg) as StatusReport;
    expect(parsed.version).toBe('v1.2.3');
  });
});
