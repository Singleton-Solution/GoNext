/**
 * NewTokenForm tests.
 *
 * Asserts:
 *   - Empty submissions surface a friendly error.
 *   - Picking scopes + a preset and submitting POSTs the expected body
 *     and calls onIssued with the returned IssuedTokenView.
 *   - API errors are surfaced inline rather than thrown.
 */
import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { NewTokenForm } from './NewTokenForm';
import type { IssuedTokenView } from '../types';

import { issueToken } from '../api';

// Mock the api module so fetch is never hit. The vi.mock call is
// hoisted by Vitest above the import statements, so this stays
// effective even though it textually follows the import.
vi.mock('../api', () => ({
  issueToken: vi.fn(),
}));

const issueMock = issueToken as unknown as ReturnType<typeof vi.fn>;

describe('NewTokenForm', () => {
  it('rejects empty name with an inline error', async () => {
    issueMock.mockReset();
    const onIssued = vi.fn();
    render(<NewTokenForm onIssued={onIssued} />);
    fireEvent.click(screen.getByTestId('scope-read'));
    fireEvent.submit(screen.getByTestId('new-token-form'));
    await waitFor(() => {
      expect(screen.getByTestId('new-token-error').textContent).toMatch(/memorable name/i);
    });
    expect(issueMock).not.toHaveBeenCalled();
  });

  it('rejects empty scope list', async () => {
    issueMock.mockReset();
    const onIssued = vi.fn();
    render(<NewTokenForm onIssued={onIssued} />);
    fireEvent.change(screen.getByTestId('new-token-name'), { target: { value: 'ci' } });
    fireEvent.submit(screen.getByTestId('new-token-form'));
    await waitFor(() => {
      expect(screen.getByTestId('new-token-error').textContent).toMatch(/at least one scope/i);
    });
    expect(issueMock).not.toHaveBeenCalled();
  });

  it('submits the canonical request body and forwards the response', async () => {
    issueMock.mockReset();
    const fake: IssuedTokenView = {
      id: '00000000-0000-7000-8000-000000000001',
      name: 'ci',
      prefix: 'AbCdEfGh',
      scopes: ['read'],
      effective_scopes: ['read'],
      created_at: '2026-01-01T00:00:00Z',
      plaintext: 'gnp_AbCdEfGh01234567890123456789ZyXw',
      save_now: true,
    };
    issueMock.mockResolvedValueOnce(fake);
    const onIssued = vi.fn();
    render(<NewTokenForm onIssued={onIssued} />);
    fireEvent.change(screen.getByTestId('new-token-name'), { target: { value: 'ci' } });
    fireEvent.click(screen.getByTestId('scope-read'));
    fireEvent.click(screen.getByTestId('expiry-90d'));
    fireEvent.submit(screen.getByTestId('new-token-form'));

    await waitFor(() => {
      expect(issueMock).toHaveBeenCalledWith({
        name: 'ci',
        scopes: ['read'],
        expires_in: '90d',
      });
    });
    expect(onIssued).toHaveBeenCalledWith(fake);
  });

  it('surfaces API errors inline', async () => {
    issueMock.mockReset();
    issueMock.mockRejectedValueOnce(new Error('boom'));
    render(<NewTokenForm onIssued={vi.fn()} />);
    fireEvent.change(screen.getByTestId('new-token-name'), { target: { value: 'ci' } });
    fireEvent.click(screen.getByTestId('scope-read'));
    fireEvent.submit(screen.getByTestId('new-token-form'));
    await waitFor(() => {
      expect(screen.getByTestId('new-token-error').textContent).toMatch(/boom/);
    });
  });
});
