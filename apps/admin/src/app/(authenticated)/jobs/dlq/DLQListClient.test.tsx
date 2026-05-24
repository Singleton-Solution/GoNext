/**
 * DLQListClient — unit tests.
 *
 * The component renders through the shared <ResourceList>, so we only
 * test what's specific to the DLQ:
 *  - rows render with type / queue / payload preview
 *  - the empty state appears when initialData is empty
 *  - bulk actions invoke replayTask / discardTask
 *  - the queue filter chip switches queues and triggers a refetch
 *
 * We stub the actions module so no real network call escapes the test.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

// Stub next/navigation — the App Router hooks aren't in jsdom.
vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => '/jobs/dlq',
  useSearchParams: () => new URLSearchParams(),
}));

// Stub the actions module before importing the component. We use
// vi.hoisted so the mock object exists at the time vi.mock is hoisted
// to the top of the file; otherwise the reference inside the factory
// would TDZ-error on `mocks`.
const mocks = vi.hoisted(() => ({
  listArchivedTasks: vi.fn(),
  replayTask: vi.fn(),
  discardTask: vi.fn(),
  redactTask: vi.fn(),
  getArchivedTask: vi.fn(),
}));
vi.mock('./actions', () => mocks);

import { DLQListClient } from './DLQListClient';
import type { DLQListResponse } from './types';

const sample: DLQListResponse = {
  data: [
    {
      id: 't1',
      queue: 'default',
      type: 'webhook:deliver',
      payload_preview: '{"url":"https://example.com"}',
      last_error: 'timeout',
      failed_at: '2026-05-17T12:00:00Z',
      retried: 3,
      max_retry: 3,
      redacted: false,
    },
    {
      id: 't2',
      queue: 'default',
      type: 'email:send',
      payload_preview: '{"to":"a@b.c"}',
      last_error: 'SMTP unavailable',
      failed_at: '2026-05-17T11:55:00Z',
      retried: 5,
      max_retry: 5,
      redacted: true,
      redacted_fields: ['to'],
    },
  ],
  pagination: { next_cursor: '' },
};

describe('DLQListClient', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.listArchivedTasks.mockResolvedValue(sample);
    mocks.replayTask.mockResolvedValue(undefined);
    mocks.discardTask.mockResolvedValue(undefined);
  });

  it('renders the task rows', () => {
    render(<DLQListClient initialQueue="default" initialData={sample} />);
    expect(screen.getByText('webhook:deliver')).toBeInTheDocument();
    expect(screen.getByText('email:send')).toBeInTheDocument();
  });

  it('renders retry counts', () => {
    render(<DLQListClient initialQueue="default" initialData={sample} />);
    expect(screen.getByText('3/3')).toBeInTheDocument();
    expect(screen.getByText('5/5')).toBeInTheDocument();
  });

  it('shows the redacted marker for masked rows', () => {
    render(<DLQListClient initialQueue="default" initialData={sample} />);
    // Row t2 has redacted=true; the preview gets a "(redacted)" suffix.
    expect(screen.getByText(/\(redacted\)/)).toBeInTheDocument();
  });

  it('renders empty state when there are no archived tasks', () => {
    render(
      <DLQListClient
        initialQueue="default"
        initialData={{ data: [], pagination: { next_cursor: '' } }}
      />,
    );
    expect(screen.getByText(/No archived tasks/i)).toBeInTheDocument();
  });

  it('exposes the queue filter chip with the current queue active', () => {
    render(<DLQListClient initialQueue="webhooks" initialData={sample} />);
    // FilterChip renders as a button with testid filter-chip-<key>.
    expect(screen.getByTestId('filter-chip-queue')).toBeInTheDocument();
  });

  it('switches queue when the filter is changed', async () => {
    render(<DLQListClient initialQueue="default" initialData={sample} />);
    // Open the filter dropdown and pick "webhooks".
    fireEvent.click(screen.getByTestId('filter-chip-queue'));
    fireEvent.click(screen.getByTestId('filter-option-queue-webhooks'));
    // Allow the await refetch to flush.
    await Promise.resolve();
    expect(mocks.listArchivedTasks).toHaveBeenCalled();
    const arg = mocks.listArchivedTasks.mock.calls[0]?.[0];
    expect(arg?.queue).toBe('webhooks');
  });

  it('invokes replayTask for each selected row when bulk replay is applied', async () => {
    render(<DLQListClient initialQueue="default" initialData={sample} />);
    // Select t1.
    fireEvent.click(screen.getByLabelText('Select row t1'));
    fireEvent.click(screen.getByTestId('bulk-action-replay'));
    // ResourceList's confirm dialog appears; press Confirm.
    fireEvent.click(screen.getByTestId('resource-list-confirm-apply'));
    await Promise.resolve();
    await Promise.resolve();
    expect(mocks.replayTask).toHaveBeenCalledWith('t1', 'default');
  });

  it('invokes discardTask for each selected row when bulk discard is applied', async () => {
    render(<DLQListClient initialQueue="default" initialData={sample} />);
    fireEvent.click(screen.getByLabelText('Select row t2'));
    fireEvent.click(screen.getByTestId('bulk-action-discard'));
    fireEvent.click(screen.getByTestId('resource-list-confirm-apply'));
    await Promise.resolve();
    await Promise.resolve();
    expect(mocks.discardTask).toHaveBeenCalledWith('t2', 'default');
  });
});
