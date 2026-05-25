/**
 * Tooltip — small floating annotation.
 *
 * Forest-ink chip with paper text for crisp legibility, sized to
 * match the `.tag` chip in the handoff. Use TooltipProvider once
 * near the root of the surface that needs tooltips.
 */
'use client';

import * as React from 'react';
import * as TooltipPrimitive from '@radix-ui/react-tooltip';

import { cn } from '@/lib/utils';

const TooltipProvider = TooltipPrimitive.Provider;
const Tooltip = TooltipPrimitive.Root;
const TooltipTrigger = TooltipPrimitive.Trigger;

const TooltipContent = React.forwardRef<
  React.ElementRef<typeof TooltipPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof TooltipPrimitive.Content>
>(({ className, sideOffset = 6, ...props }, ref) => (
  <TooltipPrimitive.Content
    ref={ref}
    sideOffset={sideOffset}
    className={cn(
      'z-50 overflow-hidden rounded-sm bg-ink px-2 py-1 font-sans text-xs text-paper shadow-md',
      'data-[state=delayed-open]:animate-in data-[state=closed]:animate-out',
      'data-[state=delayed-open]:fade-in-0 data-[state=closed]:fade-out-0',
      className,
    )}
    {...props}
  />
));
TooltipContent.displayName = TooltipPrimitive.Content.displayName;

export { Tooltip, TooltipTrigger, TooltipContent, TooltipProvider };
