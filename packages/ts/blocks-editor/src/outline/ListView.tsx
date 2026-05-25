/**
 * `<ListView>` — flat hierarchical tree of *every* block in the
 * document (not just headings). Lives next to `<DocumentOutline>` in
 * the same side panel; the chrome toggles between them or stacks
 * them, depending on host preference.
 *
 * Where the outline is "what the reader sees" (just headings), the
 * list view is "what the editor sees" (every block, every nesting
 * level). It's the panel power-users open when they need to grab a
 * deeply-nested block without scrolling, or when they want to
 * verify a paste landed where they expected.
 *
 * Behaviour:
 *
 *   - Each row shows the block's type + a short preview (text /
 *     attribute hint). Indentation mirrors the nesting depth.
 *   - Hovering a row reads as a hover-highlight on the canvas — the
 *     host wires this via `onHover(clientId | null)` and reflects
 *     the hover on the canvas's own selection chrome.
 *   - Clicking a row selects the block via `onSelect(clientId)`,
 *     same as the outline.
 *   - The currently selected block carries the same emerald accent
 *     as the outline; rows in the multi-select set get a softer
 *     emerald-tint background so authors can see the group.
 */
'use client';

import type { Block, BlockTree } from '@gonext/blocks-sdk';
import {
  useMemo,
  useState,
  type CSSProperties,
  type ReactNode,
} from 'react';

interface ListViewEntry {
  block: Block;
  clientId: string;
  depth: number;
}

/**
 * Walk the tree DFS, producing a flat list of `{block, depth}` rows.
 * We resolve the clientId here (falling back to a path-derived id)
 * so the canvas and the list view agree on identity even before the
 * autosave layer assigns real ids.
 */
export function flattenBlocks(tree: BlockTree): ListViewEntry[] {
  const out: ListViewEntry[] = [];
  function walk(blocks: BlockTree, depth: number, prefix: string) {
    blocks.forEach((block, index) => {
      const clientId =
        block.clientId ?? `${prefix}${block.type}-${index}`;
      out.push({ block, clientId, depth });
      if (block.innerBlocks !== undefined && block.innerBlocks.length > 0) {
        walk(block.innerBlocks, depth + 1, `${clientId}/`);
      }
    });
  }
  walk(tree, 0, '');
  return out;
}

/**
 * Cheap preview for a block. The block-sdk doesn't ship a "summary"
 * helper (yet), so we sniff a few well-known attribute keys and
 * fall back to the block type's tail segment.
 */
function previewForBlock(block: Block): string {
  const attrs = block.attributes ?? {};
  const text = (attrs as { text?: unknown }).text;
  const code = (attrs as { code?: unknown }).code;
  const url = (attrs as { url?: unknown }).url;
  if (typeof text === 'string' && text.length > 0) {
    return text.length > 60 ? text.slice(0, 57) + '…' : text;
  }
  if (typeof code === 'string' && code.length > 0) {
    return code.length > 60 ? code.slice(0, 57) + '…' : code;
  }
  if (typeof url === 'string' && url.length > 0) {
    return url;
  }
  return '';
}

export interface ListViewProps {
  /** The block tree the canvas is showing. */
  blocks: BlockTree;
  /** Currently selected client id. */
  selectedClientId?: string;
  /**
   * Optional multi-selection set. Rows whose clientId is in this set
   * get a softer emerald-tinted background. Useful when the host has
   * the `<SelectionProvider>` mounted.
   */
  selectedIds?: ReadonlySet<string>;
  /** Called when the user clicks a row. */
  onSelect: (clientId: string) => void;
  /** Called when the row is hovered / unhovered. `null` on leave. */
  onHover?: (clientId: string | null) => void;
  className?: string;
}

const panelStyle: CSSProperties = {
  background: 'var(--paper-2, #EFEBE0)',
  border: '1px solid var(--border, #D9D2C0)',
  borderRadius: 'var(--r-md, 8px)',
  padding: 'var(--s-4, 16px)',
  fontFamily:
    "var(--font-sans, 'Geist', -apple-system, system-ui, sans-serif)",
  fontSize: 'var(--t-sm, 13px)',
  color: 'var(--ink-soft, #1F2D26)',
  minWidth: 240,
};

const headerStyle: CSSProperties = {
  margin: '0 0 var(--s-3, 12px)',
  fontSize: 'var(--t-xs, 12px)',
  fontWeight: 600,
  letterSpacing: '0.04em',
  textTransform: 'uppercase',
  color: 'var(--fg-muted, #4A5C52)',
};

