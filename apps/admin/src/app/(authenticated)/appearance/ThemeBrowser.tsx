'use client';

/**
 * ThemeBrowser — interactive theme gallery.
 *
 * Renders the curated theme list as a 3-column card grid. The active
 * theme card gets an emerald-bright border + corner badge so the
 * operator can tell at a glance which one is currently live. Hovering
 * any card lifts it with the same soft shadow + 2px translate that
 * every other card surface in the brand uses.
 *
 * Each card has three pieces:
 *   • Preview — an in-browser CSS scene of the template, matching the
 *     `frame.*` styles from docs/design/ui_kits/templates/index.html.
 *     No bitmap assets; the previews render identically in every env.
 *   • Title block — Archivo display name + italic-accent description.
 *   • Footer — tags (left) and an emerald "Activate" button (right).
 *
 * The "Activate" CTA is wired to a stub `onActivate` handler that
 * updates local state and surfaces a toast. The real backend wire-up
 * lands when the themes-list endpoint ships in a follow-up issue —
 * the card grid itself is what we ship now.
 */
import { useState, type ReactElement } from 'react';
import { ArrowRight, Check, Sparkles } from 'lucide-react';
import { Headline } from '@/components/ui/headline';
import { cn } from '@/lib/utils';

/** A single theme entry as rendered in the gallery. */
export interface ThemeCard {
  /** Stable slug — matches the on-disk theme directory. */
  slug: string;
  /** Display name (Archivo, no italic accents allowed). */
  name: string;
  /** One-line description. Markdown-flavour `*word*` becomes an
   *  italic-serif accent on render. */
  description: string;
  /** Short labels for the foot row — keep to 1-2 per theme. */
  tags: readonly string[];
  /** Which preview frame to render. The strings mirror the
   *  `.frame.<variant>` classes in the templates ui_kit. */
  preview: 'editorial' | 'shop' | 'studio' | 'portfolio' | 'docs';
}

export interface ThemeBrowserProps {
  themes: readonly ThemeCard[];
  /** Slug of the currently-active theme. The matching card gets the
   *  emerald border + "Active" badge treatment. */
  activeSlug: string;
}

export function ThemeBrowser({
  themes,
  activeSlug: initialActiveSlug,
}: ThemeBrowserProps): ReactElement {
  // We mirror the active slug in local state so the Activate button
  // can give immediate feedback while the backend wiring is stubbed.
  // The server-rendered initial value is still the source of truth on
  // first paint.
  const [activeSlug, setActiveSlug] = useState(initialActiveSlug);

  return (
    <section
      aria-labelledby="appearance-heading"
      data-testid="theme-browser"
      className="flex flex-col gap-12 pb-16"
    >
      <header className="flex flex-col gap-3 border-b border-border pb-8">
        <span className="font-sans text-2xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
          Appearance · {themes.length} themes installed
        </span>
        <Headline as="h1" size="page" id="appearance-heading">
          Themes &amp; <em>appearance</em>.
        </Headline>
        <p className="max-w-[640px] text-md leading-normal text-fg-muted">
          Every theme is a living system — pick the one that fits the shape of
          your site today, then bend it with the customizer. Switching is one
          click; nothing in your content moves.
        </p>
      </header>

      <div
        role="list"
        className="grid grid-cols-1 gap-5 md:grid-cols-2 lg:grid-cols-3"
        data-testid="theme-grid"
      >
        {themes.map((theme) => {
          const isActive = theme.slug === activeSlug;
          return (
            <ThemeBrowserCard
              key={theme.slug}
              theme={theme}
              isActive={isActive}
              onActivate={() => setActiveSlug(theme.slug)}
            />
          );
        })}
      </div>
    </section>
  );
}

interface ThemeBrowserCardProps {
  theme: ThemeCard;
  isActive: boolean;
  onActivate: () => void;
}

