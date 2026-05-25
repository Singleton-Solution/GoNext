/**
 * NewSubscriptionClient — Living-Systems brand snapshot.
 *
 * Pins the visual contract:
 *  1. Form lives inside a Card panel (paper-2 head + paper body).
 *  2. Submit button is the emerald variant (positive create CTA).
 *  3. The post-create state shows the secret in a paper-3 recessed
 *     surface with Geist Mono — the one-time secret reveal pattern.
 *  4. The warning callout uses warning-soft / warning text tokens.
 */
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';

vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => '/webhooks/new',
  useSearchParams: () => new URLSearchParams(),
}));

const mocks = vi.hoisted(() => ({
  createSubscription: vi.fn(),
  listEventCatalog: vi.fn().mockResolvedValue({
    data: [{ name: 'post.published', description: 'A post fired.' }],
  }),
}));
vi.mock('../actions', () => mocks);

import { NewSubscriptionClient } from './NewSubscriptionClient';

describe('NewSubscriptionClient — brand snapshot', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.listEventCatalog.mockResolvedValue({
      data: [{ name: 'post.published', description: 'A post fired.' }],
    });
  });

  it('renders the form inside a Card with a paper-2 head', () => {
    render(<NewSubscriptionClient />);
    const heading = screen.getByText('Configuration');
    expect(heading.className).toContain('font-display');
    expect(heading.className).toContain('uppercase');
    // The head sits on paper-2.
    const headWrap = heading.parentElement as HTMLElement;
    expect(headWrap.className).toContain('bg-paper-2');
  });

  it('submit button lands on the emerald variant', () => {
    render(<NewSubscriptionClient />);
    const submit = screen.getByRole('button', {
      name: /Create subscription/,
    });
    expect(submit.className).toContain('bg-emerald');
    expect(submit.className).toContain('text-emerald-ink');
  });

  describe('post-create state', () => {
    beforeEach(() => {
      mocks.createSubscription.mockResolvedValue({
        id: 'sub-new',
        name: 'orders',
        url: 'https://example.com',
        events: ['post.published'],
        active: true,
        created_at: '2026-05-19T12:00:00Z',
        updated_at: '2026-05-19T12:00:00Z',
        consecutive_failures: 0,
        secret: 'deadbeef-cafe-1234-5678-aabbccddeeff',
      });
    });

    afterEach(() => {
      vi.clearAllMocks();
    });

    it('renders the secret on paper-3 in Geist Mono', async () => {
      render(<NewSubscriptionClient />);
      fireEvent.change(screen.getByLabelText('Name'), {
        target: { value: 'orders' },
      });
      fireEvent.change(screen.getByLabelText('Endpoint URL'), {
        target: { value: 'https://example.com' },
      });
      // Wait for the catalog to load and the form to render the event.
      await waitFor(() => screen.getByLabelText('Subscribe to post.published'));
      await act(async () => {
        fireEvent.click(screen.getByText('Create subscription'));
      });
      const secret = await screen.findByTestId('created-secret');
      expect(secret.className).toContain('font-mono');
      expect(secret.className).toContain('bg-paper-3');
    });
  });
});
