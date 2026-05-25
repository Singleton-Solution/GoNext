/**
 * `<EditorRailTabs>` — the right-rail tab strip for the block editor's
 * inspector (Block / Document / SEO).
 *
 * Headless and dumb on purpose: it renders a list of tab buttons and
 * fires `onChange(tabId)` when the user picks one. The host owns the
 * panel content and the active-tab state. This keeps the component
 * trivially testable and avoids prescribing a routing model.
 *
 * Brand notes:
 *  - The active tab is marked by a lavender underline (the brand's
 *    secondary accent) per the design mock at
 *    `docs/design/ui_kits/editor/index.html`.
 *  - Labels render in Geist (the body sans). No icons by default —
 *    inspector tabs read better as plain text in the design.
 */
'use client';

import { useState } from 'react';

export interface EditorRailTab {
  /** Stable id used by `data-testid` and the `value`/`onChange` contract. */
  id: string;
  /** Visible label rendered inside the tab button. */
  label: string;
  /** Optional aria-label override; defaults to `label`. */
  ariaLabel?: string;
}

export interface EditorRailTabsProps {
  /** The tabs to render, in display order. */
  tabs: readonly EditorRailTab[];
  /** Currently active tab id. When omitted, the component manages its own state. */
  value?: string;
  /** Initial tab when uncontrolled. Defaults to `tabs[0].id`. */
  defaultValue?: string;
  /** Fires when the user picks a tab. */
  onChange?: (tabId: string) => void;
  /** ARIA label for the tablist. Defaults to `"Inspector tabs"`. */
  ariaLabel?: string;
}

/**
 * The right-rail tab strip. Controlled when `value` is set, otherwise
 * manages its own active-tab state.
 */
export function EditorRailTabs({
  tabs,
  value,
  defaultValue,
  onChange,
  ariaLabel = 'Inspector tabs',
}: EditorRailTabsProps) {
  const fallback = defaultValue ?? tabs[0]?.id ?? '';
  const [internal, setInternal] = useState<string>(fallback);
  const active = value ?? internal;

  return (
    <div
      role="tablist"
      aria-label={ariaLabel}
      className="gonext-editor-rail-tabs"
      data-testid="editor-rail-tabs"
    >
      {tabs.map((tab) => {
        const isActive = tab.id === active;
        return (
          <button
            key={tab.id}
            type="button"
            role="tab"
            aria-selected={isActive}
            aria-label={tab.ariaLabel ?? tab.label}
            className="gonext-editor-rail-tabs__tab"
            data-testid={`editor-rail-tabs-tab-${tab.id}`}
            data-active={isActive ? 'true' : 'false'}
            onClick={() => {
              if (value === undefined) setInternal(tab.id);
              onChange?.(tab.id);
            }}
          >
            {tab.label}
          </button>
        );
      })}
    </div>
  );
}