function ThemeBrowserCard({
  theme,
  isActive,
  onActivate,
}: ThemeBrowserCardProps): ReactElement {
  return (
    <article
      role="listitem"
      data-testid={`theme-card-${theme.slug}`}
      data-active={isActive || undefined}
      className={cn(
        // Base card — paper-2 surface, hairline border, soft shadow.
        'group relative flex flex-col overflow-hidden rounded-lg border bg-paper-2 transition-all duration-[160ms] ease-brand',
        'shadow-xs hover:-translate-y-[2px] hover:shadow-md',
        isActive
          ? 'border-emerald-bright ring-2 ring-emerald-bright/35'
          : 'border-border hover:border-border-strong',
      )}
    >
      {isActive && (
        <span
          data-testid={`theme-badge-${theme.slug}`}
          className="absolute right-3 top-3 z-10 inline-flex items-center gap-1 rounded-pill bg-emerald px-2 py-[3px] font-sans text-2xs font-semibold uppercase tracking-wider text-emerald-ink shadow-xs"
        >
          <Sparkles aria-hidden className="h-3 w-3" />
          Active
        </span>
      )}

      <ThemePreview variant={theme.preview} />

      <div className="flex flex-1 flex-col gap-3 p-5">
        <div className="flex items-baseline justify-between gap-3">
          <h2 className="font-display text-xl font-bold tracking-tight text-ink">
            {theme.name}
          </h2>
          <span className="font-sans text-2xs font-medium uppercase tracking-[0.08em] text-fg-subtle">
            {theme.tags[0] ?? 'Theme'}
          </span>
        </div>
        <p className="text-sm leading-normal text-fg-muted [&_em]:font-serif [&_em]:italic [&_em]:font-normal [&_em]:text-emerald-deep">
          <RenderAccented text={theme.description} />
        </p>

        <div className="mt-auto flex items-center justify-between gap-3 border-t border-border pt-4">
          <div className="flex flex-wrap gap-1">
            {theme.tags.map((tag) => (
              <span
                key={tag}
                className="inline-flex items-center rounded-sm border border-border bg-paper-3 px-2 py-[2px] font-sans text-2xs font-medium text-fg-muted"
              >
                {tag}
              </span>
            ))}
          </div>
          <button
            type="button"
            onClick={onActivate}
            disabled={isActive}
            data-testid={`theme-activate-${theme.slug}`}
            className={cn(
              'inline-flex items-center gap-[6px] rounded-md font-display text-xs font-bold leading-none transition-colors duration-[160ms] ease-brand',
              'px-3 py-[7px] shadow-xs focus-visible:outline-none focus-visible:shadow-focus',
              isActive
                ? 'cursor-default bg-emerald-soft text-emerald-deep'
                : 'bg-emerald text-emerald-ink hover:bg-emerald-deep hover:text-paper',
            )}
            aria-label={isActive ? `${theme.name} is the active theme` : `Activate ${theme.name}`}
          >
            {isActive ? (
              <>
                <Check aria-hidden className="h-3 w-3" />
                Active
              </>
            ) : (
              <>
                Activate
                <ArrowRight aria-hidden className="h-3 w-3" />
              </>
            )}
          </button>
        </div>
      </div>
    </article>
  );
}

/**
 * Renders one of the brand's mini-template scenes. Each variant is a
 * pixel-tight CSS scene; no images, no fetches.
 */
function ThemePreview({ variant }: { variant: ThemeCard['preview'] }): ReactElement {
  const wrapperClass =
    'relative aspect-[4/3] overflow-hidden border-b border-border bg-paper';
  return (
    <div className={wrapperClass} data-preview={variant}>
      <div className="absolute inset-3 flex flex-col overflow-hidden rounded-sm border border-border shadow-sm">
        <div className="flex gap-1 border-b border-border bg-paper-3 px-2 py-[5px]">
          <span className="block h-[6px] w-[6px] rounded-full bg-fg-faint/60" />
          <span className="block h-[6px] w-[6px] rounded-full bg-fg-faint/60" />
          <span className="block h-[6px] w-[6px] rounded-full bg-fg-faint/60" />
        </div>
        <div className="flex-1 overflow-hidden">
          {variant === 'editorial' && <EditorialFrame />}
          {variant === 'shop' && <ShopFrame />}
          {variant === 'studio' && <StudioFrame />}
          {variant === 'portfolio' && <PortfolioFrame />}
          {variant === 'docs' && <DocsFrame />}
        </div>
      </div>
    </div>
  );
}

function EditorialFrame(): ReactElement {
  return (
    <div className="flex h-full flex-col gap-1.5 bg-paper p-3.5">
      <span className="font-sans text-[8px] font-medium uppercase tracking-[0.14em] text-emerald-deep">
        {'// 02 — Sourcing'}
      </span>
      <span className="font-display text-[18px] font-extrabold leading-none tracking-tight text-ink">
        Single-
        <em className="font-serif text-[1.05em] font-normal italic text-emerald-deep">
          origin
        </em>
        .
      </span>
      <span className="text-[8px] leading-[1.5] text-fg-muted">
        Five years, eleven trips, a few thousand cups later.
      </span>
      <div
        className="mt-1 h-9 overflow-hidden rounded-[3px]"
        style={{
          background:
            'linear-gradient(135deg, #1F2D26 0%, #0E1A14 100%)',
        }}
      >
        <div
          aria-hidden
          className="h-full w-full"
          style={{
            backgroundImage:
              'radial-gradient(circle at 30% 60%, rgba(52, 211, 153, 0.4) 0%, transparent 40%), radial-gradient(circle at 70% 20%, rgba(167, 139, 250, 0.3) 0%, transparent 40%)',
          }}
        />
      </div>
      <div className="mt-1 h-[5px] w-4/5 rounded-[2px] bg-paper-3" />
      <div className="h-[5px] w-1/2 rounded-[2px] bg-paper-3" />
    </div>
  );
}

