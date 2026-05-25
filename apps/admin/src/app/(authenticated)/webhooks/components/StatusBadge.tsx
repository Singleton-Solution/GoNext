'use client';

/**
 * <StatusBadge> renders the health pill the list view shows next to
 * each subscription.
 *
 * Brand: Living-Systems (#432). The pill maps to the brand palette
 * tokens directly via the shared <Badge> primitive so the colours stay
 * in sync with the rest of the surface:
 *
 *  - degraded_at set     → danger
 *  - active = false      → default (neutral)
 *  - last_delivery_status missing → outline ("Pending")
 *  - last_delivery_status = success → emerald ("Healthy")
 *  - last_delivery_status = retry   → lavender ("Retrying")
 *  - last_delivery_status = failed  → danger ("Failed")
 *
 * The pulse dot is on so an operator's eye is drawn to live state at
 * a glance.
 */
import type { ReactElement } from 'react';
import { Badge } from '@/components/ui/badge';
import type { Subscription } from '../types';

export interface StatusBadgeProps {
  subscription: Pick<Subscription, 'active' | 'degraded_at' | 'last_delivery_status'>;
}

type Variant = 'emerald' | 'lavender' | 'danger' | 'default' | 'outline';

function classify(sub: StatusBadgeProps['subscription']): {
  label: string;
  variant: Variant;
} {
  if (sub.degraded_at) return { label: 'Degraded', variant: 'danger' };
  if (!sub.active) return { label: 'Disabled', variant: 'default' };
  if (!sub.last_delivery_status) return { label: 'Pending', variant: 'outline' };
  switch (sub.last_delivery_status) {
    case 'success':
      return { label: 'Healthy', variant: 'emerald' };
    case 'retry':
      return { label: 'Retrying', variant: 'lavender' };
    case 'failed':
      return { label: 'Failed', variant: 'danger' };
    default:
      return { label: 'Unknown', variant: 'default' };
  }
}

export function StatusBadge({ subscription }: StatusBadgeProps): ReactElement {
  const { label, variant } = classify(subscription);
  return (
    <Badge
      role="status"
      aria-label={`Subscription status: ${label}`}
      variant={variant}
      dot
    >
      {label}
    </Badge>
  );
}
