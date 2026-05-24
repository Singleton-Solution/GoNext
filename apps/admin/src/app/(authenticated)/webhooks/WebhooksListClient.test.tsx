/**
 * WebhooksListClient — unit tests.
 *
 * The list view is a thin shell over the actions module, so we stub
 * the actions to assert that Test/Disable/Delete fire the right
 * endpoint with the right id.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

// Stub next/navigation — the App Router hooks aren't in jsdom.
vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => '/webhooks',
  useSearchParams: () => new URLSearchParams(),
}));

const mocks = vi.hoisted(() => ({
  listSubscriptions: vi.fn(),
  testSubscription: vi.fn(),
  disableSubscription: vi.fn(),
  enableSubscription: vi.fn(),
  deleteSubscription: vi.fn(),
  createSubscription: vi.fn(),
  updateSubscription: vi.fn(),
  listDeliveries: vi.fn(),
  listEventCatalog: vi.fn(),
}));
vi.mock('./actions', () => mocks);

import { WebhooksListClient } from './WebhooksListClient';
import type { SubscriptionListResponse } from './types';

const sample: SubscriptionListResponse = {
  data: [
    {
      id: 'sub-1',
      name: 'orders',
      url: 'https://example.com/orders',
      events: ['post.published'],
      active: true,
      created_at: '2026-05-19T12:00:00Z',
      updated_at: '2026-05-19T12:00:00Z',
      consecutive_failures: 0,
      last_delivery_status: 'success',
      last_delivery_at: '2026-05-19T11:00:00Z',
    },
    {
      id: 'sub-2',
      name: 'analytics',
      url: 'https://example.com/analytics',
      events: ['user.created'],
      active: false,
      created_at: '2026-05-18T12:00:00Z',
      updated_at: '2026-05-18T12:00:00Z',
      consecutive_failures: 3,
      last_delivery_status: 'failed',
    },
  ],
  pagination: { next_cursor: '' },
};

describe('WebhooksListClient', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.testSubscription.mockResolvedValue({
      delivered: true,
      response_code: 200,
      duration_ms: 42,
    });
    mocks.disableSubscription.mockImplementation(async (id: string) => ({
      ...sample.data.find((s) => s.id === id)!,
      active: false,
    }));
    mocks.enableSubscription.mockImplementation(async (id: string) => ({
      ...sample.data.find((s) => s.id === id)!,
      active: true,
    }));
    mocks.deleteSubscription.mockResolvedValue(undefined);
  });

  it('renders the subscription rows', () => {
    render(<WebhooksListClient initialData={sample} />);
    expect(screen.getByText('orders')).toBeInTheDocument();
    expect(screen.getByText('analytics')).toBeInTheDocument();
  });

  it('renders status badges with the right label', () => {
    render(<WebhooksListClient initialData={sample} />);
    expect(screen.getByText('Healthy')).toBeInTheDocument();
    expect(screen.getByText('Disabled')).toBeInTheDocument();
  });

  it('renders the empty state when there are no subscriptions', () => {
    render(
      <WebhooksListClient
        initialData={{ data: [], pagination: { next_cursor: '' } }}
      />,
    );
    expect(screen.getByText(/No webhook subscriptions yet/i)).toBeInTheDocument();
  });

  it('fires testSubscription when the Test button is clicked', async () => {
    render(<WebhooksListClient initialData={sample} />);
    fireEvent.click(screen.getByLabelText('Test orders'));
    await waitFor(() =>
      expect(mocks.testSubscription).toHaveBeenCalledWith('sub-1'),
    );
    // Result notice is surfaced.
    expect(await screen.findByTestId('test-result')).toHaveTextContent(
      /HTTP 200/,
    );
  });

  it('disables an active subscription via disableSubscription', async () => {
    render(<WebhooksListClient initialData={sample} />);
    fireEvent.click(screen.getByLabelText('Disable orders'));
    await waitFor(() =>
      expect(mocks.disableSubscription).toHaveBeenCalledWith('sub-1'),
    );
  });

  it('enables a disabled subscription via enableSubscription', async () => {
    render(<WebhooksListClient initialData={sample} />);
    fireEvent.click(screen.getByLabelText('Enable analytics'));
    await waitFor(() =>
      expect(mocks.enableSubscription).toHaveBeenCalledWith('sub-2'),
    );
  });

  it('removes a row after confirmed delete', async () => {
    const origConfirm = window.confirm;
    window.confirm = vi.fn(() => true);
    render(<WebhooksListClient initialData={sample} />);
    fireEvent.click(screen.getByLabelText('Delete orders'));
    await waitFor(() =>
      expect(mocks.deleteSubscription).toHaveBeenCalledWith('sub-1'),
    );
    // After the optimistic removal, the row should be gone.
    expect(screen.queryByText('orders')).not.toBeInTheDocument();
    window.confirm = origConfirm;
  });

  it('skips delete when the operator cancels the confirm', async () => {
    const origConfirm = window.confirm;
    window.confirm = vi.fn(() => false);
    render(<WebhooksListClient initialData={sample} />);
    fireEvent.click(screen.getByLabelText('Delete orders'));
    await Promise.resolve();
    expect(mocks.deleteSubscription).not.toHaveBeenCalled();
    window.confirm = origConfirm;
  });
});
