/**
 * Layout boundary tests for the `(public)` route group.
 *
 * Asserts the chrome split: surfaces under the public layout
 * (/login, /setup) render WITHOUT the admin sidebar, header bar, or
 * any other signed-in IA. The previous scaffold leaked the sidebar
 * onto these surfaces because the root layout rendered it
 * unconditionally; this test pins the fix.
 *
 * The chosen assertion is the absence of `<aside>`: the sidebar is
 * the only `<aside>` the admin renders, so asserting its absence is
 * both targeted and robust against incidental DOM changes.
 *
 * next/navigation hooks are stubbed because jsdom doesn't ship the
 * Next App Router. The stubs are deterministic enough to drive the
 * client islands the pages mount (e.g. the login form, the setup
 * wizard's router-push on success).
 */
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('next/navigation', () => ({
  usePathname: () => '/login',
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    prefetch: vi.fn(),
    refresh: vi.fn(),
  }),
  useSearchParams: () => new URLSearchParams(),
}));

import PublicLayout from './layout';
import LoginPage from './login/page';

describe('(public) layout', () => {
  it('does NOT render the admin sidebar around /login', () => {
    const { container } = render(
      <PublicLayout>
        <LoginPage />
      </PublicLayout>,
    );
    expect(container.querySelector('aside')).toBeNull();
    // Sanity: the actual page content IS present.
    expect(screen.getByRole('heading', { name: /Sign in/i })).toBeInTheDocument();
  });

  it('does NOT render the admin header bar around /login', () => {
    const { container } = render(
      <PublicLayout>
        <LoginPage />
      </PublicLayout>,
    );
    // The signed-in header is the only `.app-shell__header` div the
    // admin emits; its absence means the chrome did not leak.
    expect(container.querySelector('.app-shell__header')).toBeNull();
    expect(container.querySelector('.app-shell')).toBeNull();
  });

  it('wraps content in a centered public-shell container', () => {
    const { container } = render(
      <PublicLayout>
        <div>marker</div>
      </PublicLayout>,
    );
    expect(container.querySelector('.public-shell')).not.toBeNull();
    expect(container.querySelector('.public-shell__content')).not.toBeNull();
  });
});

describe('(public) layout — wraps /setup with NO sidebar', () => {
  // The setup page is an async server component. We don't render it
  // through the actual page module (that would require a Next runtime
  // to handle the server fetch); instead we mount the SetupWizard
  // directly. The behaviour under test is the layout, not the wizard.
  it('renders the setup wizard without the admin sidebar', async () => {
    const SetupWizardMod = await import('./setup/SetupWizard');
    const SetupWizard = SetupWizardMod.default;
    const { container } = render(
      <PublicLayout>
        <SetupWizard
          initialStatus={{ installation_completed: false, user_count: 0 }}
        />
      </PublicLayout>,
    );
    expect(container.querySelector('aside')).toBeNull();
    expect(container.querySelector('.sidebar')).toBeNull();
  });
});
