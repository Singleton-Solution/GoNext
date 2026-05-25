/**
 * Textarea — multi-line text input primitive on cream paper.
 *
 * Mirrors the `<Input>` rest/hover/focus contract so a textarea
 * sitting next to an input in a form reads as the same family:
 * paper-3 sunken surface, border-strong on hover, emerald focus
 * border + emerald-tinted halo on focus. The minimum height keeps
 * the textarea taller than a single Input row so its purpose is
 * legible at a glance; consumers can override with `className`.
 */
import * as React from 'react';

import { cn } from '@/lib/utils';

const Textarea = React.forwardRef<
  HTMLTextAreaElement,
  React.TextareaHTMLAttributes<HTMLTextAreaElement>
>(({ className, ...props }, ref) => {
  return (
    <textarea
      className={cn(
        'flex min-h-[80px] w-full rounded-md border border-border bg-paper-3 px-3 py-2 font-sans text-sm leading-normal text-ink',
        'placeholder:text-fg-faint',
        'transition-colors transition-shadow duration-[160ms] ease-brand',
        'hover:border-border-strong',
        'focus-visible:outline-none focus-visible:border-emerald focus-visible:shadow-focus',
        'disabled:cursor-not-allowed disabled:opacity-50',
        'resize-y',
        className,
      )}
      ref={ref}
      {...props}
    />
  );
});
Textarea.displayName = 'Textarea';

export { Textarea };
