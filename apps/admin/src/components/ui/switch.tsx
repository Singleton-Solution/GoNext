/**
 * Switch — on/off toggle.
 *
 * Track is paper-3 when off and emerald when on (the brand's
 * "positive / active" colour, same as Button[emerald]). Thumb is
 * paper-2 on either side so the contrast stays readable.
 */
'use client';

import * as React from 'react';
import * as SwitchPrimitives from '@radix-ui/react-switch';

import { cn } from '@/lib/utils';

const Switch = React.forwardRef<
  React.ElementRef<typeof SwitchPrimitives.Root>,
  React.ComponentPropsWithoutRef<typeof SwitchPrimitives.Root>
>(({ className, ...props }, ref) => (
  <SwitchPrimitives.Root
    className={cn(
      'peer inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-pill border border-transparent',
      'transition-colors ease-brand duration-[160ms]',
      'focus-visible:outline-none focus-visible:shadow-focus',
      'disabled:cursor-not-allowed disabled:opacity-50',
      'data-[state=checked]:bg-emerald data-[state=unchecked]:bg-paper-4',
      className,
    )}
    {...props}
    ref={ref}
  >
    <SwitchPrimitives.Thumb
      className={cn(
        'pointer-events-none block h-4 w-4 rounded-pill bg-paper shadow-sm ring-0',
        'transition-transform ease-brand duration-[160ms]',
        'data-[state=checked]:translate-x-4 data-[state=unchecked]:translate-x-0.5',
      )}
    />
  </SwitchPrimitives.Root>
));
Switch.displayName = SwitchPrimitives.Root.displayName;

export { Switch };
