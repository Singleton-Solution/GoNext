'use client';

/**
 * <StatusBadge> renders the health pill the list view shows next to
 * each subscription. The classification rules:
 *
 *  - degraded_at set → red "Degraded" badge. Operators should look.
 *  - active = false → grey "Disabled" badge.
 *  - last_delivery_status missing → blue "Pending" — never delivered.
 *  - last_delivery_status === 'success' → green "Healthy".
 *  - last_delivery_status === 'retry' → amber "Retrying".
 *  - last_delivery_status === 'failed' → red "Failed".
 *
 * The component is intentionally string-only (no icon dependency) so
 * the badge stays readable in monochrome contexts.
 */
import type { ReactElement } from 'react';
import type { Subscription } from '../types';

export interface StatusBadgeProps {
  subscription: Pick<Subscription, 'active' | 'degraded_at' | 'last_delivery_status'>;
}

type Tone = 'success' | 'warn' | 'danger' | 'neutral' | 'info';

function classify(sub: StatusBadgeProps['subscription']): {
  label: string;
  tone: Tone;
} {
  if (sub.degraded_at) return { label: 'Degraded', tone: 'danger' };
  if (!sub.active) return { label: 'Disabled', tone: 'neutral' };
  if (!sub.last_delivery_status) return { label: 'Pending', tone: 'info' };
  switch (sub.last_delivery_status) {
    case 'success':
      return { label: 'Healthy', tone: 'success' };
    case 'retry':
      return { label: 'Retrying', tone: 'warn' };
    case 'failed':
      return { label: 'Failed', tone: 'danger' };
    default:
      return { label: 'Unknown', tone: 'neutral' };
  }
}

const TONE_STYLES: Record<Tone, { background: string; color: string }> = {
  success: { background: '#d6f5d6', color: '#16591a' },
  warn: { background: '#fff4d6', color: '#7a5300' },
  danger: { background: '#fbd5d5', color: '#7a1f1f' },
  neutral: { background: '#eaeaea', color: '#444' },
  info: { background: '#d6eaff', color: '#1e3a8a' },
};

export function StatusBadge({ subscription }: StatusBadgeProps): ReactElement {
  const { label, tone } = classify(subscription);
  const style = TONE_STYLES[tone];
  return (
    <span
      role="status"
      aria-label={`Subscription status: ${label}`}
      style={{
        display: 'inline-block',
        padding: '2px 8px',
        borderRadius: 999,
        fontSize: 12,
        fontWeight: 600,
        background: style.background,
        color: style.color,
        lineHeight: 1.5,
      }}
    >
      {label}
    </span>
  );
}
