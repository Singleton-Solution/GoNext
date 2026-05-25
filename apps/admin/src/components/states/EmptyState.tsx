/**
 * EmptyState — "no items here yet" surface, brand-tokenized.
 *
 * The empty state is the moment a list confesses it has nothing to
 * show — and the design handoff treats it as a first-class brand
 * surface, not a fallback. Per `docs/design/ui_kits/states/index.html`
 * (the `.empty` recipe) the layout is:
 *
 *   • A 56×56 emerald-soft icon tile (with two faint concentric rings
 *     spaced 8 / 16px outside it — the "living rings" motif that
 *     visually marks this as a calm, growing-not-broken state).
 *   • A Headline (size="sub") with the italic-accent rule honoured —
 *     `<EmptyState title={<>Write your <em>first</em> post.</>}>`.
 *   • A short body in --fg-muted Geist, max ~360px wide.
 *   • Optional action(s) — typically a primary CTA + secondary.
 *
 * Voice rule (HANDOFF.md): "Confident, quiet, alive." Empty states
 * never apologise, never use exclamation, and never blame the user.
 * The copy is *invitational* — "an empty page is the best one".
 *
 * Surface tokens used:
 *   --paper-2   ← outer wrapper background (cream card)
 *   --paper     ← inner well, where the state actually lives
 *   --border    ← hairline
 *   --r-lg      ← outer corner radius (matches .card)
 *   --emerald-soft / --emerald-deep ← default icon tile
 *   --paper-3 / --fg-muted          ← search variant icon tile
 *
 * The component is intentionally surface-agnostic with respect to
 * forest dark backgrounds — empty states only appear on cream in this
 * brand. If a forest variant is ever needed we'll layer it on through
 * a `surface` prop without breaking this contract.
 */
import * as React from 'react';
import type { LucideIcon } from 'lucide-react';

import { cn } from '@/lib/utils';

export interface EmptyStateProps
  extends React.HTMLAttributes<HTMLDivElement> {
  /**
   * Lucide icon component to render at 26×26 inside the emerald-soft
   * tile. We accept the component reference (not a rendered element)
   * so we can size/colour it consistently across every empty state in
   * the product without callers having to remember the contract.
   */
  icon: LucideIcon;
  /**
   * Headline content. Pass JSX so the italic-accent rule still works:
   *   `title={<>Write your <em>first</em> post.</>}`
   * String children are accepted too — they'll render as a plain
   * heading without an accent.
   */
  title: React.ReactNode;
  /**
   * Body copy in Geist, --fg-muted. Keep it under ~30 words.
   */
  body: React.ReactNode;
  /**
   * Optional action region — typically one or two `<Button>` elements.
   * Rendered in an inline-flex row with 8px gap, centered under the
   * body. Omitted by default for read-only contexts.
   */
  action?: React.ReactNode;
  /**
   * Visual variant. `default` uses the emerald-soft icon tile (signals
   * "you haven't done this *yet* — go for it"). `search` uses the
   * neutral paper-3 tile (signals "this filter narrowed nothing — try
   * again"). Picked based on the user's intent at the moment they
   * landed in the empty state, not the type of list.
   */
  variant?: 'default' | 'search';
}

/**
 * The "living rings" — two faint squares 8/16px outside the icon
 * tile. They visually echo the icon's rounded square and reinforce the
 * "living systems" brand without adding visual weight. Pulled out into
 * its own component so the JSX in EmptyState stays readable.
 */
function LivingRings({ tone }: { tone: 'emerald' | 'neutral' }): React.ReactElement {
  const ringClass =
    tone === 'emerald'
      ? 'border border-emerald-soft'
      : 'border border-border-subtle';
  return (
    <>
      <span
        aria-hidden="true"
        className={cn(
          'pointer-events-none absolute -inset-2 rounded-md opacity-50',
          ringClass,
        )}
      />
      <span
        aria-hidden="true"
        className={cn(
          'pointer-events-none absolute -inset-4 rounded-md opacity-25',
          ringClass,
        )}
      />
    </>
  );
}

const EmptyState = React.forwardRef<HTMLDivElement, EmptyStateProps>(
  (
    {
      icon: Icon,
      title,
      body,
      action,
      variant = 'default',
      className,
      ...rest
    },
    ref,
  ): React.ReactElement => {
    const isSearch = variant === 'search';
    return (
      <div
        ref={ref}
        role="status"
        // The empty surface itself is a cream card with the inner well
        // on plain paper — matches the `.state` + `.state-body` split
        // in the handoff so a list switching from rows → empty doesn't
        // jolt the eye.
        className={cn(
          'flex flex-col overflow-hidden rounded-lg border border-border bg-paper-2 shadow-xs',
          className,
        )}
        data-testid="empty-state"
        data-variant={variant}
        {...rest}
      >
        <div
          className={cn(
            'flex flex-1 items-center justify-center bg-paper px-6 py-9',
            'min-h-[280px]',
          )}
        >
          <div className="max-w-[360px] text-center">
            <div className="relative mx-auto mb-[18px] flex h-14 w-14 items-center justify-center">
              <LivingRings tone={isSearch ? 'neutral' : 'emerald'} />
              <span
                className={cn(
                  'relative flex h-14 w-14 items-center justify-center rounded-md',
                  isSearch ? 'bg-paper-3' : 'bg-emerald-soft',
                )}
              >
                <Icon
                  // 26×26 is the canonical size from the handoff CSS
                  // (`.empty .ico i { width: 26px; height: 26px }`).
                  size={26}
                  strokeWidth={1.75}
                  className={cn(
                    isSearch ? 'text-fg-muted' : 'text-emerald-deep',
                  )}
                  aria-hidden="true"
                />
              </span>
            </div>
            {/*
              Headline composed inline so the italic-accent rule
              works out of the box on JSX titles. We hard-code the
              Archivo/serif treatment here (rather than nesting
              <Headline size="sub">) to lock the empty-state
              typography spec — 28px / 1.05 line-height — which
              differs slightly from <Headline>'s sub size (32px).
            */}
            <h3
              className={cn(
                'font-display text-[28px] font-extrabold leading-[1.05] tracking-tight text-ink',
                '[&_em]:font-serif [&_em]:italic [&_em]:font-normal',
                '[&_em]:text-[1.05em] [&_em]:tracking-[-0.01em] [&_em]:text-emerald-deep',
              )}
              data-testid="empty-state-title"
            >
              {title}
            </h3>
            <p
              className="mx-0 mb-[22px] mt-3 text-sm leading-[1.55] text-fg-muted"
              data-testid="empty-state-body"
            >
              {body}
            </p>
            {action ? (
              <div
                className="inline-flex gap-2"
                data-testid="empty-state-actions"
              >
                {action}
              </div>
            ) : null}
          </div>
        </div>
      </div>
    );
  },
);
EmptyState.displayName = 'EmptyState';

export { EmptyState };
