/**
 * Tests for `<LockBanner>`.
 *
 * The banner is mostly presentational; we check that it surfaces the
 * holder's display name, an ETA, and the Refresh button when supplied.
 */
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { LockBanner } from './LockBanner.tsx';

describe('<LockBanner>', () => {
  it('renders the holder display name', () => {
    render(
      <LockBanner
        lockedBy={{
          userId: 'u-other',
          displayName: 'Alice',
          expiresAt: new Date(Date.now() + 60_000).toISOString(),
        }}
        nowFn={() => Date.now()}
      />,
    );
    expect(
      screen.getByTestId('autosave-lock-banner-holder').textContent,
    ).toBe('Alice');
  });

  it('formats ETA in seconds when under a minute', () => {
    const now = 1_700_000_000_000;
    render(
      <LockBanner
        lockedBy={{
          userId: 'u',
          displayName: 'A',
          expiresAt: new Date(now + 30_000).toISOString(),
        }}
        nowFn={() => now}
      />,
    );
    expect(
      screen.getByTestId('autosave-lock-banner-eta').textContent,
    ).toMatch(/30s/);
  });

  it('formats ETA in minutes + seconds when over a minute', () => {
    const now = 1_700_000_000_000;
    render(
      <LockBanner
        lockedBy={{
          userId: 'u',
          displayName: 'A',
          expiresAt: new Date(now + 90_000).toISOString(),
        }}
        nowFn={() => now}
      />,
    );
    expect(
      screen.getByTestId('autosave-lock-banner-eta').textContent,
    ).toMatch(/1m 30s/);
  });

  it('shows "expired" when ETA has passed', () => {
    const now = 1_700_000_000_000;
    render(
      <LockBanner
        lockedBy={{
          userId: 'u',
          displayName: 'A',
          expiresAt: new Date(now - 10_000).toISOString(),
        }}
        nowFn={() => now}
      />,
    );
    expect(
      screen.getByTestId('autosave-lock-banner-eta').textContent,
    ).toBe('expired');
  });

  it('fires onRefresh when Try Again clicked', async () => {
    const onRefresh = vi.fn();
    render(
      <LockBanner
        lockedBy={{
          userId: 'u',
          displayName: 'A',
          expiresAt: new Date(Date.now() + 30_000).toISOString(),
        }}
        onRefresh={onRefresh}
        nowFn={() => Date.now()}
      />,
    );
    const btn = screen.getByTestId('autosave-lock-banner-refresh');
    const user = userEvent.setup();
    await user.click(btn);
    expect(onRefresh).toHaveBeenCalled();
  });

  it('hides the refresh button when no callback supplied', () => {
    render(
      <LockBanner
        lockedBy={{
          userId: 'u',
          displayName: 'A',
          expiresAt: new Date(Date.now() + 30_000).toISOString(),
        }}
        nowFn={() => Date.now()}
      />,
    );
    expect(
      screen.queryByTestId('autosave-lock-banner-refresh'),
    ).toBeNull();
  });
});
