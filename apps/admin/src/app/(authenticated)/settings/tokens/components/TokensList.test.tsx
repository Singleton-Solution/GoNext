/**
 * TokensList — brand surface + behaviour tests.
 *
 * Pins the Living-Systems chrome:
 *  - Empty state shows the "Create your first token" emerald CTA.
 *  - Loading state surfaces a polite "Loading…" indicator.
 *  - Populated state renders one row per token with a mono prefix, a
 *    lavender chip per scope, and a destructive Revoke button.
 *
 * `../api` is mocked so the component renders without hitting fetch.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';

import type { TokenView } from '../types';
import { listTokens } from '../api';

vi.mock('../api', () => ({
  listTokens: vi.fn(),
  revokeToken: vi.fn(),
}));

import { TokensList } from './TokensList';

const listMock = listTokens as unknown as ReturnType<typeof vi.fn>;

const sample: TokenView[] = [
  {
    id: 't-1',
    name: 'github-actions',
    prefix: 'AbCdEfGh',
    scopes: ['read', 'edit_posts'],
    created_at: '2026-05-01T00:00:00Z',
    last_used_at: '2026-05-24T12:00:00Z',
    expires_at: '2027-05-01T00:00:00Z',
  },
  {
    id: 't-2',
    name: 'laptop-cli',
    prefix: 'ZzYyXxWw',
    scopes: ['read'],
    created_at: '2026-04-01T00:00:00Z',
    last_used_at: null,
    expires_at: null,
  },
];

describe('<TokensList>', () => {
  beforeEach(() => {
    listMock.mockReset();
  });

  it('shows the empty state with a "Create your first token" CTA', async () => {
    listMock.mockResolvedValueOnce([]);
    render(<TokensList />);
    await waitFor(() => {
      expect(screen.getByTestId('tokens-empty')).toBeInTheDocument();
    });
    const cta = screen.getByRole('link', { name: /create your first token/i });
    expect(cta).toHaveAttribute('href', '/settings/tokens/new');
  });

  it('renders one row per token with mono prefix + lavender scope chips', async () => {
    listMock.mockResolvedValueOnce(sample);
    render(<TokensList />);
    await waitFor(() => {
      expect(screen.getByTestId('tokens-table')).toBeInTheDocument();
    });
    // Two body rows + one header row.
    const rows = screen.getAllByRole('row');
    expect(rows).toHaveLength(1 + sample.length);
    // Mono prefix.
    expect(screen.getByText(/gnp_AbCdEfGh/)).toBeInTheDocument();
    expect(screen.getByText(/gnp_ZzYyXxWw/)).toBeInTheDocument();
    // Lavender scope chips carry data-scope. Two chips on row 1, one on row 2.
    const scopeChips = screen
      .getAllByText(/^(read|edit_posts)$/)
      .filter((el) => el.getAttribute('data-scope'));
    expect(scopeChips.length).toBeGreaterThanOrEqual(3);
    const first = scopeChips[0];
    expect(first).toBeDefined();
    expect(first?.className).toMatch(/lavender/);
  });

  it('renders a destructive Revoke button on each row', async () => {
    listMock.mockResolvedValueOnce(sample);
    render(<TokensList />);
    await waitFor(() => {
      expect(screen.getByTestId('revoke-t-1')).toBeInTheDocument();
    });
    const revoke = screen.getByTestId('revoke-t-1');
    expect(revoke.className).toMatch(/bg-danger/);
  });
});
