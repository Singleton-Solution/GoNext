/**
 * UsersList component tests.
 *
 * The server-fetch path in `page.tsx` is exercised by integration tests in a
 * follow-up issue (needs MSW once the OpenAPI fixture lands per #240). Here
 * we lock down the behaviour the issue acceptance criteria call out
 * explicitly: empty state, row rendering, partial email mask, and the
 * visually-distinct status badge for suspended users.
 *
 * `next/navigation` is mocked because RTL runs in jsdom without an App
 * Router; we replace `useRouter` with a deterministic spy.
 */
import { describe, expect, it, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';

const pushSpy = vi.fn();

vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: pushSpy, replace: vi.fn(), prefetch: vi.fn() }),
}));

import { UsersList } from './UsersList';
import type { AdminUser } from './types';

const sample: AdminUser[] = [
  {
    id: 'u_1',
    handle: 'alice',
    email: 'alice@example.com',
    display_name: 'Alice Adams',
    status: 'active',
    role: 'admin',
    last_seen_at: '2026-05-10T10:00:00Z',
  },
  {
    id: 'u_2',
    handle: 'bob',
    email: 'bob@example.com',
    display_name: 'Bob Brown',
    status: 'suspended',
    role: 'editor',
    last_seen_at: '2026-04-01T08:30:00Z',
  },
  {
    id: 'u_3',
    handle: 'carol',
    email: 'carol@example.com',
    display_name: 'Carol Carter',
    status: 'active',
    role: 'subscriber',
    last_seen_at: null,
  },
];

describe('UsersList', () => {
  it('shows the empty state when no users are returned', () => {
    render(<UsersList users={[]} />);
    expect(screen.getByText(/no users yet/i)).toBeInTheDocument();
    // The invite CTA is still rendered — the empty list is not an error.
    expect(screen.getByRole('link', { name: /invite user/i })).toHaveAttribute(
      'href',
      '/users/new',
    );
  });

  it('renders one row per user', () => {
    render(<UsersList users={sample} />);
    // 1 header row + 3 body rows
    const rows = screen.getAllByRole('row');
    expect(rows).toHaveLength(1 + sample.length);
    expect(screen.getByText('@alice')).toBeInTheDocument();
    expect(screen.getByText('@bob')).toBeInTheDocument();
    expect(screen.getByText('@carol')).toBeInTheDocument();
  });

  it('renders emails with a partial mask, never the raw value', () => {
    render(<UsersList users={sample} />);
    expect(screen.getByText('a***@example.com')).toBeInTheDocument();
    expect(screen.getByText('b***@example.com')).toBeInTheDocument();
    expect(screen.getByText('c***@example.com')).toBeInTheDocument();
    // Raw email addresses must not leak into the table.
    expect(screen.queryByText('alice@example.com')).not.toBeInTheDocument();
    expect(screen.queryByText('bob@example.com')).not.toBeInTheDocument();
  });

  it('styles the suspended status badge differently from active', () => {
    render(<UsersList users={sample} />);
    const aliceRow = screen.getByRole('row', { name: /open alice/i });
    const bobRow = screen.getByRole('row', { name: /open bob/i });

    const aliceBadge = within(aliceRow).getByText(/active/i);
    const bobBadge = within(bobRow).getByText(/suspended/i);

    // The legacy data-status attribute survives the brand refresh; downstream
    // automation may still key off it.
    expect(aliceBadge).toHaveAttribute('data-status', 'active');
    expect(bobBadge).toHaveAttribute('data-status', 'suspended');

    // Visual distinction now flows from token-driven badge variants
    // (success / danger) rather than inline backgroundColor styles. The two
    // pills should carry different className contracts so a reviewer can't
    // accidentally render two visually identical pills.
    expect(aliceBadge.className).not.toBe(bobBadge.className);
    expect(aliceBadge.className).toMatch(/success/);
    expect(bobBadge.className).toMatch(/danger/);
  });
});
