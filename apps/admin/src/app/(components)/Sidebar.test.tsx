/**
 * Sidebar component test.
 *
 * Asserts the IA: the seven canonical destinations (Dashboard, Posts, Pages,
 * Comments, Media, Users, Settings) all render as links to their respective
 * routes. The collapse toggle exists and flips its aria-expanded state.
 *
 * `next/navigation` is mocked because the App Router hook (`usePathname`) is
 * not implemented in jsdom — we replace it with a deterministic stub.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

vi.mock('next/navigation', () => ({
  usePathname: () => '/posts',
}));

import { Sidebar } from './Sidebar';

describe('Sidebar', () => {
  it('renders the canonical nav links', () => {
    render(<Sidebar />);

    const expected: Array<[label: string, href: string]> = [
      ['Dashboard', '/'],
      ['Posts', '/posts'],
      ['Pages', '/pages'],
      ['Comments', '/comments'],
      ['Media', '/media'],
      ['Users', '/users'],
      ['Plugins', '/plugins'],
      ['Settings', '/settings'],
      ['System Status', '/status'],
    ];

    for (const [label, href] of expected) {
      const link = screen.getByRole('link', { name: new RegExp(label, 'i') });
      expect(link).toBeInTheDocument();
      expect(link).toHaveAttribute('href', href);
    }
  });

  it('marks the active route with aria-current', () => {
    render(<Sidebar />);
    const active = screen.getByRole('link', { name: /Posts/i });
    expect(active).toHaveAttribute('aria-current', 'page');

    const inactive = screen.getByRole('link', { name: /Dashboard/i });
    expect(inactive).not.toHaveAttribute('aria-current');
  });

  it('toggles collapsed state via the toggle button', () => {
    render(<Sidebar />);
    const toggle = screen.getByRole('button', { name: /Collapse sidebar/i });
    expect(toggle).toHaveAttribute('aria-expanded', 'true');

    fireEvent.click(toggle);

    const reopened = screen.getByRole('button', { name: /Expand sidebar/i });
    expect(reopened).toHaveAttribute('aria-expanded', 'false');
  });
});
