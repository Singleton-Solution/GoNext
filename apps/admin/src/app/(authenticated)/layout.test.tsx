/**
 * Layout boundary tests for the `(authenticated)` route group.
 *
 * Asserts that signed-in surfaces (e.g. /posts) DO render the admin
 * sidebar — the chrome that the public layout intentionally omits.
 * Together with `(public)/layout.test.tsx` this pins the split:
 *
 *   public/* → no sidebar, no header chrome
 *   (authenticated)/* → sidebar + header
 *
 * next/navigation is stubbed because jsdom doesn't ship the App
 * Router. The stub returns /posts so the Sidebar marks the Posts
 * link as active and we can assert it without coincidence.
 */
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('next/navigation', () => ({
  usePathname: () => '/posts',
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    prefetch: vi.fn(),
    refresh: vi.fn(),
  }),
  useSearchParams: () => new URLSearchParams(),
}));

import AuthenticatedLayout from './layout';

describe('(authenticated) layout', () => {
  it('renders the admin sidebar', () => {
    const { container } = render(
      <AuthenticatedLayout>
        <div>page content</div>
      </AuthenticatedLayout>,
    );
    const aside = container.querySelector('aside');
    expect(aside).not.toBeNull();
    expect(aside).toHaveAttribute('aria-label', 'Primary navigation');
  });

  it('renders the admin header bar', () => {
    const { container } = render(
      <AuthenticatedLayout>
        <div>page content</div>
      </AuthenticatedLayout>,
    );
    expect(container.querySelector('.app-shell__header')).not.toBeNull();
    expect(container.querySelector('.app-shell__main')).not.toBeNull();
  });

  it('wraps children inside <main>', () => {
    render(
      <AuthenticatedLayout>
        <div data-testid="page-content">page content</div>
      </AuthenticatedLayout>,
    );
    const main = screen.getByRole('main');
    expect(main).toContainElement(screen.getByTestId('page-content'));
  });

  it('renders the Posts nav link (the (authenticated) layout owns it)', () => {
    render(
      <AuthenticatedLayout>
        <div>posts page</div>
      </AuthenticatedLayout>,
    );
    const link = screen.getByRole('link', { name: /Posts/i });
    expect(link).toHaveAttribute('href', '/posts');
  });
});
