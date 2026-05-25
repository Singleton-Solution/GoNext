/**
 * StatusBadge — Living-Systems brand snapshot.
 *
 * Pins the variant mapping for every health classification:
 *  - Healthy  → emerald (success)
 *  - Retrying → lavender (the deliveries-domain accent)
 *  - Failed   → danger
 *  - Degraded → danger (regardless of last status)
 *  - Disabled → default (neutral)
 *  - Pending  → outline
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { StatusBadge } from './StatusBadge';

describe('StatusBadge — brand snapshot', () => {
  it('Healthy maps to the emerald variant', () => {
    render(
      <StatusBadge
        subscription={{ active: true, last_delivery_status: 'success' }}
      />,
    );
    const badge = screen.getByText('Healthy');
    expect(badge.className).toContain('bg-emerald-soft');
    expect(badge.className).toContain('text-emerald-deep');
  });

  it('Retrying maps to the lavender variant', () => {
    render(
      <StatusBadge
        subscription={{ active: true, last_delivery_status: 'retry' }}
      />,
    );
    const badge = screen.getByText('Retrying');
    expect(badge.className).toContain('bg-lavender-soft');
    expect(badge.className).toContain('text-lavender-deep');
  });

  it('Failed maps to the danger variant', () => {
    render(
      <StatusBadge
        subscription={{ active: true, last_delivery_status: 'failed' }}
      />,
    );
    const badge = screen.getByText('Failed');
    expect(badge.className).toContain('bg-danger-soft');
    expect(badge.className).toContain('text-danger');
  });

  it('Degraded maps to danger regardless of last status', () => {
    render(
      <StatusBadge
        subscription={{
          active: true,
          last_delivery_status: 'success',
          degraded_at: '2026-05-19T12:00:00Z',
        }}
      />,
    );
    const badge = screen.getByText('Degraded');
    expect(badge.className).toContain('bg-danger-soft');
  });

  it('Disabled maps to the default (neutral) variant', () => {
    render(<StatusBadge subscription={{ active: false }} />);
    const badge = screen.getByText('Disabled');
    expect(badge.className).toContain('bg-paper-3');
    expect(badge.className).toContain('text-fg-muted');
  });

  it('Pending maps to outline (no fill, neutral border)', () => {
    render(<StatusBadge subscription={{ active: true }} />);
    const badge = screen.getByText('Pending');
    expect(badge.className).toContain('bg-transparent');
  });

  it('renders the live pulse dot in every variant', () => {
    render(
      <StatusBadge
        subscription={{ active: true, last_delivery_status: 'success' }}
      />,
    );
    const badge = screen.getByText('Healthy');
    // <Badge dot> renders a <span aria-hidden> before the children.
    const dot = badge.querySelector('span[aria-hidden]');
    expect(dot).toBeTruthy();
  });
});
