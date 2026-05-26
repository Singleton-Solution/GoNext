/**
 * `<BlockToolbar>` — composable toolbar surfaced next to a selected
 * block.
 *
 * The original `<BlockTransformToolbar>` shipped a single concern (the
 * Transform-to dropdown) bolted directly to the canvas. As the editor
 * grew more behaviours (locks indicator, alignment chip, AI rewrite
 * trigger, …) every new item meant another sibling component the
 * canvas had to render in a specific order. This file replaces that
 * pattern with a single `<BlockToolbar>` that takes an ordered list of
 * `ToolbarAction`s — each describing one item in the toolbar — and
 * renders them in a row with consistent brand styling.
 *
 * The two first-class providers ship in this file:
 *
 *   - {@link transformActionProvider} — adapts a `TransformRegistry`
 *     into one "transform-to" `ToolbarAction` whose `onSelect` opens
 *     a dropdown of available transforms. Reproduces the original
 *     `<BlockTransformToolbar>` behaviour as a single action.
 *
 *   - {@link lockActionProvider} — emits a read-only `ToolbarAction`
 *     surfacing the existing `<BlockLockIndicator>` chip whenever the
 *     block is locked. The action's `onSelect` is a noop because the
 *     chip is informational (clicks go to the lock inspector tab, not
 *     the toolbar).
 *
 * Both providers are functions of `(block) => ToolbarAction[]` so the
 * toolbar can flatten them with a `.flatMap` and pass the result as
 * `actions={...}`. Adding a new behaviour is a matter of writing one
 * more provider; nothing in the toolbar changes.
 *
 * Backwards-compat: `<BlockTransformToolbar>` is kept around as a
 * thin wrapper over a single-provider `<BlockToolbar>` so existing
 * callers keep working unchanged.
 */
'use client';

import type { Block } from '@gonext/blocks-sdk';
import { useState, type CSSProperties, type ReactNode } from 'react';
import { BlockLockIndicator } from './block-lock-indicator.tsx';
import { isLocked } from './locks.ts';
import type {
  Transform,
  TransformRegistry,
} from './transform-types.ts';

/** One item in the toolbar.
 *
 *  `kind` is the rendering hint:
 *
 *    - `'button'`  — clickable pill that fires `onSelect` on activate.
 *                    The default.
 *    - `'dropdown'`— button + popover panel; `onSelect` is fired with
 *                    the selected option's id, and `renderPanel` provides
 *                    the panel content. Used by the transform provider.
 *    - `'chip'`    — read-only indicator (no click handler wired); used
 *                    by the lock provider.
 *
 *  Providers stick to these three so the toolbar's render switch stays
 *  small. New shapes can land later; do not push styling concerns into
 *  the action shape — the toolbar owns the surface treatment so the row
 *  reads as one component.
 */
export interface ToolbarAction {
  /** Stable id used as the React key and to disambiguate `onSelect`
   *  callbacks. Required. */
  id: string;
  /** Human label shown on the button / chip. */
  label: string;
  /** Optional visual lead. May be a glyph component, a string, or any
   *  ReactNode — the toolbar slots it in front of the label. */
  icon?: ReactNode;
  /** Optional dropdown / panel content. Required when `kind` is
   *  `'dropdown'`; ignored otherwise. The function receives a close
   *  handler so panel items can collapse the popover after a
   *  selection. */
  renderPanel?: (close: () => void) => ReactNode;
  /** Fired when the action is activated. Receives an optional `option`
   *  id — set by the dropdown's panel items when one is picked. */
  onSelect?: (option?: string) => void;
  /** Disables the button. The toolbar greys the chrome but still
   *  shows the action so the operator sees the full row. */
  disabled?: boolean;
  /** Rendering hint. Defaults to `'button'`. */
  kind?: 'button' | 'dropdown' | 'chip';
  /** Optional accessible label override. Defaults to `label`. */
  ariaLabel?: string;
}

/** A provider produces zero or more actions for the given block. The
 *  toolbar flattens the list across providers and renders the result.
 *  Returning an empty array is the idiomatic way to say "this provider
 *  has nothing to contribute for this block" (e.g. the locks provider
 *  on an unlocked block). */
export type ActionProvider = (block: Block) => ToolbarAction[];

