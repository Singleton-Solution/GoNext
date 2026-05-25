/**
 * Card — surface primitive on cream paper.
 *
 * Mirrors `.card` from docs/design/colors_and_type.css and the
 * docs/design/preview/comp-cards.html prototypes: paper-2 background,
 * 1px border, --r-lg radius, --sh-xs resting shadow. Hover variants
 * lift to sh-md with a 2px translate — see `.card-hover` in the
 * preview for the lift behaviour; consumers opt in by composing
 * `hover:shadow-md hover:-translate-y-[2px]` on the root.
 *
 * Subcomponents (Header / Title / Description / Content / Footer)
 * follow the shadcn convention so existing snippets that compose with
 * the standard slot names work without modification.
 */
import * as React from 'react';

import { cn } from '@/lib/utils';

const Card = React.forwardRef<
  HTMLDivElement,
  React.HTMLAttributes<HTMLDivElement>
>(({ className, ...props }, ref) => (
  <div
    ref={ref}
    className={cn(
      'bg-paper-2 text-ink rounded-lg border border-border shadow-xs',
      className,
    )}
    {...props}
  />
));
Card.displayName = 'Card';

const CardHeader = React.forwardRef<
  HTMLDivElement,
  React.HTMLAttributes<HTMLDivElement>
>(({ className, ...props }, ref) => (
  <div
    ref={ref}
    className={cn('flex flex-col gap-[6px] p-6', className)}
    {...props}
  />
));
CardHeader.displayName = 'CardHeader';

const CardTitle = React.forwardRef<
  HTMLDivElement,
  React.HTMLAttributes<HTMLDivElement>
>(({ className, ...props }, ref) => (
  <div
    ref={ref}
    className={cn(
      'font-display text-xl font-bold leading-snug tracking-tight text-ink [&_em]:font-serif [&_em]:italic [&_em]:font-normal [&_em]:text-emerald-deep',
      className,
    )}
    {...props}
  />
));
CardTitle.displayName = 'CardTitle';

const CardDescription = React.forwardRef<
  HTMLDivElement,
  React.HTMLAttributes<HTMLDivElement>
>(({ className, ...props }, ref) => (
  <div
    ref={ref}
    className={cn('font-sans text-sm text-fg-muted', className)}
    {...props}
  />
));
CardDescription.displayName = 'CardDescription';

const CardContent = React.forwardRef<
  HTMLDivElement,
  React.HTMLAttributes<HTMLDivElement>
>(({ className, ...props }, ref) => (
  <div
    ref={ref}
    className={cn('px-6 pb-6 pt-0 text-ink-soft', className)}
    {...props}
  />
));
CardContent.displayName = 'CardContent';

const CardFooter = React.forwardRef<
  HTMLDivElement,
  React.HTMLAttributes<HTMLDivElement>
>(({ className, ...props }, ref) => (
  <div
    ref={ref}
    className={cn('flex items-center px-6 pb-6 pt-0', className)}
    {...props}
  />
));
CardFooter.displayName = 'CardFooter';

export {
  Card,
  CardHeader,
  CardFooter,
  CardTitle,
  CardDescription,
  CardContent,
};
