/**
 * Toaster — sonner wrapper themed for cream paper.
 *
 * Renders toasts using sonner's API but with brand-tokenized
 * surfaces so success / error / info land in the right
 * emerald / lavender / danger families. Mount once in the root
 * (authenticated) layout; consumers call `toast(...)` from `sonner`.
 */
'use client';

import * as React from 'react';
import { Toaster as Sonner } from 'sonner';

type ToasterProps = React.ComponentProps<typeof Sonner>;

function Toaster({ ...props }: ToasterProps): React.ReactElement {
  return (
    <Sonner
      theme="light"
      className="toaster group"
      toastOptions={{
        classNames: {
          toast:
            'group toast group-[.toaster]:bg-paper-2 group-[.toaster]:text-ink group-[.toaster]:border-border group-[.toaster]:shadow-md group-[.toaster]:rounded-md',
          description: 'group-[.toast]:text-fg-muted',
          actionButton:
            'group-[.toast]:bg-emerald group-[.toast]:text-emerald-ink group-[.toast]:rounded-sm group-[.toast]:font-display group-[.toast]:font-bold',
          cancelButton:
            'group-[.toast]:bg-paper-3 group-[.toast]:text-fg-muted group-[.toast]:rounded-sm',
          success:
            'group-[.toaster]:border-emerald-soft group-[.toaster]:bg-emerald-soft group-[.toaster]:text-emerald-deep',
          error:
            'group-[.toaster]:border-danger-soft group-[.toaster]:bg-danger-soft group-[.toaster]:text-danger',
          warning:
            'group-[.toaster]:border-warning-soft group-[.toaster]:bg-warning-soft group-[.toaster]:text-warning',
          info:
            'group-[.toaster]:border-lavender-soft group-[.toaster]:bg-lavender-soft group-[.toaster]:text-lavender-deep',
        },
      }}
      {...props}
    />
  );
}

export { Toaster };
