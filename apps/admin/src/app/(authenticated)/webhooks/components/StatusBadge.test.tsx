/**
 * StatusBadge — unit tests.
 *
 * Pure render test: assert that each input produces the expected label.
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { StatusBadge } from './StatusBadge';

describe('StatusBadge', () => {
  it('renders Healthy for an active subscription with last success', () => {
    render(
      <StatusBadge
        subscription={{ active: true, last_delivery_status: 'success' }}
      />,
    );
    expect(screen.getByText('Healthy')).toBeInTheDocument();
  });

  it('renders Disabled when active is false', () => {
    render(<StatusBadge subscription={{ active: false }} />);
    expect(screen.getByText('Disabled')).toBeInTheDocument();
  });

  it('renders Degraded when degraded_at is set, regardless of active', () => {
    render(
      <StatusBadge
        subscription={{
          active: true,
          last_delivery_status: 'success',
          degraded_at: '2026-05-19T12:00:00Z',
        }}
      />,
    );
    expect(screen.getByText('Degraded')).toBeInTheDocument();
  });

  it('renders Pending when there is no last_delivery_status', () => {
    render(<StatusBadge subscription={{ active: true }} />);
    expect(screen.getByText('Pending')).toBeInTheDocument();
  });

  it('renders Retrying for last_delivery_status = retry', () => {
    render(
      <StatusBadge
        subscription={{ active: true, last_delivery_status: 'retry' }}
      />,
    );
    expect(screen.getByText('Retrying')).toBeInTheDocument();
  });

  it('renders Failed for last_delivery_status = failed', () => {
    render(
      <StatusBadge
        subscription={{ active: true, last_delivery_status: 'failed' }}
      />,
    );
    expect(screen.getByText('Failed')).toBeInTheDocument();
  });
});