export interface BlockToolbarProps {
  /** The selected block. Passed through to providers when actions is
   *  not pre-flattened. */
  block: Block;
  /** Pre-flattened action list, or a list of providers the toolbar
   *  flattens itself. Either form works; tests and small callers can
   *  pass `actions={[...]}` directly. */
  actions?: ToolbarAction[];
  /** Provider list — the composable form. The toolbar flattens these
   *  by calling each provider with `block` and concatenating the
   *  results. If both `actions` and `providers` are supplied, the
   *  toolbar concatenates them — `actions` first. */
  providers?: ActionProvider[];
  /** Optional test id override. */
  testId?: string;
}

/* ─── Styling (Living systems / docs/design/HANDOFF.md) ──────────────── */

const wrapperStyle: CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 'var(--s-2, 6px)',
  fontFamily:
    "var(--font-sans, 'Geist', -apple-system, system-ui, sans-serif)",
};

const buttonBaseStyle: CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 'var(--s-1, 4px)',
  padding: '6px 12px',
  background: 'var(--paper-2, #EFEBE0)',
  border: '1px solid var(--border, #D9D2C0)',
  borderRadius: 'var(--r-pill, 999px)',
  fontFamily: 'inherit',
  fontSize: 'var(--t-xs, 12px)',
  fontWeight: 500,
  color: 'var(--ink, #0E1A14)',
  cursor: 'pointer',
  boxShadow: 'var(--sh-xs, 0 1px 2px rgba(14, 26, 20, 0.04))',
  transition:
    'background var(--dur-fast, 100ms) var(--ease, cubic-bezier(0.2, 0.7, 0.2, 1)), color var(--dur-fast, 100ms) var(--ease, cubic-bezier(0.2, 0.7, 0.2, 1))',
};

const buttonDisabledStyle: CSSProperties = {
  ...buttonBaseStyle,
  color: 'var(--fg-faint, #94A199)',
  background: 'var(--paper-3, #E6E1D2)',
  cursor: 'not-allowed',
  boxShadow: 'none',
};

const panelWrapStyle: CSSProperties = {
  position: 'relative',
  display: 'inline-flex',
};

const panelStyle: CSSProperties = {
  position: 'absolute',
  top: 'calc(100% + 4px)',
  left: 0,
  zIndex: 30,
  minWidth: 200,
  margin: 0,
  padding: 6,
  listStyle: 'none',
  background: 'var(--paper-2, #EFEBE0)',
  border: '1px solid var(--border, #D9D2C0)',
  borderRadius: 'var(--r-md, 8px)',
  boxShadow:
    'var(--sh-md, 0 6px 14px -4px rgba(14, 26, 20, 0.08), 0 2px 6px -2px rgba(14, 26, 20, 0.04))',
  display: 'flex',
  flexDirection: 'column',
  gap: 2,
};

/* ─── Component ─────────────────────────────────────────────────────── */

/**
 * Generalised toolbar surface. Renders each action in order; the
 * canvas-side hook decides which providers it composes. Keeping the
 * dropdown state per-action means two dropdowns can't both be open at
 * once on the same row — clicking a second action's toggle implicitly
 * closes the first via the open-id ref below.
 */
