/**
 * Forgot-password page tests.
 *
 * Pin the wire contract end-to-end: a submitted email POSTs to
 * /api/v1/auth/password-reset/request and the page transitions to the
 * enumeration-safe success copy regardless of whether the API confirms
 * issuance. Also exercises the 429 error path.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';

vi.mock('next/navigation', () => ({
  usePathname: () => '/forgot-password',
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    prefetch: vi.fn(),
    refresh: vi.fn(),
  }),
  useSearchParams: () => new URLSearchParams(),
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

import ForgotPasswordPage from './page';
import { ApiError } from '@/lib/api-client';

describe('ForgotPasswordPage', () => {
  beforeEach(() => {
    apiPost.mockReset();
  });

  it('POSTs to the reset-request endpoint and shows the success state', async () => {
    apiPost.mockResolvedValueOnce({});
    render(<ForgotPasswordPage />);

    fireEvent.change(screen.getByLabelText(/email/i), {
      target: { value: 'alice@example.com' },
    });
    fireEvent.click(screen.getByRole('button', { name: /send reset link/i }));

    await waitFor(() => {
      expect(apiPost).toHaveBeenCalledWith(
        '/api/v1/auth/password-reset/request',
        { email: 'alice@example.com' },
      );
    });
    expect(
      await screen.findByText(/if an account exists/i),
    ).toBeInTheDocument();
  });

  it('shows the enumeration-safe success copy for an unknown email too', async () => {
    apiPost.mockResolvedValueOnce({});
    render(<ForgotPasswordPage />);

    fireEvent.change(screen.getByLabelText(/email/i), {
      target: { value: 'ghost@example.com' },
    });
    fireEvent.click(screen.getByRole('button', { name: /send reset link/i }));

    expect(
      await screen.findByText(/if an account exists/i),
    ).toBeInTheDocument();
  });

  it('renders a friendly error when the API returns 429', async () => {
    apiPost.mockRejectedValueOnce(
      new ApiError(429, 'Too Many Requests', { error: 'rate_limited' }),
    );
    render(<ForgotPasswordPage />);

    fireEvent.change(screen.getByLabelText(/email/i), {
      target: { value: 'alice@example.com' },
    });
    fireEvent.click(screen.getByRole('button', { name: /send reset link/i }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      /too many attempts/i,
    );
  });
});
