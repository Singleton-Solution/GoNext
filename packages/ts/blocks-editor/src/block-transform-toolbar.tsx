/**
 * `<BlockTransformToolbar>` — the "Transform to..." dropdown the canvas
 * renders next to the currently selected block.
 *
 * Behavior contract:
 *
 *  - Reads the available transforms from `TransformRegistry.from(name)`,
 *    passing the selected `block` so each transform's optional
 *    `isMatch` predicate gets the chance to opt itself out. A heading
 *    sitting at level 1 should not surface "level up".
 *  - Renders a `<button>` that toggles a list of transform options.
 *    Each option is a `<button role="menuitem">`. Clicking an option
 *    calls `onApply(transformId)` and closes the menu.
 *  - When no transforms apply, the toggle button is disabled — the
 *    affordance is visible (so authors can see the toolbar's full
 *    shape) but obviously inactive.
 *  - The dropdown is uncontrolled by default. Tests and apps that
 *    want to assert on the open state can pass `initialOpen` to skip
 *    the toggle-click warmup.
 *
 * Why not a native `<select>`? The dropdown needs to render rich tile
 * content per option (label + optional description) and respond to a
 * `Transform to: Group` aria-label. A `<select>` would also let the
 * browser style the popup with system chrome that fights the editor's
 * theming. Sticking with `<button>` + a list panel keeps every detail
 * under our control.
 */
'use client';

import type { Block } from '@gonext/blocks-sdk';
import { useState, type CSSProperties } from 'react';
import type { Transform, TransformRegistry } from './transform-types.ts';

/**
 * Brand styling — "Living systems" (docs/design/HANDOFF.md and the
 * block-transform pill pattern in docs/design/ui_kits/editor/index.html).
 * The toggle is a `--paper-2` pill with a soft `--sh-xs` shadow; on
 * hover the fill shifts to `--emerald-soft` (never translates — quiet,
 * not bouncy, per the motion handoff). The menu is the slash-menu
 * panel from the editor mock: `--paper-2` surface, `--sh-md` lift,
 * Geist menu items, emerald-soft hover, no chrome borrowed from the
 * native `<select>` popup.
 */
const wrapperStyle: CSSProperties = {
  position: 'relative',
  display: 'inline-flex',
  fontFamily:
    "var(--font-sans, 'Geist', -apple-system, system-ui, sans-serif)",
};

const toggleStyle: CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 'var(--s-2, 8px)',
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

const toggleDisabledStyle: CSSProperties = {
  ...toggleStyle,
  color: 'var(--fg-faint, #94A199)',
  background: 'var(--paper-3, #E6E1D2)',
  cursor: 'not-allowed',
  boxShadow: 'none',
};

const menuStyle: CSSProperties = {
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

const optionStyle: CSSProperties = {
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
};

const optionLabelStyle: CSSProperties = {
  fontSize: 'var(--t-sm, 13px)',
  fontWeight: 500,
};

const optionDescStyle: CSSProperties = {
  fontFamily:
    "var(--font-mono, 'Geist Mono', ui-monospace, monospace)",
  fontSize: 'var(--t-2xs, 11px)',
  color: 'var(--fg-subtle, #6B7B72)',
};

export interface BlockTransformToolbarProps {
  /** The currently selected block. */
  block: Block;
  /** The registry the toolbar reads transforms from. */
  registry: TransformRegistry;
  /**
   * Called when an option is selected. Receives the transform id; the
   * host is expected to look up the transform and splice the result
   * into the editor's tree state.
   */
  onApply: (transformId: string, transform: Transform) => void;
  /**
   * Whether the dropdown starts open. Defaults to `false`. Useful for
   * tests that want to assert on the option list without driving a
   * toggle click first.
   */
  initialOpen?: boolean;
  /**
   * Label rendered on the toggle button. Defaults to "Transform to".
   * Theme integrators can swap in a localised label without forking
   * the component.
   */
  toggleLabel?: string;
}

/**
 * The "Transform to..." dropdown. Client component.
 */
export function BlockTransformToolbar({
  block,
  registry,
  onApply,
  initialOpen = false,
  toggleLabel = 'Transform to',
}: BlockTransformToolbarProps) {
  const [open, setOpen] = useState<boolean>(initialOpen);

  // Recompute on every render. The transform set is small (tens of
  // entries even with plugin registrations), so the linear scan is
  // comfortably under any UX-relevant threshold and dodges the need
  // for a subscription to the registry.
  const options: Transform[] = registry.from(block.type, block);
  const hasOptions = options.length > 0;

  return (
    <div
      className="gonext-block-transform-toolbar"
      data-testid="block-transform-toolbar"
      data-block-type={block.type}
      style={wrapperStyle}
    >
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`${toggleLabel}: ${block.type}`}
        disabled={!hasOptions}
        onClick={() => setOpen((prev) => !prev)}
        className="gonext-block-transform-toolbar__toggle"
        data-testid="block-transform-toolbar-toggle"
        data-open={open ? 'true' : 'false'}
        style={!hasOptions ? toggleDisabledStyle : toggleStyle}
        onMouseEnter={(event) => {
          if (!hasOptions) return;
          event.currentTarget.style.background =
            'var(--emerald-soft, #D1FAE5)';
          event.currentTarget.style.color =
            'var(--emerald-deep, #047857)';
        }}
        onMouseLeave={(event) => {
          if (!hasOptions) return;
          event.currentTarget.style.background =
            'var(--paper-2, #EFEBE0)';
          event.currentTarget.style.color = 'var(--ink, #0E1A14)';
        }}
      >
        {toggleLabel}
      </button>

      {open && hasOptions ? (
        <ul
          role="menu"
          aria-label={`${toggleLabel} options`}
          className="gonext-block-transform-toolbar__menu"
          data-testid="block-transform-toolbar-menu"
          style={menuStyle}
        >
          {options.map((t) => (
            <li key={t.id} role="none">
              <button
                type="button"
                role="menuitem"
                onClick={() => {
                  onApply(t.id, t);
                  setOpen(false);
                }}
                className="gonext-block-transform-toolbar__option"
                data-testid={`block-transform-toolbar-option-${t.id}`}
                title={t.description ?? t.label}
                style={optionStyle}
                onMouseEnter={(event) => {
                  event.currentTarget.style.background =
                    'var(--emerald-soft, #D1FAE5)';
                  event.currentTarget.style.color =
                    'var(--emerald-deep, #047857)';
                }}
                onMouseLeave={(event) => {
                  event.currentTarget.style.background = 'transparent';
                  event.currentTarget.style.color = 'var(--ink, #0E1A14)';
                }}
              >
                <span
                  className="gonext-block-transform-toolbar__option-label"
                  style={optionLabelStyle}
                >
                  {t.label}
                </span>
                {t.description !== undefined ? (
                  <span
                    className="gonext-block-transform-toolbar__option-description"
                    style={optionDescStyle}
                  >
                    {t.description}
                  </span>
                ) : null}
              </button>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
