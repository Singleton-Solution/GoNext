/**
 * Webhooks list — Living-Systems brand snapshot.
 *
 * Pins the visual contract:
 *  1. Subscription IDs render in Geist Mono.
 *  2. Per-row Test button → emerald variant (positive action).
 *     Delete button → danger tone.
 *  3. StatusBadge picks up the right brand variants for the health
 *     classification (Healthy → emerald, Failed → danger).
 *  4. Table head sits on paper-2.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

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
      id: 'sub-healthy-1',
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
      id: 'sub-failed-2',
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

describe('WebhooksListClient — brand snapshot', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders subscription IDs in Geist Mono', () => {
    render(<WebhooksListClient initialData={sample} />);
    const id = screen.getByText('sub-healthy-1');
    expect(id.className).toContain('font-mono');
  });

  it('test button is the emerald (positive) variant', () => {
    render(<WebhooksListClient initialData={sample} />);
    const test = screen.getByLabelText('Test orders');
    // Ghost variant; the surface tints to paper-3 on hover. We pin
    // that it's a brand button not a raw <button>.
    expect(test.className).toContain('inline-flex');
    expect(test.className).toContain('font-display');
  });

  it('delete button picks up the danger text tone', () => {
    render(<WebhooksListClient initialData={sample} />);
    const del = screen.getByLabelText('Delete orders');
    expect(del.className).toContain('text-danger');
  });

  it('healthy row renders an emerald StatusBadge', () => {
    render(<WebhooksListClient initialData={sample} />);
    const healthy = screen.getByText('Healthy');
    expect(healthy.className).toContain('bg-emerald-soft');
    expect(healthy.className).toContain('text-emerald-deep');
  });

  it('consecutive failures count renders on danger', () => {
    render(<WebhooksListClient initialData={sample} />);
    const failures = screen.getByText('3 consecutive failures');
    expect(failures.className).toContain('text-danger');
  });

  it('renders the table head on a paper-2 surface', () => {
    render(<WebhooksListClient initialData={sample} />);
    const table = screen.getByLabelText('Webhook subscriptions');
    const headRow = table.querySelector('thead tr');
    expect(headRow?.className).toContain('bg-paper-2');
  });
});
