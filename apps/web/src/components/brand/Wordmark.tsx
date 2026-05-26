/**
 * Wordmark — the composite "Go" (Archivo 800) + italic "Next"
 * (Instrument Serif italic). Same baseline, no space between, tracked
 * tight. Mirrors the brand-foot pattern in
 * docs/design/ui_kits/marketing/index.html and the wordmark helper
 * declared in tokens.css.
 *
 * The `surface` prop controls the colour swap on forest backgrounds —
 * "next" stays cream-paper-ish on cream, and lifts to emerald-bright
 * inside the dark footer + nav pill where the marketing kit highlights
 * the italic half of the wordmark.
 */
import * as React from 'react';

import { cn } from '@/lib/utils';

export interface WordmarkProps {
  /** Tag — defaults to span. Use <a> when wrapped in a link primitive. */
  as?: 'span' | 'div';
  /** Drives the colour swap. Cream is default. */
  surface?: 'cream' | 'forest';
  /**
   * Visual size. The dollar values map back to the marketing-kit
   * inline styles for nav (17/19), footer (17/20), and big-mark (any).
   */
  size?: 'sm' | 'md' | 'lg' | 'xl';
  className?: string;
}

const sizes: Record<NonNullable<WordmarkProps['size']>, string> = {
  sm: 'text-[15px] [&_.wm-next]:text-[17px]',
  md: 'text-[17px] [&_.wm-next]:text-[19px]',
  lg: 'text-[20px] [&_.wm-next]:text-[22px]',
  xl: 'text-[28px] [&_.wm-next]:text-[31px]',
};

export function Wordmark({
  as: Tag = 'span',
  surface = 'cream',
  size = 'md',
  className,
}: WordmarkProps): React.ReactElement {
  return (
    <Tag
      className={cn(
        'inline-flex items-baseline gap-[1px] leading-none tracking-tight',
        sizes[size],
        className,
      )}
      aria-label="GoNext"
    >
      <span
        className={cn(
          'wm-go font-display font-extrabold',
          surface === 'forest' ? 'text-fg-on-forest' : 'text-ink',
        )}
      >
        Go
      </span>
      <span
        className={cn(
          'wm-next font-serif italic font-normal',
          surface === 'forest' ? 'text-emerald-bright' : 'text-ink',
        )}
      >
        Next
      </span>
    </Tag>
  );
}
