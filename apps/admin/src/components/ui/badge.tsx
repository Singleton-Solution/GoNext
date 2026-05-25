/**
 * Badge — short status / tag chip.
 *
 * Variants mirror `.tag` from docs/design/colors_and_type.css:
 *   - default  → paper-3 surface, fg-muted text, border
 *   - emerald  → emerald-soft surface, emerald-deep text
 *   - lavender → lavender-soft surface, lavender-deep text
 *   - success / warning / danger — semantic variants
 *   - ink      → inverse for use on light surfaces inside dark cards
 *
 * `dot` adds a leading pulse dot in the current text colour — useful
 * for live-status indicators ("Active", "Recording").
 */
import * as React from 'react';
import { cva, type VariantProps } from 'class-variance-authority';

import { cn } from '@/lib/utils';

const badgeVariants = cva(
  'inline-flex items-center gap-1 rounded-sm border px-2 py-[2px] font-sans text-xs font-medium leading-[1.5] transition-colors',
  {
    variants: {
      variant: {
        default: 'bg-paper-3 text-fg-muted border-border',
        emerald: 'bg-emerald-soft text-emerald-deep border-transparent',
        lavender: 'bg-lavender-soft text-lavender-deep border-transparent',
        success: 'bg-success-soft text-success border-transparent',
        warning: 'bg-warning-soft text-warning border-transparent',
        danger: 'bg-danger-soft text-danger border-transparent',
        ink: 'bg-ink text-paper border-transparent',
        outline: 'bg-transparent text-fg-muted border-border',
      },
    },
    defaultVariants: {
      variant: 'default',
    },
  },
);

export interface BadgeProps
  extends React.HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof badgeVariants> {
  /**
   * Render a leading pulse dot in the current text colour. Matches
   * `.tag--dot` from the handoff.
   */
  dot?: boolean;
}

function Badge({
  className,
  variant,
  dot = false,
  children,
  ...props
}: BadgeProps): React.ReactElement {
  return (
    <span className={cn(badgeVariants({ variant }), className)} {...props}>
      {dot ? (
        <span
          aria-hidden="true"
          className="h-[6px] w-[6px] flex-shrink-0 rounded-pill bg-current opacity-90"
        />
      ) : null}
      {children}
    </span>
  );
}

export { Badge, badgeVariants };
