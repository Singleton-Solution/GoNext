/**
 * InviteForm — brand-surface snapshot + behaviour tests.
 *
 * Pins:
 *  - Italic-accent headline ("someone").
 *  - Validation on missing / malformed email.
 *  - The emerald Send CTA renders with the brand variant.
 */
import { describe, expect, it } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

import { InviteForm } from './InviteForm';

describe('<InviteForm>', () => {
  it('renders the brand headline with the italic accent', () => {
    render(<InviteForm />);
    const heading = screen.getByRole('heading', { level: 1 });
    expect(heading.querySelector('em')?.textContent).toMatch(/someone/i);
    expect(heading.className).toContain('font-display');
  });

  it('rejects an empty email with an inline error', async () => {
    render(<InviteForm />);
    fireEvent.submit(screen.getByTestId('invite-form'));
    await waitFor(() => {
      expect(screen.getByTestId('invite-error').textContent).toMatch(
        /enter the new collaborator/i,
      );
    });
  });

  it('rejects a malformed email', async () => {
    render(<InviteForm />);
    const email = screen.getByTestId('invite-email');
    fireEvent.change(email, { target: { value: 'nope' } });
    fireEvent.submit(screen.getByTestId('invite-form'));
    await waitFor(() => {
      expect(screen.getByTestId('invite-error').textContent).toMatch(
        /complete email/i,
      );
    });
  });

  it('renders the emerald Send CTA', () => {
    render(<InviteForm />);
    const submit = screen.getByTestId('invite-submit');
    expect(submit.textContent).toMatch(/send/i);
    // The emerald variant compiles into bg-emerald — class-name contract.
    expect(submit.className).toMatch(/bg-emerald/);
  });
});
