/**
 * UserDetail — brand-surface snapshot + behaviour tests.
 *
 * Pins the Living-Systems chrome:
 *  - Italic-accent headline ("in detail").
 *  - Avatar fallback initials.
 *  - Save button disabled until the operator changes a field.
 *  - Audit log renders one row per event; empty state is its own branch.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn(), prefetch: vi.fn() }),
}));

import { UserDetail, type AuditEvent } from './UserDetail';
import type { AdminUser } from '../types';

const sampleUser: AdminUser = {
  id: 'u_1',
  handle: 'alice',
  email: 'alice@example.com',
  display_name: 'Alice Adams',
  status: 'active',
  role: 'editor',
  last_seen_at: '2026-05-10T10:00:00Z',
};

const sampleAudit: AuditEvent[] = [
  {
    id: 'evt-1',
    at: '2026-05-25T10:00:00Z',
    action: 'Signed in',
    source: '203.0.113.42 · Firefox',
  },
  {
    id: 'evt-2',
    at: '2026-05-24T08:00:00Z',
    action: 'Updated profile',
  },
];

describe('<UserDetail>', () => {
  it('renders the brand headline with the italic accent', () => {
    render(<UserDetail user={sampleUser} audit={sampleAudit} />);
    const heading = screen.getByRole('heading', { level: 1 });
    // The italic-accent rule is the canonical brand move.
    expect(heading.querySelector('em')?.textContent).toMatch(/in detail/i);
    // Outer tag carries the brand Archivo classes.
    expect(heading.className).toContain('font-display');
  });

  it('shows the initials in the avatar fallback', () => {
    render(<UserDetail user={sampleUser} audit={sampleAudit} />);
    expect(screen.getByText('AA')).toBeInTheDocument();
  });

  it('disables Save until a field changes', () => {
    render(<UserDetail user={sampleUser} audit={sampleAudit} />);
    const save = screen.getByTestId('user-detail-save') as HTMLButtonElement;
    expect(save).toBeDisabled();

    // Toggle the status switch — Save should become enabled.
    fireEvent.click(screen.getByRole('switch', { name: /account active/i }));
    expect(save).not.toBeDisabled();
  });

  it('renders one audit list item per event', () => {
    render(<UserDetail user={sampleUser} audit={sampleAudit} />);
    const log = screen.getByTestId('audit-log');
    expect(log.querySelectorAll('li')).toHaveLength(sampleAudit.length);
    expect(screen.getByText(/signed in/i)).toBeInTheDocument();
  });

  it('renders an empty audit placeholder when the log is empty', () => {
    render(<UserDetail user={sampleUser} audit={[]} />);
    expect(screen.getByTestId('audit-empty')).toBeInTheDocument();
    expect(screen.queryByTestId('audit-log')).not.toBeInTheDocument();
  });
});