const rowBase: CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 8,
  padding: '6px 8px',
  borderRadius: 'var(--r-sm, 6px)',
  cursor: 'pointer',
  background: 'transparent',
  border: 'none',
  width: '100%',
  textAlign: 'left',
  fontFamily: 'inherit',
  fontSize: 'inherit',
  color: 'inherit',
  transition:
    'background var(--dur-fast, 100ms) var(--ease, cubic-bezier(0.2, 0.7, 0.2, 1))',
};

const rowHover: CSSProperties = {
  ...rowBase,
  background: 'var(--paper-3, #E5E0CE)',
};

const rowSelected: CSSProperties = {
  ...rowBase,
  background: 'var(--emerald-soft, #D1FAE5)',
  color: 'var(--emerald-deep, #047857)',
};

const rowMulti: CSSProperties = {
  ...rowBase,
  background: 'var(--emerald-tint, rgba(16, 185, 129, 0.08))',
};

const typeChipStyle: CSSProperties = {
  fontFamily:
    "var(--font-mono, 'Geist Mono', ui-monospace, monospace)",
  fontSize: 'var(--t-xs, 11px)',
  color: 'var(--fg-muted, #4A5C52)',
  background: 'var(--paper-3, #E5E0CE)',
  borderRadius: 'var(--r-sm, 4px)',
  padding: '1px 6px',
  flexShrink: 0,
};

const previewStyle: CSSProperties = {
  flex: 1,
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
};

export function ListView({
  blocks,
  selectedClientId,
  selectedIds,
  onSelect,
  onHover,
  className,
}: ListViewProps) {
  const rows = useMemo(() => flattenBlocks(blocks), [blocks]);
  const [hoveredId, setHoveredId] = useState<string | null>(null);

  const setHover = (id: string | null) => {
    setHoveredId(id);
    onHover?.(id);
  };

  return (
    <nav
      aria-label="Block list view"
      data-testid="list-view"
      className={className}
      style={panelStyle}
    >
      <h3 data-testid="list-view-header" style={headerStyle}>
        List view
      </h3>
      {rows.length === 0 ? (
        <p
          data-testid="list-view-empty"
          style={{
            margin: 0,
            color: 'var(--fg-faint, #94A199)',
            fontStyle: 'italic',
            fontFamily:
              "var(--font-serif, 'Instrument Serif', Georgia, serif)",
          }}
        >
          The document is empty.
        </p>
      ) : (
        <ul
          role="tree"
          data-testid="list-view-tree"
          style={{ listStyle: 'none', margin: 0, padding: 0 }}
        >
          {rows.map((row) => (
            <ListViewRow
              key={row.clientId}
              entry={row}
              selectedClientId={selectedClientId}
              selectedIds={selectedIds}
              hoveredId={hoveredId}
              onSelect={onSelect}
              onHover={setHover}
            />
          ))}
        </ul>
      )}
    </nav>
  );
}

interface ListViewRowProps {
  entry: ListViewEntry;
  selectedClientId?: string;
  selectedIds?: ReadonlySet<string>;
  hoveredId: string | null;
  onSelect: (clientId: string) => void;
  onHover: (clientId: string | null) => void;
}

function ListViewRow({
  entry,
  selectedClientId,
  selectedIds,
  hoveredId,
  onSelect,
  onHover,
}: ListViewRowProps): ReactNode {
  const { block, clientId, depth } = entry;
  const isSelected = clientId === selectedClientId;
  const isMulti =
    selectedIds !== undefined && selectedIds.has(clientId) && !isSelected;
  const isHovered = hoveredId === clientId && !isSelected;
  const baseRowStyle = isSelected
    ? rowSelected
    : isMulti
      ? rowMulti
      : isHovered
        ? rowHover
        : rowBase;

  return (
    <li role="treeitem" aria-selected={isSelected}>
      <button
        type="button"
        data-testid={`list-view-row-${clientId}`}
        data-block-type={block.type}
        data-selected={isSelected ? 'true' : 'false'}
        data-multi={isMulti ? 'true' : 'false'}
        data-hovered={isHovered ? 'true' : 'false'}
        onClick={() => onSelect(clientId)}
        onMouseEnter={() => onHover(clientId)}
        onMouseLeave={() => onHover(null)}
        onFocus={() => onHover(clientId)}
        onBlur={() => onHover(null)}
        style={{
          ...baseRowStyle,
          paddingLeft: `calc(var(--s-2, 8px) + ${depth * 16}px)`,
        }}
      >
        <span style={typeChipStyle}>{block.type}</span>
        <span style={previewStyle}>{previewForBlock(block)}</span>
      </button>
    </li>
  );
}
