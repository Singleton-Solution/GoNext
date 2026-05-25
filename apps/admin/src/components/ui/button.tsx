/**
 * Button — primary actionable primitive, brand-tokenized.
 *
 * Variants mirror docs/design/preview/comp-buttons.html:
 *   - default  → paper-2 surface, ink border, soft xs shadow (the
 *                neutral CTA — "Save draft", form companions).
 *   - primary  → solid ink fill, paper text (the "main" CTA —
 *                "Deploy site").
 *   - emerald  → solid emerald fill, emerald-ink text (the
 *                positive/active CTA — "Start free", "Publish").
 *   - ghost    → transparent, fills with paper-3 on hover.
 *   - outline  → transparent, border-strong on hover. Pulled across
 *                from the legacy WordPress-parity admin surfaces that
 *                expect an outline variant.
 *   - link     → text-only, emerald-deep on cream.
 *   - destructive → danger fill, danger-soft hover. Used for delete
 *                modals and irreversible actions.
 *
 * Sizes mirror the .btn / .btn--sm / .btn--lg trio.
 *
 * The italic-accent rule does NOT apply to button labels — buttons
 * stay in Archivo for the same crisp grotesque tone as the surrounding
 * UI text. Headlines own the italic accents.
 */
'use client';

import * as React from 'react';
import { Slot } from '@radix-ui/react-slot';
import { cva, type VariantProps } from 'class-variance-authority';

import { cn } from '@/lib/utils';

const buttonVariants = cva(
  'inline-flex items-center justify-center gap-[6px] whitespace-nowrap rounded-md font-display font-bold text-sm leading-none transition-colors transition-shadow duration-[160ms] ease-brand focus-visible:outline-none focus-visible:shadow-focus disabled:pointer-events-none disabled:opacity-50',
  {
    variants: {
      variant: {
        default:
          'bg-paper-2 text-ink border border-border shadow-xs hover:bg-paper-3 hover:border-border-strong',
        primary:
          'bg-ink text-paper border border-ink shadow-xs hover:bg-forest-2 hover:border-forest-2',
        emerald:
          'bg-emerald text-emerald-ink border border-emerald shadow-xs hover:bg-emerald-deep hover:text-paper hover:border-emerald-deep',
        ghost:
          'bg-transparent text-fg-muted border-transparent hover:bg-paper-3 hover:text-ink',
        outline:
          'bg-transparent text-ink border border-border-strong hover:bg-paper-3 hover:border-ink',
        link: 'bg-transparent text-emerald-deep border-transparent underline-offset-4 hover:underline shadow-none',
        destructive:
          'bg-danger text-paper border border-danger shadow-xs hover:bg-danger/90',
      },
      size: {
        sm: 'px-[10px] py-[5px] text-xs rounded-sm',
        default: 'px-4 py-[9px] text-sm',
        lg: 'px-5 py-3 text-base',
        icon: 'h-9 w-9 p-0',
      },
    },
    defaultVariants: {
      variant: 'default',
      size: 'default',
    },
  },
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  /**
   * When true, render through Radix Slot — useful for wrapping a
   * `<Link>` from `next/link` while inheriting all button styling
   * without nesting `<button>` inside `<a>`.
   */
  asChild?: boolean;
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : 'button';
    return (
      <Comp
        className={cn(buttonVariants({ variant, size, className }))}
        ref={ref}
        {...props}
      />
    );
  },
);
Button.displayName = 'Button';

export { Button, buttonVariants };
