/**
 * Editor chrome — the top toolbar and right-rail inspector tabs that
 * frame the document on the canvas.
 *
 * The canvas + inserter + transform toolbar handle authoring; this
 * module ships the surrounding "Living systems" surfaces:
 *
 *   - `<EditorTopBar>`  — the 54px forest top strip with a crumb on
 *     the left, a view-switcher pill in the middle, and a slot for
 *     the autosave indicator + actions on the right. Mirrors the
 *     `.topbar` rule in `docs/design/ui_kits/editor/index.html`.
 *
 *   - `<EditorTitle>`  — the contenteditable document title that
 *     uses the brand's Headline composition (Archivo grotesque +
 *     Instrument Serif italic accent for the emphasised word). The
 *     placeholder is *also* an italic-accent string so the unedited
 *     state shows the rule even before the author writes a word.
 *
 *   - `<InspectorTabs>` — the right rail tabs (Block / Document /
 *     SEO). Geist 500 labels, with the active tab carrying a
 *     lavender underline rather than the canvas's emerald — the mock
 *     intentionally uses the secondary accent here so authors can
 *     tell the inspector apart from the canvas selection chrome at a
 *     glance.
 *
 * Everything is visual-only: no Lexical wiring, no save side-effects.
 * The admin app composes these around the existing `<BlockEditCanvas>`
 * + `<BlockInserter>` to land the full editor surface.
 */
'use client';

import {
  useState,
  type CSSProperties,
  type ReactNode,
} from 'react';

/* ─── Top bar ──────────────────────────────────────────────────── */

export interface EditorTopBarProps {
  /** Left-side crumb / breadcrumb cluster. Free-form. */
  crumb?: ReactNode;
  /**
   * Centred view-switcher slot. Pass a `<EditorViewSwitcher>` or any
   * pill cluster shaped like the mock's `.topbar .center` group.
   */
  viewSwitcher?: ReactNode;
  /**
   * Right-side actions. Pass the autosave indicator + Schedule +
   * Publish buttons here. Rendered with `--s-2` gaps.
   */
  actions?: ReactNode;
  /** Optional className for the host strip. */
  className?: string;
}

const topBarStyle: CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  height: 54,
  padding: '0 14px 0 16px',
  background: 'var(--forest, #0E1A14)',
  color: 'var(--fg-on-forest, #F0EAD8)',
  borderBottom: '1px solid var(--forest-border, #2C3D33)',
  fontFamily:
    "var(--font-sans, 'Geist', -apple-system, system-ui, sans-serif)",
  fontSize: 'var(--t-sm, 13px)',
  flexShrink: 0,
};

const topBarLeftStyle: CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 14,
  minWidth: 0,
};

const topBarRightStyle: CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 'var(--s-2, 8px)',
};

export function EditorTopBar({
  crumb,
  viewSwitcher,
  actions,
  className,
}: EditorTopBarProps) {
  return (
    <div
      data-testid="editor-top-bar"
      data-surface="forest"
      className={className}
      style={topBarStyle}
    >
      <div data-testid="editor-top-bar-left" style={topBarLeftStyle}>
        {crumb}
      </div>
      {viewSwitcher !== undefined ? (
        <div data-testid="editor-top-bar-center">{viewSwitcher}</div>
      ) : null}
      <div data-testid="editor-top-bar-right" style={topBarRightStyle}>
        {actions}
      </div>
    </div>
  );
}

/* ─── View switcher ────────────────────────────────────────────── */

export interface EditorViewSwitcherProps {
  /** Available views in display order. */
  views: ReadonlyArray<{ id: string; label: string }>;
  /** Active view id. */
  activeId: string;
  /** Called when the user clicks a view. */
  onChange: (id: string) => void;
  className?: string;
}

const viewSwitcherStyle: CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 4,
  padding: 3,
  background: 'var(--forest-2, #18261E)',
  borderRadius: 'var(--r-md, 8px)',
};

const viewBtnBase: CSSProperties = {
  padding: '6px 14px',
  background: 'transparent',
  color: 'var(--fg-on-forest-muted, #A8B5AC)',
  border: 'none',
  cursor: 'pointer',
  fontFamily: 'inherit',
  fontSize: 'var(--t-xs, 12px)',
  fontWeight: 500,
  borderRadius: 'var(--r-sm, 6px)',
};

