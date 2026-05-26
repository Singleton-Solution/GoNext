/**
 * Reset-password page tests.
 *
 * Covers the happy path (token in query, valid password) and the
 * three failure modes the user is most likely to hit:
 *   - mismatched confirm field (client-side check)
 *   - 410 invalid_or_expired_token (API)
 *   - 422 weak_password (API, with detail string)
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';

const searchParamsMock = new URLSearchParams('?token=' + 'a'.repeat(64));
vi.mock('next/navigation', () => ({
  usePathname: () => '/reset-password',
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    prefetch: vi.fn(),
    refresh: vi.fn(),
  }),
  useSearchParams: () => searchParamsMock,
}));

const apiPost = vi.fn();
vi.mock('@/lib/api-client', async () => {
  const actual =
    await vi.importActual<typeof import('@/lib/api-client')>('@/lib/api-client');
  return {
    ...actual,
    api: {
      ...actual.api,
      post: (...args: unknown[]) =>
        apiPost(...(args as Parameters<typeof actual.api.post>)),
    },
  };
});

import ResetPasswordPage from './page';
import { ApiError } from '@/lib/api-client';

const STRONG_PASSWORD = 'correct-horse-battery-staple';

async function fillBothPasswordFields(
  value: string,
  confirmValue: string = value,
): Promise<void> {
  fireEvent.change(await screen.findByLabelText(/^new password$/i), {
    target: { value },
  });
  fireEvent.change(screen.getByLabelText(/confirm new password/i), {
    target: { value: confirmValue },
  });
}

describe('ResetPasswordPage', () => {
  beforeEach(() => {
    apiPost.mockReset();
  });

  it('confirms the reset and shows the done state', async () => {
    apiPost.mockResolvedValueOnce({ user_id: 'user-1' });
    render(<ResetPasswordPage />);

    await fillBothPasswordFields(STRONG_PASSWORD);
    fireEvent.click(screen.getByRole('button', { name: /set new password/i }));

    await waitFor(() => {
      expect(apiPost).toHaveBeenCalledWith(
        '/api/v1/auth/password-reset/confirm',
        expect.objectContaining({ new_password: STRONG_PASSWORD }),
      );
    });
    expect(
      await screen.findByText(/your password has been updated/i),
    ).toBeInTheDocument();
  });

  it('blocks submit when the confirm field does not match', async () => {
    render(<ResetPasswordPage />);

    await fillBothPasswordFields(STRONG_PASSWORD, 'different-password-xxxxx');
    fireEvent.click(screen.getByRole('button', { name: /set new password/i }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      /do not match/i,
    );
    expect(apiPost).not.toHaveBeenCalled();
  });

  it('renders the expired-token message on 410', async () => {
    apiPost.mockRejectedValueOnce(
      new ApiError(410, 'Gone', { error: 'invalid_or_expired_token' }),
    );
    render(<ResetPasswordPage />);

    await fillBothPasswordFields(STRONG_PASSWORD);
    fireEvent.click(screen.getByRole('button', { name: /set new password/i }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      /invalid or has expired/i,
    );
  });

  it('renders the detail string on 422 weak_password', async () => {
    apiPost.mockRejectedValueOnce(
      new ApiError(422, 'Unprocessable Entity', {
        error: 'weak_password',
        detail: 'Password must be at least 12 characters.',
      }),
    );
    render(<ResetPasswordPage />);

    await fillBothPasswordFields(STRONG_PASSWORD);
    fireEvent.click(screen.getByRole('button', { name: /set new password/i }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      /at least 12 characters/i,
    );
  });
});
