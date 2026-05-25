/**
 * Class-name composition helper.
 *
 * `cn(...inputs)` merges arbitrary Tailwind class fragments, dropping
 * falsy values and de-duplicating conflicting utilities so that the
 * last value wins. This is the canonical helper used by the shadcn/ui
 * primitives that live under `src/components/ui/`.
 */
import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