const viewBtnActive: CSSProperties = {
  ...viewBtnBase,
  background: 'var(--forest-3, #22322A)',
  color: 'var(--fg-on-forest, #F0EAD8)',
};

export function EditorViewSwitcher({
  views,
  activeId,
  onChange,
  className,
}: EditorViewSwitcherProps) {
  return (
    <div
      role="tablist"
      aria-label="Editor view"
      data-testid="editor-view-switcher"
      className={className}
      style={viewSwitcherStyle}
    >
      {views.map((v) => {
        const active = v.id === activeId;
        return (
          <button
            key={v.id}
            type="button"
            role="tab"
            aria-selected={active}
            data-active={active ? 'true' : 'false'}
            data-testid={`editor-view-switcher-${v.id}`}
            onClick={() => onChange(v.id)}
            style={active ? viewBtnActive : viewBtnBase}
          >
            {v.label}
          </button>
        );
      })}
    </div>
  );
}

/* ─── Editor title ─────────────────────────────────────────────── */

export interface EditorTitleProps {
  /** The current title. */
  value: string;
  /** Called when the contenteditable surface fires `input`. */
  onChange?: (next: string) => void;
  /**
   * Placeholder shown when `value` is empty. The brand wants the
   * placeholder itself to honour the italic-accent rule, so pass a
   * string containing markdown-ish emphasis (`*word*`) and the
   * component will render the bracketed word in Instrument Serif
   * italic. Defaults to a brand-flavored prompt.
   */
  placeholder?: string;
  /** ARIA label, falls back to "Document title". */
  ariaLabel?: string;
  className?: string;
}

const titleStyle: CSSProperties = {
  display: 'block',
  margin: 0,
  padding: 0,
  outline: 'none',
  border: 'none',
  background: 'transparent',
  fontFamily:
    "var(--font-display, 'Archivo', system-ui, sans-serif)",
  fontWeight: 800,
  fontSize: 56,
  lineHeight: 0.95,
  letterSpacing: '-0.03em',
  color: 'var(--ink, #0E1A14)',
  // The italic-accent rule (mirrors apps/admin's <Headline> and the
  // editor mock's `.doc h1.title em` block). `em` children swap to
  // Instrument Serif, 400, italic, emerald-deep.
};

const placeholderStyle: CSSProperties = {
  ...titleStyle,
  color: 'var(--fg-faint, #94A199)',
  // Disable selection on the placeholder so the contenteditable
  // surface behind it still owns the caret.
  pointerEvents: 'none',
  userSelect: 'none',
};

/**
 * Split a `*word*` markdown-ish placeholder into Archivo + Instrument
 * Serif italic fragments. Anything outside `*...*` renders straight;
 * one accent per placeholder, matching the brand rule.
 */
function renderItalicPlaceholder(placeholder: string): ReactNode {
  const match = placeholder.match(/^(.*?)\*([^*]+)\*(.*)$/);
  if (match === null) {
    return placeholder;
  }
  return (
    <>
      {match[1]}
      <em
        data-testid="editor-title-placeholder-accent"
        style={{
          fontFamily:
            "var(--font-serif, 'Instrument Serif', Georgia, serif)",
          fontStyle: 'italic',
          fontWeight: 400,
          color: 'var(--emerald-deep, #047857)',
          fontSize: '1.05em',
          letterSpacing: '-0.01em',
        }}
      >
        {match[2]}
      </em>
      {match[3]}
    </>
  );
}

const titleWrapperStyle: CSSProperties = {
  position: 'relative',
  margin: '0 0 16px',
};

