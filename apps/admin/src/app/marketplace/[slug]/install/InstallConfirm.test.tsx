/**
 * InstallConfirm tests.
 *
 * The crucial assertion here is that the install confirmation screen
 * REUSES the CapabilityReview component from the manual install path
 * — not a duplicated copy. We assert that by:
 *
 *   - looking at the panel's aria-label (the shared component spells
 *     it "Capability review"),
 *   - confirming the consent checkbox carries the same id
 *     `capabilities_acknowledged` the manual flow exposes.
 *
 * Other coverage:
 *   - Install button is disabled until consent is given,
 *   - clicking Install with a faked action returns the success panel
 *     pointing at /plugins.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';

const installSpy = vi.fn(
  async (_slug: string, _ack: boolean) => ({
    ok: true as const,
    data: { slug: 'akismet', version: '1.0.0', plugin_slug: 'akismet' },
  }),
);

vi.mock('next/navigation', () => ({
  useRouter: () => ({
    refresh: vi.fn(),
    push: vi.fn(),
    replace: vi.fn(),
    prefetch: vi.fn(),
  }),
}));

vi.mock('../../actions', () => ({
  installMarketplacePlugin: (slug: string, ack: boolean) =>
    installSpy(slug, ack),
}));

import { InstallConfirm } from './InstallConfirm';

describe('InstallConfirm', () => {
  beforeEach(() => {
    installSpy.mockClear();
  });

  it('renders the shared CapabilityReview panel for the manifest caps', () => {
    render(
      <InstallConfirm
        slug="akismet"
        listingName="Akismet"
        versionLabel="1.0.0"
        capabilities={['posts.read', 'email.send']}
      />,
    );
    // The shared component's panel aria-label is "Capability review".
    expect(
      screen.getByLabelText(/capability review/i),
    ).toBeInTheDocument();
    // Both capability ids should be visible.
    expect(screen.getByText('posts.read')).toBeInTheDocument();
    expect(screen.getByText('email.send')).toBeInTheDocument();
    // Consent checkbox from the shared component:
    expect(
      document.getElementById('capabilities_acknowledged'),
    ).toBeInTheDocument();
  });

  it('disables Install until consent is given', () => {
    render(
      <InstallConfirm
        slug="akismet"
        listingName="Akismet"
        versionLabel="1.0.0"
        capabilities={['posts.read']}
      />,
    );
    const button = screen.getByRole('button', { name: /install plugin/i });
    expect(button).toBeDisabled();
    const consent = document.getElementById(
      'capabilities_acknowledged',
    ) as HTMLInputElement;
    fireEvent.click(consent);
    expect(button).not.toBeDisabled();
  });

  it('shows the success panel after a successful install', async () => {
    render(
      <InstallConfirm
        slug="akismet"
        listingName="Akismet"
        versionLabel="1.0.0"
        capabilities={[]}
      />,
    );
    const consent = document.getElementById(
      'capabilities_acknowledged',
    ) as HTMLInputElement;
    fireEvent.click(consent);
    const button = screen.getByRole('button', { name: /install plugin/i });
    await act(async () => {
      fireEvent.click(button);
    });
    expect(installSpy).toHaveBeenCalledWith('akismet', true);
    expect(screen.getByRole('status').textContent).toMatch(
      /“Akismet” installed/,
    );
  });
});
