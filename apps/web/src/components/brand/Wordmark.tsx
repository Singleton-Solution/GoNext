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
  /**
   * Optional site name override. The wordmark splits on the FIRST
   * space — the leading half renders in display-bold ("Go"-style), the
   * trailing half renders in italic serif ("Next"-style). If only one
   * word is supplied the whole name renders in display-bold and the
   * italic half is omitted. Defaults to the brand mark ("Go" + "Next").
   */
  name?: string;
}

/**
 * Split a site name into the "head" + "italic tail" the wordmark paints.
 * The first space is the seam — anything before is the bold display
 * half, anything after is the italic serif half. A single-word name
 * leaves the tail empty so the renderer paints just one span.
 */
function splitName(name: string): { head: string; tail: string } {
  const trimmed = name.trim();
  if (trimmed === '') return { head: 'Go', tail: 'Next' };
  const firstSpace = trimmed.indexOf(' ');
  if (firstSpace === -1) return { head: trimmed, tail: '' };
  return {
    head: trimmed.slice(0, firstSpace),
    tail: trimmed.slice(firstSpace + 1).trim(),
  };
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
  name,
}: WordmarkProps): React.ReactElement {
  const { head, tail } = splitName(name ?? 'Go Next');
  // The aria-label collapses to the configured site name so screen
  // readers get the same string the visual mark renders, rather than
  // the brand-default "GoNext".
  const ariaLabel = tail === '' ? head : `${head}${tail}`;
  return (
    <Tag
      className={cn(
        'inline-flex items-baseline gap-[1px] leading-none tracking-tight',
        sizes[size],
        className,
      )}
      aria-label={ariaLabel}
    >
      <span
        className={cn(
          'wm-go font-display font-extrabold',
          surface === 'forest' ? 'text-fg-on-forest' : 'text-ink',
        )}
      >
        {head}
      </span>
      {tail !== '' ? (
        <span
          className={cn(
            'wm-next font-serif italic font-normal',
            surface === 'forest' ? 'text-emerald-bright' : 'text-ink',
          )}
        >
          {tail}
        </span>
      ) : null}
    </Tag>
  );
}
