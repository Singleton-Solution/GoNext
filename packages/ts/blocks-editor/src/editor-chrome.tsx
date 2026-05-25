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
 *   - `<EditorWorkspace>` — **the editor-UX integration point**. This
 *     is the single place the chrome composes the three P2 editor-UX
 *     pieces together:
 *       · the paste-handler (`onPaste`) — mounted on the canvas
 *         container, so any Cmd+V over the document surface routes
 *         through Docs/Word/Notion/Markdown detection;
 *       · the dnd-kit `<SortableBlockList>` — wraps the block list
 *         in a SortableContext + selection provider so the canvas,
 *         outline, and list view share a single selection set;
 *       · the `<DocumentOutline>` + `<ListView>` panels — rendered
 *         in a side rail, toggled via `<OutlineToggle>`.
 *     Issues #213, #117, #111 all funnel through this one component
 *     so the rest of the chrome stays untouched.
 *
 * Everything else here is visual-only: no Lexical wiring, no save
 * side-effects. The admin app composes these around the existing
 * `<BlockEditCanvas>` + `<BlockInserter>` to land the full editor
 * surface.
 */
'use client';

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type ClipboardEvent as ReactClipboardEvent,
  type ReactNode,
} from 'react';
import type { BlockTree } from '@gonext/blocks-sdk';
import { SelectionProvider, SortableBlockList } from './dnd/index.ts';
import { DocumentOutline, ListView } from './outline/index.ts';
import { onPaste as runPasteHandler } from './paste-handler.ts';

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

/* ─── Outline toggle ───────────────────────────────────────────── */

export interface OutlineToggleProps {
  /** Current open state. */
  open: boolean;
  /** Toggle handler. */
  onToggle: (next: boolean) => void;
  className?: string;
}

const outlineToggleStyle: CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 6,
  padding: '6px 10px',
  background: 'transparent',
  border: '1px solid var(--forest-border, #2C3D33)',
  color: 'var(--fg-on-forest, #F0EAD8)',
  borderRadius: 'var(--r-md, 8px)',
  fontFamily:
    "var(--font-sans, 'Geist', -apple-system, system-ui, sans-serif)",
  fontSize: 'var(--t-xs, 12px)',
  fontWeight: 500,
  cursor: 'pointer',
};

const outlineToggleActiveStyle: CSSProperties = {
  ...outlineToggleStyle,
  background: 'var(--forest-3, #22322A)',
  borderColor: 'var(--emerald, #10B981)',
};

/**
 * Small chrome button that opens / closes the outline + list-view
 * rail. Designed for the top bar's action slot. The "active" styling
 * mirrors the view-switcher's on-pill so authors can tell at a glance
 * which side panel is open.
 */
export function OutlineToggle({
  open,
  onToggle,
  className,
}: OutlineToggleProps) {
  return (
    <button
      type="button"
      aria-pressed={open}
      aria-label={open ? 'Close outline' : 'Open outline'}
      data-testid="outline-toggle"
      data-active={open ? 'true' : 'false'}
      className={className}
      onClick={() => onToggle(!open)}
      style={open ? outlineToggleActiveStyle : outlineToggleStyle}
    >
      {/* Tree-line glyph; inline SVG so we don't take a lucide dep. */}
      <svg
        aria-hidden="true"
        width="14"
        height="14"
        viewBox="0 0 14 14"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <path d="M2 3h10M4 7h8M6 11h6" />
      </svg>
      Outline
    </button>
  );
}

/* ─── Editor workspace — single integration point ─────────────── */

export type EditorWorkspaceSidePanel = 'outline' | 'list' | 'none';

export interface EditorWorkspaceProps {
  /** The current block tree. */
  blocks: BlockTree;
  /** Called when the user reorders blocks via drag-drop. */
  onReorder: (nextIds: string[]) => void;
  /**
   * Called when the user pastes a clipboard payload over the canvas.
   * The handler runs Docs/Word/Notion/Markdown detection and emits
   * the resulting BlockTree. The host is responsible for splicing
   * the tree into its state.
   */
  onPaste: (blocks: BlockTree) => void;
  /** Currently selected block client id. */
  selectedClientId?: string;
  /** Called when a row in the outline or list view is clicked. */
  onSelectBlock: (clientId: string) => void;
  /** The canvas itself — usually <BlockEditCanvas>. */
  canvas: ReactNode;
  /**
   * Resolves a block id to the React node that renders inside the
   * sortable row. Defaults to a no-op (the canvas is the body). When
   * supplied, the workspace uses dnd-kit's sortable row chrome over
   * the canvas's render.
   */
  renderSortableRow?: (id: string, selected: boolean) => ReactNode;
  /** Which side panel is open (controlled). */
  sidePanel?: EditorWorkspaceSidePanel;
  /** Optional className for layout overrides. */
  className?: string;
}