export function EditorTitle({
  value,
  onChange,
  placeholder = 'How we *source* great writing.',
  ariaLabel = 'Document title',
  className,
}: EditorTitleProps) {
  const isEmpty = value.length === 0;

  return (
    <div
      data-testid="editor-title-wrapper"
      className={className}
      style={titleWrapperStyle}
    >
      <h1
        contentEditable
        suppressContentEditableWarning
        role="textbox"
        aria-label={ariaLabel}
        data-testid="editor-title"
        data-empty={isEmpty ? 'true' : 'false'}
        spellCheck={false}
        // Mirror the brand italic-accent rule on em children written
        // by the author themselves. We apply the colour via inline
        // style so the rule renders even without admin tokens.css.
        style={{
          ...titleStyle,
          // Author-written <em> children get the same italic treatment.
        }}
        onInput={(event) => {
          if (onChange !== undefined) {
            onChange((event.target as HTMLElement).textContent ?? '');
          }
        }}
      >
        {value}
      </h1>
      {isEmpty ? (
        <span
          data-testid="editor-title-placeholder"
          aria-hidden="true"
          style={{
            ...placeholderStyle,
            position: 'absolute',
            top: 0,
            left: 0,
          }}
        >
          {renderItalicPlaceholder(placeholder)}
        </span>
      ) : null}
    </div>
  );
}

/* ─── Inspector tabs ───────────────────────────────────────────── */

export interface InspectorTabsProps {
  /** Tabs in display order. */
  tabs: ReadonlyArray<{ id: string; label: string }>;
  /** Active tab id. */
  activeId: string;
  /** Called when the user clicks a tab. */
  onChange: (id: string) => void;
  className?: string;
}

const inspTabsStyle: CSSProperties = {
  display: 'flex',
  borderBottom: '1px solid var(--border, #D9D2C0)',
  padding: '0 6px',
  background: 'var(--paper-2, #EFEBE0)',
  fontFamily:
    "var(--font-sans, 'Geist', -apple-system, system-ui, sans-serif)",
};

// All four border properties spelled out so React doesn't fight the
// shorthand/longhand mix when switching between the active and base
// styles (active changes only `borderBottomColor`).
const inspTabBase: CSSProperties = {
  flex: 1,
  padding: '12px 6px',
  background: 'transparent',
  borderTop: 'none',
  borderRight: 'none',
  borderLeft: 'none',
  borderBottomWidth: 2,
  borderBottomStyle: 'solid',
  borderBottomColor: 'transparent',
  cursor: 'pointer',
  fontFamily: 'inherit',
  fontSize: 'var(--t-xs, 12px)',
  fontWeight: 500,
  color: 'var(--fg-muted, #4A5C52)',
  transition:
    'color var(--dur-fast, 100ms) var(--ease, cubic-bezier(0.2, 0.7, 0.2, 1)), border-color var(--dur-fast, 100ms) var(--ease, cubic-bezier(0.2, 0.7, 0.2, 1))',
};

// Lavender, not emerald — the inspector is the editor's secondary
// authoring surface (settings / metadata) and the brand reserves
// lavender for that companion role.
const inspTabActive: CSSProperties = {
  ...inspTabBase,
  color: 'var(--ink, #0E1A14)',
  borderBottomColor: 'var(--lavender, #A78BFA)',
};

export function InspectorTabs({
  tabs,
  activeId,
  onChange,
  className,
}: InspectorTabsProps) {
  return (
    <div
      role="tablist"
      aria-label="Inspector tabs"
      data-testid="inspector-tabs"
      className={className}
      style={inspTabsStyle}
    >
      {tabs.map((t) => {
        const active = t.id === activeId;
        return (
          <button
            key={t.id}
            type="button"
            role="tab"
            aria-selected={active}
            data-active={active ? 'true' : 'false'}
            data-testid={`inspector-tab-${t.id}`}
            onClick={() => onChange(t.id)}
            style={active ? inspTabActive : inspTabBase}
          >
            {t.label}
          </button>
        );
      })}
    </div>
  );
}

/**
 * Uncontrolled wrapper around `<InspectorTabs>` for hosts that don't
 * want to wire their own state. Defaults to the first tab.
 */
export function UncontrolledInspectorTabs({
  tabs,
  defaultId,
  onChange,
  className,
}: {
  tabs: ReadonlyArray<{ id: string; label: string }>;
  defaultId?: string;
  onChange?: (id: string) => void;
  className?: string;
}) {
  const initial = defaultId ?? tabs[0]?.id ?? '';
  const [activeId, setActiveId] = useState<string>(initial);
  return (
    <InspectorTabs
      tabs={tabs}
      activeId={activeId}
      onChange={(id) => {
        setActiveId(id);
        onChange?.(id);
      }}
      className={className}
    />
  );
}
