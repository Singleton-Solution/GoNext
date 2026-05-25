/**
 * WebhookDetail — Living-Systems brand snapshot.
 *
 * Pins the visual contract:
 *  1. Headline renders the italic-accent rule with the subscription
 *     name as the serif italic.
 *  2. Test/Disable/Delete toolbar uses the emerald/default/destructive
 *     button variants.
 *  3. Response code renders as a mono badge tinted to the status
 *     (2xx → emerald, 5xx → danger, etc.).
 *  4. Deliveries table head sits on paper-2.
 *  5. Expanded delivery preview renders Geist Mono on paper-3.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => '/webhooks/abc',
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
  listEventCatalog: vi.fn().mockResolvedValue({ data: [] }),
}));
vi.mock('../actions', () => mocks);

import { WebhookDetailClient } from './WebhookDetailClient';
import type {
  DeliveryListResponse,
  Subscription,
} from '../types';

const sub: Subscription = {
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
};

const deliveries: DeliveryListResponse = {
  data: [
    {
      id: 1,
      subscription_id: 'sub-1',
      event_id: 'evt-001',
      event_type: 'post.published',
      attempt: 1,
      status: 'success',
      response_code: 200,
      duration_ms: 42,
      response_body_preview: '{"ok":true}',
      delivered_at: '2026-05-19T11:00:00Z',
    },
    {
      id: 2,
      subscription_id: 'sub-1',
      event_id: 'evt-002',
      event_type: 'post.published',
      attempt: 1,
      status: 'failed',
      response_code: 503,
      duration_ms: 1200,
      error: 'upstream timeout',
      delivered_at: '2026-05-19T10:55:00Z',
    },
  ],
  pagination: { next_cursor: '' },
};

describe('WebhookDetailClient — brand snapshot', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders the headline with the italic-accent rule', () => {
    render(<WebhookDetailClient subscription={sub} deliveries={deliveries} />);
    const h1 = screen.getByRole('heading', { level: 1 });
    expect(h1.className).toContain('font-display');
    expect(h1.className).toContain('[&_em]:font-serif');
    expect(h1.className).toContain('[&_em]:italic');
    expect(h1.className).toContain('[&_em]:text-emerald-deep');
  });

  it('response code badge picks up the emerald tint for 2xx', () => {
    render(<WebhookDetailClient subscription={sub} deliveries={deliveries} />);
    const code200 = screen.getByText('200');
    expect(code200.className).toContain('font-mono');
    expect(code200.className).toContain('bg-emerald-soft');
    expect(code200.className).toContain('text-emerald-deep');
  });

  it('response code badge picks up the danger tint for 5xx', () => {
    render(<WebhookDetailClient subscription={sub} deliveries={deliveries} />);
    const code503 = screen.getByText('503');
    expect(code503.className).toContain('font-mono');
    expect(code503.className).toContain('bg-danger-soft');
    expect(code503.className).toContain('text-danger');
  });

  it('duration renders in Geist Mono with tabular-nums', () => {
    render(<WebhookDetailClient subscription={sub} deliveries={deliveries} />);
    const duration = screen.getByText('42 ms');
    expect(duration.className).toContain('font-mono');
    expect(duration.className).toContain('tabular-nums');
  });

  it('expanded preview renders mono on paper-3', () => {
    render(<WebhookDetailClient subscription={sub} deliveries={deliveries} />);
    // Click the Show button for delivery #1.
    const row = screen.getByTestId('delivery-row-1');
    const showBtn = row.querySelector('button[aria-expanded]') as HTMLElement;
    fireEvent.click(showBtn);
    const preview = screen.getByText('{"ok":true}');
    expect(preview.tagName).toBe('PRE');
    expect(preview.className).toContain('font-mono');
    expect(preview.className).toContain('bg-paper-3');
  });

  it('table head sits on paper-2', () => {
    render(<WebhookDetailClient subscription={sub} deliveries={deliveries} />);
    const table = screen.getByLabelText('Recent deliveries');
    const headRow = table.querySelector('thead tr');
    expect(headRow?.className).toContain('bg-paper-2');
  });
});