const workspaceStyle: CSSProperties = {
  display: 'grid',
  gridTemplateColumns: 'minmax(0, 1fr) auto',
  gap: 'var(--s-5, 20px)',
  alignItems: 'start',
  width: '100%',
};

const sidePanelStyle: CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--s-4, 16px)',
  width: 280,
  flexShrink: 0,
};

/**
 * The integration container. Wires up the paste handler on the
 * canvas wrapper, mounts a `<SelectionProvider>` so the canvas +
 * side panels share selection state, and exposes the optional
 * sortable row chrome.
 *
 * The component itself is intentionally thin — its job is to *wire*,
 * not to author behaviour. Each underlying primitive is tested in
 * its own file; the only thing tested here is the wiring.
 */
export function EditorWorkspace({
  blocks,
  onReorder,
  onPaste,
  selectedClientId,
  onSelectBlock,
  canvas,
  renderSortableRow,
  sidePanel = 'none',
  className,
}: EditorWorkspaceProps) {
  // Flat client-id list, used by both the sortable wrapper and the
  // outline/list panels. We resolve fallback ids the same way the
  // outline + list view do so cross-panel selection stays consistent.
  const ids = useMemo(() => {
    const out: string[] = [];
    blocks.forEach((b, i) => {
      out.push(b.clientId ?? `${b.type}-${i}`);
    });
    return out;
  }, [blocks]);

  const onPasteHandler = useCallback(
    (event: ReactClipboardEvent<HTMLDivElement>) => {
      // We accept React's synthetic event but the underlying handler
      // wants the native ClipboardEvent — they share the same
      // `clipboardData` shape so a cast is safe.
      const result = runPasteHandler(event.nativeEvent);
      if (result !== null) {
        event.preventDefault();
        onPaste(result);
      }
    },
    [onPaste],
  );

  return (
    <SelectionProvider initialIds={selectedClientId ? [selectedClientId] : []}>
      <div
        className={className}
        data-testid="editor-workspace"
        style={workspaceStyle}
      >
        <div
          data-testid="editor-workspace-canvas"
          onPaste={onPasteHandler}
          style={{ minWidth: 0 }}
        >
          {renderSortableRow !== undefined ? (
            <SortableBlockList
              ids={ids}
              renderItem={renderSortableRow}
              onReorder={onReorder}
              externalSelectionProvider
            />
          ) : (
            canvas
          )}
        </div>
        {sidePanel !== 'none' ? (
          <aside
            data-testid="editor-workspace-side-panel"
            data-panel={sidePanel}
            style={sidePanelStyle}
          >
            {sidePanel === 'outline' ? (
              <DocumentOutline
                blocks={blocks}
                selectedClientId={selectedClientId}
                onSelect={onSelectBlock}
              />
            ) : (
              <SidePanelListView
                blocks={blocks}
                selectedClientId={selectedClientId}
                onSelectBlock={onSelectBlock}
              />
            )}
          </aside>
        ) : null}
      </div>
    </SelectionProvider>
  );
}

/**
 * Internal wrapper that wires the list view's `onHover` to the
 * canvas's hover-highlight state. Kept local so the outer
 * `<EditorWorkspace>` doesn't have to thread `hoverId` through
 * its prop sheet.
 */
function SidePanelListView({
  blocks,
  selectedClientId,
  onSelectBlock,
}: {
  blocks: BlockTree;
  selectedClientId?: string;
  onSelectBlock: (id: string) => void;
}) {
  const [hoverId, setHoverId] = useState<string | null>(null);
  const hoverRef = useRef<string | null>(null);
  // Surface the hover via a data-attribute on the body of the
  // workspace; canvas styling can react to it via CSS. We don't
  // mutate the DOM directly from a ref because that fights React;
  // instead we put the attribute on the wrapping aside via effect.
  useEffect(() => {
    hoverRef.current = hoverId;
    const root = document.querySelector(
      '[data-testid="editor-workspace-canvas"]',
    );
    if (root === null) return;
    if (hoverId !== null) {
      root.setAttribute('data-hover-block', hoverId);
    } else {
      root.removeAttribute('data-hover-block');
    }
  }, [hoverId]);

  return (
    <ListView
      blocks={blocks}
      selectedClientId={selectedClientId}
      onSelect={onSelectBlock}
      onHover={setHoverId}
    />
  );
}