export function BlockToolbar({
  block,
  actions: explicit,
  providers = [],
  testId,
}: BlockToolbarProps): ReactNode {
  const [openId, setOpenId] = useState<string | null>(null);

  // Flatten providers in declaration order; explicit actions come
  // first so callers can pin a leading slot without writing a provider.
  const fromProviders = providers.flatMap((p) => p(block));
  const actions: ToolbarAction[] = [
    ...(explicit ?? []),
    ...fromProviders,
  ];
  if (actions.length === 0) return null;

  return (
    <div
      className="gonext-block-toolbar"
      data-testid={testId ?? 'block-toolbar'}
      data-block-type={block.type}
      style={wrapperStyle}
      role="toolbar"
    >
      {actions.map((action) => {
        const kind = action.kind ?? 'button';
        if (kind === 'chip') {
          return (
            <div
              key={action.id}
              data-testid={`block-toolbar-chip-${action.id}`}
              data-action-id={action.id}
            >
              {action.icon ?? action.label}
            </div>
          );
        }
        const disabled = action.disabled ?? false;
        const isOpen = kind === 'dropdown' && openId === action.id;
        const onActivate = (): void => {
          if (disabled) return;
          if (kind === 'dropdown') {
            setOpenId((prev) => (prev === action.id ? null : action.id));
            return;
          }
          action.onSelect?.();
        };
        return (
          <div
            key={action.id}
            className="gonext-block-toolbar__item"
            data-testid={`block-toolbar-item-${action.id}`}
            data-action-id={action.id}
            data-open={isOpen ? 'true' : 'false'}
            style={kind === 'dropdown' ? panelWrapStyle : undefined}
          >
            <button
              type="button"
              aria-label={action.ariaLabel ?? action.label}
              aria-haspopup={kind === 'dropdown' ? 'menu' : undefined}
              aria-expanded={kind === 'dropdown' ? isOpen : undefined}
              disabled={disabled}
              onClick={onActivate}
              className="gonext-block-toolbar__toggle"
              data-testid={`block-toolbar-toggle-${action.id}`}
              style={disabled ? buttonDisabledStyle : buttonBaseStyle}
            >
              {action.icon !== undefined ? action.icon : null}
              <span>{action.label}</span>
            </button>
            {kind === 'dropdown' && isOpen && action.renderPanel ? (
              <div
                role="menu"
                aria-label={`${action.label} options`}
                className="gonext-block-toolbar__panel"
                data-testid={`block-toolbar-panel-${action.id}`}
                style={panelStyle}
              >
                {action.renderPanel(() => setOpenId(null))}
              </div>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}

/* ─── Action providers ──────────────────────────────────────────────── */

/** Options for the transform provider. */
export interface TransformProviderOptions {
  /** The transform registry to read from. */
  registry: TransformRegistry;
  /** Fired when an option is picked. Matches the original
   *  `<BlockTransformToolbar>` callback shape so callers swap without
   *  changing their reducer. */
  onApply: (transformId: string, transform: Transform) => void;
  /** Override the toggle label. Defaults to "Transform to". */
  toggleLabel?: string;
}

/** Produce a single "Transform to" dropdown action for the block, or
 *  an empty array when no transforms apply. The action carries a
 *  `renderPanel` that renders one menuitem per available transform
 *  using the same `option` data-test ids the legacy component used,
 *  so visual tests keep their queries. */
export function transformActionProvider(
  options: TransformProviderOptions,
): ActionProvider {
  return (block: Block) => {
    const opts = options.registry.from(block.type, block);
    const hasOptions = opts.length > 0;
    return [
      {
        id: 'transform',
        kind: 'dropdown',
        label: options.toggleLabel ?? 'Transform to',
        ariaLabel: `${options.toggleLabel ?? 'Transform to'}: ${block.type}`,
        disabled: !hasOptions,
        renderPanel: (close) => (
          <ul
            role="none"
            style={{
              listStyle: 'none',
              margin: 0,
              padding: 0,
              display: 'flex',
              flexDirection: 'column',
              gap: 2,
            }}
          >
            {opts.map((t) => (
              <li key={t.id} role="none">
                <button
                  type="button"
                  role="menuitem"
                  onClick={() => {
                    options.onApply(t.id, t);
                    close();
                  }}
                  data-testid={`block-toolbar-transform-option-${t.id}`}
                  title={t.description ?? t.label}
                  style={{
                    display: 'flex',
                    flexDirection: 'column',
                    alignItems: 'flex-start',
                    gap: 2,
                    width: '100%',
                    padding: '7px 10px',
                    background: 'transparent',
                    border: '1px solid transparent',
                    borderRadius: 'var(--r-sm, 6px)',
                    fontFamily: 'inherit',
                    textAlign: 'left',
                    cursor: 'pointer',
                    color: 'var(--ink, #0E1A14)',
                  }}
                >
                  <span
                    style={{
                      fontSize: 'var(--t-sm, 13px)',
                      fontWeight: 500,
                    }}
                  >
                    {t.label}
                  </span>
                  {t.description !== undefined ? (
                    <span
                      style={{
                        fontFamily:
                          "var(--font-mono, 'Geist Mono', ui-monospace, monospace)",
                        fontSize: 'var(--t-2xs, 11px)',
                        color: 'var(--fg-subtle, #6B7B72)',
                      }}
                    >
                      {t.description}
                    </span>
                  ) : null}
                </button>
              </li>
            ))}
          </ul>
        ),
      },
    ];
  };
}

/** Produce a lock-indicator chip action for the block. Returns an
 *  empty array when the block is unlocked — the toolbar slots are
 *  defined by the union of provider outputs, so an unlocked block
 *  just doesn't surface the chip. */
export function lockActionProvider(): ActionProvider {
  return (block: Block) => {
    if (!isLocked(block)) return [];
    return [
      {
        id: 'lock',
        kind: 'chip',
        label: 'Locked',
        icon: <BlockLockIndicator block={block} />,
      },
    ];
  };
}