function ShopFrame(): ReactElement {
  return (
    <div className="grid h-full grid-cols-2 content-start gap-1.5 bg-paper p-3">
      <div className="col-span-2 font-display text-[13px] font-extrabold tracking-tight text-ink">
        Brick &amp;{' '}
        <em className="font-serif font-normal italic text-emerald-deep">
          Mortar
        </em>
        .
      </div>
      {[
        { name: 'Roaster v2', price: '$420', feat: false },
        { name: 'Burr Grinder', price: '$180', feat: true },
        { name: 'Bag · 250g', price: '$22', feat: false },
        { name: 'Subscription', price: '$18/mo', feat: false },
      ].map((p) => (
        <div
          key={p.name}
          className="flex flex-col gap-[3px] rounded-[4px] border border-border bg-paper-2 p-1.5"
        >
          <div
            className="relative h-6 overflow-hidden rounded-[2px] bg-forest"
            aria-hidden
          >
            <div
              className="absolute inset-0"
              style={{
                background: p.feat
                  ? 'radial-gradient(circle at 60% 50%, rgba(167, 139, 250, 0.5) 0%, transparent 60%)'
                  : 'radial-gradient(circle at 60% 50%, rgba(52, 211, 153, 0.4) 0%, transparent 60%)',
              }}
            />
          </div>
          <span className="text-[7px] font-semibold text-ink">{p.name}</span>
          <span className="font-mono text-[7px] text-fg-muted">{p.price}</span>
        </div>
      ))}
    </div>
  );
}

function StudioFrame(): ReactElement {
  return (
    <div
      className="relative h-full overflow-hidden bg-forest p-3.5 text-fg-on-forest"
      aria-hidden={false}
    >
      <div
        className="absolute"
        style={{
          bottom: '-50%',
          right: '-30%',
          width: '200px',
          height: '200px',
          background:
            'radial-gradient(circle, rgba(16, 185, 129, 0.25) 0%, transparent 60%)',
        }}
      />
      <span className="relative font-sans text-[8px] font-medium uppercase tracking-[0.14em] text-emerald-bright">
        {'// 04 — Studio'}
      </span>
      <div className="relative mt-1.5 font-display text-[19px] font-extrabold leading-none tracking-tight">
        Things we{' '}
        <em className="font-serif font-normal italic text-emerald-bright">
          made
        </em>
        .
      </div>
      <div className="relative mt-2.5 grid grid-cols-3 gap-[3px]">
        {Array.from({ length: 9 }).map((_, i) => (
          <span
            key={i}
            className={cn(
              'aspect-square rounded-[2px]',
              i === 0 ? 'bg-emerald' : i === 4 ? 'bg-lavender' : 'bg-forest-2',
            )}
          />
        ))}
      </div>
    </div>
  );
}

function PortfolioFrame(): ReactElement {
  return (
    <div className="h-full bg-emerald-soft p-3.5">
      <span className="font-display text-[17px] font-extrabold leading-none tracking-tight text-forest">
        Mira{' '}
        <em className="font-serif font-normal italic text-emerald-deep">
          Okafor
        </em>
      </span>
      <span className="mt-1.5 block font-sans text-[8px] font-medium uppercase tracking-[0.14em] text-emerald-deep">
        Designer · Brooklyn
      </span>
      <div className="mt-3 space-y-[5px] text-[8px] leading-[1.7] text-forest">
        <span className="block">01. Slowdown · 2024</span>
        <span className="block">02. Habit garden · 2024</span>
        <span className="block">03. Counter app · 2023</span>
        <span className="block">04. Field notes · 2023</span>
      </div>
    </div>
  );
}

function DocsFrame(): ReactElement {
  return (
    <div className="grid h-full grid-cols-[64px_1fr] bg-paper">
      <div className="flex flex-col gap-[3px] border-r border-border bg-paper-2 px-1.5 py-2">
        {['Get started', 'Install', 'Concepts', 'API'].map((t, i) => (
          <span
            key={t}
            className={cn(
              'rounded-[2px] px-1.5 py-[3px] text-[7px]',
              i === 1
                ? 'bg-ink font-medium text-paper'
                : 'text-fg-muted',
            )}
          >
            {t}
          </span>
        ))}
      </div>
      <div className="p-2.5">
        <div className="font-display text-[14px] font-extrabold tracking-tight text-ink">
          Install in{' '}
          <em className="font-serif font-normal italic text-emerald-deep">
            five
          </em>
          .
        </div>
        <p className="mt-1.5 text-[7px] leading-[1.6] text-fg-muted">
          Run one command, point your DNS, and you&apos;re live. The same
          binary serves your content and your dashboard.
        </p>
      </div>
    </div>
  );
}

/**
 * Tiny markdown-flavour parser for the description field — `*word*`
 * becomes a serif italic emerald-deep emphasis. We deliberately keep
 * this very small (one-pass, regex split) rather than dragging in a
 * full markdown lib for two characters.
 */
function RenderAccented({ text }: { text: string }): ReactElement {
  const parts = text.split(/(\*[^*]+\*)/g);
  return (
    <>
      {parts.map((part, i) => {
        if (part.startsWith('*') && part.endsWith('*') && part.length > 2) {
          return (
            <em
              key={i}
              className="font-serif italic font-normal text-emerald-deep"
            >
              {part.slice(1, -1)}
            </em>
          );
        }
        return <span key={i}>{part}</span>;
      })}
    </>
  );
}
