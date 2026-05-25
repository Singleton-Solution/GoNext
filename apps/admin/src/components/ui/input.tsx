/**
 * Input — text-field primitive on cream paper.
 *
 * Mirrors `.input` from docs/design/colors_and_type.css and
 * docs/design/preview/comp-inputs.html. The rest-state surface is
 * paper-3 (sunken into the page), hover bumps to border-strong, focus
 * borders emerald and emits the emerald-tinted halo (--sh-focus). The
 * focus ring is what gives the input its "alive" feel — keep it on.
 */
import * as React from 'react';

import { cn } from '@/lib/utils';

const Input = React.forwardRef<
  HTMLInputElement,
  React.InputHTMLAttributes<HTMLInputElement>
>(({ className, type, ...props }, ref) => {
  return (
    <input
      type={type}
      className={cn(
        'flex h-9 w-full rounded-md border border-border bg-paper-3 px-3 py-1.5 font-sans text-sm leading-none text-ink',
        'placeholder:text-fg-faint',
        'transition-colors transition-shadow duration-[160ms] ease-brand',
        'hover:border-border-strong',
        'focus-visible:outline-none focus-visible:border-emerald focus-visible:shadow-focus',
        'disabled:cursor-not-allowed disabled:opacity-50',
        'file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-ink',
        className,
      )}
      ref={ref}
      {...props}
    />
  );
});
Input.displayName = 'Input';

export { Input };
