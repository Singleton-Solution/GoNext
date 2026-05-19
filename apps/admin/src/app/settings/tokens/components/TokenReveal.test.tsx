/**
 * TokenReveal tests.
 *
 * The component is the single most security-sensitive surface in the
 * admin app: it shows the plaintext PAT once and gates dismissal behind
 * "I've saved it". The tests pin every load-bearing behavior:
 *
 *   1. Plaintext is visible (revealed) after Show.
 *   2. Copy-to-clipboard calls navigator.clipboard.writeText with the
 *      exact plaintext (no slicing, no fingerprint).
 *   3. The Done button is disabled until the confirmation checkbox is
 *      ticked, and then onDismiss fires.
 *   4. effective_scopes mismatch surfaces the warning.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { TokenReveal } from './TokenReveal';
import type { IssuedTokenView } from '../types';

function makeToken(over: Partial<IssuedTokenView> = {}): IssuedTokenView {
  return {
    id: '00000000-0000-7000-8000-000000000001',
    name: 'ci-token',
    prefix: 'AbCdEfGh',
    scopes: ['read', 'edit_posts'],
    effective_scopes: ['read', 'edit_posts'],
    created_at: '2026-01-01T00:00:00Z',
    plaintext: 'gnp_AbCdEfGh01234567890123456789ZyXw',
    save_now: true,
    ...over,
  };
}

describe('TokenReveal', () => {
  beforeEach(() => {
    // Reset clipboard for each test.
    Object.assign(navigator, {
      clipboard: {
        writeText: vi.fn(() => Promise.resolve()),
      },
    });
  });

  it('renders the plaintext field with a placeholder masked input', () => {
    const onDismiss = vi.fn();
    render(<TokenReveal token={makeToken()} onDismiss={onDismiss} />);
    const field = screen.getByTestId('token-plaintext') as HTMLInputElement;
    expect(field).toBeInTheDocument();
    expect(field.value).toBe('gnp_AbCdEfGh01234567890123456789ZyXw');
    // Masked by default.
    expect(field.type).toBe('password');
  });

  it('reveals the plaintext when Show is clicked', () => {
    render(<TokenReveal token={makeToken()} onDismiss={vi.fn()} />);
    const field = screen.getByTestId('token-plaintext') as HTMLInputElement;
    fireEvent.click(screen.getByRole('button', { name: /show token/i }));
    expect(field.type).toBe('text');
  });

  it('copies the full plaintext to the clipboard', async () => {
    const token = makeToken();
    render(<TokenReveal token={token} onDismiss={vi.fn()} />);
    fireEvent.click(screen.getByTestId('token-copy'));
    await waitFor(() => {
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith(token.plaintext);
    });
    // Pill flips to Copied!
    expect(screen.getByTestId('token-copy').textContent).toMatch(/copied/i);
  });

  it('disables Done until the confirmation checkbox is checked', () => {
    const onDismiss = vi.fn();
    render(<TokenReveal token={makeToken()} onDismiss={onDismiss} />);
    const done = screen.getByTestId('token-done') as HTMLButtonElement;
    expect(done).toBeDisabled();

    fireEvent.click(done);
    expect(onDismiss).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId('token-confirm'));
    expect(done).not.toBeDisabled();

    fireEvent.click(done);
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it('surfaces an effective_scopes warning when the intersection is narrower than requested', () => {
    render(
      <TokenReveal
        token={makeToken({
          scopes: ['read', 'manage_options'],
          effective_scopes: ['read'],
        })}
        onDismiss={vi.fn()}
      />,
    );
    expect(screen.getByText(/effective scopes/i)).toBeInTheDocument();
    expect(screen.getByText(/narrower than requested/i)).toBeInTheDocument();
  });

  it('does not show the warning when scopes match', () => {
    render(<TokenReveal token={makeToken()} onDismiss={vi.fn()} />);
    expect(screen.queryByText(/narrower than requested/i)).not.toBeInTheDocument();
  });
});
