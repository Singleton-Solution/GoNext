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
import { useState } from 'react';
import type { Transform, TransformRegistry } from './transform-types.ts';

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
      >
        {toggleLabel}
      </button>

      {open && hasOptions ? (
        <ul
          role="menu"
          aria-label={`${toggleLabel} options`}
          className="gonext-block-transform-toolbar__menu"
          data-testid="block-transform-toolbar-menu"
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
              >
                <span className="gonext-block-transform-toolbar__option-label">
                  {t.label}
                </span>
                {t.description !== undefined ? (
                  <span className="gonext-block-transform-toolbar__option-description">
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
