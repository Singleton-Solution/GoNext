/**
 * Headline — display-type primitive with italic-accent rule.
 *
 * The single most important brand primitive: every page heading in
 * GoNext composes Archivo's heavy grotesque with Instrument Serif's
 * editorial italic for the emphasized word(s). The handoff calls this
 * "the signature move" — see docs/design/HANDOFF.md ("The italic
 * accent rule") and the .display / h1 / h2 rules in
 * docs/design/colors_and_type.css.
 *
 * Mirrors the admin's apps/admin/src/components/ui/headline.tsx so the
 * same component contract is honored on both surfaces — admin pages
 * and the public marketing site use the same scale, the same colors,
 * and the same data-surface=forest swap.
 *
 * Composition rule:
 *   • Outer h1/h2/h3/h4 → Archivo, font-weight 800, tight tracking.
 *   • Inner <em> → Instrument Serif italic, 400, scaled +5%, colored
 *     emerald-deep on cream and emerald-bright on forest surfaces.
 *
 * Use sparingly: one italic word per heading, max two. Emphasis, not
 * decoration.
 */
import * as React from 'react';

import { cn } from '@/lib/utils';

type HeadingTag = 'h1' | 'h2' | 'h3' | 'h4';

type Size = 'display' | 'page' | 'section' | 'sub';

const sizeClasses: Record<Size, string> = {
  display: 'text-[clamp(44px,6.5vw,96px)] leading-[1.0]',
  page: 'text-[clamp(40px,5.5vw,64px)] leading-[1.0]',
  section: 'text-[44px] leading-[1.05]',
  sub: 'text-[32px] leading-[1.15]',
};

export interface HeadlineProps
  extends Omit<React.HTMLAttributes<HTMLHeadingElement>, 'color'> {
  as?: HeadingTag;
  size?: Size;
  /**
   * Cream is default (italic accent uses --emerald-deep). On forest
   * surfaces both the headline text and the italic accent retune —
   * setting this prop sets data-surface="forest" directly on the
   * element so the cascade catches it. The same swap fires
   * automatically when an ancestor carries data-surface="forest".
   */
  surface?: 'cream' | 'forest';
}

export const Headline = React.forwardRef<HTMLHeadingElement, HeadlineProps>(
  (
    {
      className,
      as = 'h1',
      size = 'page',
      surface,
      children,
      ...props
    },
    ref,
  ) => {
    const Tag = as;
    const base = cn(
      'font-display font-extrabold tracking-tight m-0 text-ink',
      // Italic-accent rule.
      '[&_em]:font-serif [&_em]:italic [&_em]:font-normal',
      '[&_em]:text-[1.05em] [&_em]:tracking-[-0.01em]',
      '[&_em]:text-emerald-deep',
      // Forest swap (prop OR ancestor).
      'data-[surface=forest]:text-fg-on-forest',
      '[[data-surface=forest]_&]:text-fg-on-forest',
      'data-[surface=forest]:[&_em]:text-emerald-bright',
      '[[data-surface=forest]_&]:[&_em]:text-emerald-bright',
    );
    const surfaceAttr =
      surface === 'forest' ? { 'data-surface': 'forest' } : {};
    return (
      <Tag
        ref={ref}
        {...surfaceAttr}
        className={cn(base, sizeClasses[size], className)}
        {...props}
      >
        {children}
      </Tag>
    );
  },
);
Headline.displayName = 'Headline';
