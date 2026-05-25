/**
 * Class-name composition helper.
 *
 * Mirrors apps/admin/src/lib/utils.ts so the public site and admin
 * share the same conventional helper for stitching Tailwind class lists
 * together. `clsx` does the truthy-filter; `twMerge` resolves conflicts
 * between later Tailwind utilities so passing `className` from a
 * parent doesn't double-stack on the base variant.
 */
import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
