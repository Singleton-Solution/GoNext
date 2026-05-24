/**
 * DLQDetailClient — unit tests.
 *
 * Targets:
 *  - The detail view renders the task type, last error, and payload.
 *  - The replay button calls replayTask(id, queue) after confirmation.
 *  - The discard button calls discardTask(id, queue) after confirmation.
 *  - The Redact button opens RedactDialog with the parsed top-level
 *    fields from the payload.
 *  - Applying a redaction calls redactTask + refreshes the task.
 *  - Errors from action calls are surfaced inline.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

const pushMock = vi.fn();
vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: pushMock, replace: vi.fn() }),
  usePathname: () => '/jobs/dlq/t1',
  useSearchParams: () => new URLSearchParams(),
}));

const mocks = vi.hoisted(() => ({
  replayTask: vi.fn(),
  discardTask: vi.fn(),
  redactTask: vi.fn(),
  getArchivedTask: vi.fn(),
  listArchivedTasks: vi.fn(),
}));
vi.mock('../actions', () => mocks);

import { DLQDetailClient } from './DLQDetailClient';
import type { ArchivedTask } from '../types';

const sampleTask: ArchivedTask = {
  id: 't1',
  queue: 'default',
  type: 'webhook:deliver',
  payload_preview: '{"url":"https://example.com","token":"abcd"}',
  payload: '{"url":"https://example.com","token":"abcd"}',
  last_error: 'remote returned 500',
  failed_at: '2026-05-17T12:00:00Z',
  retried: 3,
  max_retry: 3,
  redacted: false,
};

describe('DLQDetailClient', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Default window.confirm to accept so action paths run end-to-end.
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    mocks.replayTask.mockResolvedValue(undefined);
    mocks.discardTask.mockResolvedValue(undefined);
    mocks.redactTask.mockResolvedValue(undefined);
    mocks.getArchivedTask.mockResolvedValue({
      ...sampleTask,
      payload: '{"url":"https://example.com","token":"***REDACTED***"}',
      redacted: true,
      redacted_fields: ['token'],
    });
  });

  it('renders the task type, error, and payload', () => {
    render(<DLQDetailClient initialTask={sampleTask} />);
    expect(screen.getByTestId('dlq-detail-type')).toHaveTextContent(
      'webhook:deliver',
    );
    expect(screen.getByText(/remote returned 500/)).toBeInTheDocument();
    const payload = screen.getByTestId('dlq-detail-payload');
    expect(payload.textContent).toContain('"url"');
  });

  it('replay button calls replayTask and navigates back', async () => {
    render(<DLQDetailClient initialTask={sampleTask} />);
    fireEvent.click(screen.getByTestId('dlq-detail-replay'));
    await waitFor(() =>
      expect(mocks.replayTask).toHaveBeenCalledWith('t1', 'default'),
    );
    expect(pushMock).toHaveBeenCalledWith('/jobs/dlq?queue=default');
  });

  it('discard button calls discardTask and navigates back', async () => {
    render(<DLQDetailClient initialTask={sampleTask} />);
    fireEvent.click(screen.getByTestId('dlq-detail-discard'));
    await waitFor(() =>
      expect(mocks.discardTask).toHaveBeenCalledWith('t1', 'default'),
    );
    expect(pushMock).toHaveBeenCalledWith('/jobs/dlq?queue=default');
  });

  it('replay does nothing when confirmation is declined', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(false);
    render(<DLQDetailClient initialTask={sampleTask} />);
    fireEvent.click(screen.getByTestId('dlq-detail-replay'));
    // Give any pending promises a chance to resolve before asserting.
    await Promise.resolve();
    expect(mocks.replayTask).not.toHaveBeenCalled();
  });

  it('redact button opens the dialog with parsed payload fields', () => {
    render(<DLQDetailClient initialTask={sampleTask} />);
    fireEvent.click(screen.getByTestId('dlq-detail-redact'));
    expect(screen.getByTestId('redact-dialog')).toBeInTheDocument();
    // The sample payload has url + token at the top level.
    expect(screen.getByTestId('redact-field-url')).toBeInTheDocument();
    expect(screen.getByTestId('redact-field-token')).toBeInTheDocument();
  });

  it('apply on the redact dialog calls redactTask + refreshes', async () => {
    render(<DLQDetailClient initialTask={sampleTask} />);
    fireEvent.click(screen.getByTestId('dlq-detail-redact'));
    fireEvent.click(screen.getByTestId('redact-field-token'));
    fireEvent.click(screen.getByTestId('redact-apply'));
    await waitFor(() =>
      expect(mocks.redactTask).toHaveBeenCalledWith('t1', {
        queue: 'default',
        fields: ['token'],
      }),
    );
    expect(mocks.getArchivedTask).toHaveBeenCalledWith('t1', 'default');
  });

  it('surfaces replay errors inline', async () => {
    mocks.replayTask.mockRejectedValueOnce(new Error('boom'));
    render(<DLQDetailClient initialTask={sampleTask} />);
    fireEvent.click(screen.getByTestId('dlq-detail-replay'));
    await waitFor(() =>
      expect(screen.getByTestId('dlq-detail-error')).toHaveTextContent('boom'),
    );
  });

  it('shows the redacted summary when the task carries a redaction', () => {
    const t: ArchivedTask = {
      ...sampleTask,
      redacted: true,
      redacted_fields: ['token'],
    };
    render(<DLQDetailClient initialTask={t} />);
    expect(screen.getByText(/Redacted/)).toBeInTheDocument();
    expect(screen.getByText(/fields:/)).toBeInTheDocument();
  });
});
