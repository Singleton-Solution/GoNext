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
 * Composition rule (mirrors the CSS handoff):
 *   • Outer h1/h2/h3/h4 → Archivo, font-weight 800, tight tracking.
 *     Display sizes use clamp() so very wide headlines gracefully
 *     shrink on narrower viewports.
 *   • Inner <em> → Instrument Serif, font-style italic, font-weight
 *     400, 1.05em scale-up so the serif optically matches the heavier
 *     sans. Colour:
 *         • on cream (default) → --emerald-deep
 *         • on forest (the heading itself has data-surface="forest",
 *           OR an ancestor element does) → --emerald-bright
 *     The colour swap is delivered via Tailwind data-attribute
 *     selectors so consumers don't have to thread the surface
 *     manually — wrapping a section in `<div data-surface="forest">`
 *     re-tunes every Headline inside it.
 *
 * Sizes pin to the handoff's display/page/section/sub scale:
 *   display → 96px (the giant hero headline)
 *   page    → 64px (per-route page heads)
 *   section → 44px
 *   sub     → 32px
 *
 * Use sparingly: one italic word per heading, max two. Emphasis, not
 * decoration.
 */
import * as React from 'react';
import { cva, type VariantProps } from 'class-variance-authority';

import { cn } from '@/lib/utils';

type HeadingTag = 'h1' | 'h2' | 'h3' | 'h4';

const headlineVariants = cva(
  cn(
    // Base — Archivo grotesque, extra-bold, tight tracking.
    'font-display font-extrabold tracking-tight m-0 text-ink',
    // Italic-accent rule: <em> children swap to Instrument Serif
    // italic, 400 weight, scaled +5% to optically match the sans.
    '[&_em]:font-serif [&_em]:italic [&_em]:font-normal',
    '[&_em]:text-[1.05em] [&_em]:tracking-[-0.01em]',
    // On cream surfaces (default) the accent is emerald-deep.
    '[&_em]:text-emerald-deep',
    // On a forest surface, both the headline text AND the italic
    // accent retune: text → fg-on-forest, accent → emerald-bright.
    // We cover two trigger paths:
    //   1. data-surface="forest" set directly on the Headline element
    //   2. any ancestor element carries data-surface="forest"
    'data-[surface=forest]:text-fg-on-forest',
    '[[data-surface=forest]_&]:text-fg-on-forest',
    'data-[surface=forest]:[&_em]:text-emerald-bright',
    '[[data-surface=forest]_&]:[&_em]:text-emerald-bright',
  ),
  {
    variants: {
      size: {
        // The hero size shrinks on narrow viewports without dropping
        // all the way to the next stop in the scale.
        display:
          'text-[clamp(44px,6.5vw,96px)] leading-[1.0]',
        page: 'text-[clamp(40px,5.5vw,64px)] leading-[1.0]',
        section: 'text-[44px] leading-[1.05]',
        sub: 'text-[32px] leading-[1.15]',
      },
    },
    defaultVariants: {
      size: 'page',
    },
  },
);

export interface HeadlineProps
  extends Omit<React.HTMLAttributes<HTMLHeadingElement>, 'color'>,
    VariantProps<typeof headlineVariants> {
  /**
   * Which heading element to render. Defaults to `h1` for the most
   * common case (per-page page heads). Use `h2` / `h3` / `h4` to keep
   * the DOM outline correct on nested headings — visual size is
   * controlled by `size`, not by the tag.
   */
  as?: HeadingTag;
  /**
   * Hint for the italic-accent colour. Cream uses --emerald-deep
   * (default); forest swaps to --emerald-bright. Setting this
   * explicitly is equivalent to the inherited `[data-surface=forest]`
   * ancestor rule and overrides it on the element directly.
   */
  surface?: 'cream' | 'forest';
}

const Headline = React.forwardRef<HTMLHeadingElement, HeadlineProps>(
  (
    { className, size, as = 'h1', surface, children, ...props },
    ref,
  ): React.ReactElement => {
    const Tag = as;
    // The `surface` prop maps to a data attribute so the
    // [data-surface=forest] selector in the variant catches both
    // prop-driven and ancestor-driven cases. Cream is the default,
    // so we only emit the attribute when forest is requested.
    const surfaceAttr =
      surface === 'forest' ? { 'data-surface': 'forest' } : {};
    return (
      <Tag
        ref={ref}
        {...surfaceAttr}
        className={cn(headlineVariants({ size }), className)}
        {...props}
      >
        {children}
      </Tag>
    );
  },
);
Headline.displayName = 'Headline';

export { Headline, headlineVariants };
