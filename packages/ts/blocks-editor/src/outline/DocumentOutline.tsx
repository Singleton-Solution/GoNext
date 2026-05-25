/**
 * `<DocumentOutline>` — tree view of every heading block (`core/heading`)
 * in the document, rendered as anchor links that scroll the canvas to
 * the matching block.
 *
 * Two things make this useful as a side panel:
 *
 *  1. **Navigation shortcut.** Long posts have 20-40 headings; an
 *     outline lets the author jump to the section they're editing
 *     without scrolling-and-searching through prose.
 *  2. **Structural smell-test.** Seeing the H1/H2/H3 chain at a
 *     glance surfaces missing or out-of-order levels (e.g. an H4
 *     under an H2 with no H3 in between).
 *
 * The component is *read-only* over the block tree — it builds a
 * heading tree from the canonical `BlockTree` and emits clicks via
 * `onSelect(clientId)`. The host wires the click to its own selection
 * setter (so the canvas can scroll to it). We deliberately don't
 * mutate the block tree from inside the outline.
 *
 * The tree algorithm is a single pass: walk every block (recursing
 * into `innerBlocks`); for each `core/heading`, push it onto a stack
 * keyed by its level. A node's parent is the nearest stack entry
 * with a *lower* level; equal-or-higher levels pop the stack first.
 * This mirrors how Word / Pages build their navigation panes.
 */
'use client';

import type { Block, BlockTree } from '@gonext/blocks-sdk';
import {
  useMemo,
  type CSSProperties,
  type MouseEvent as ReactMouseEvent,
} from 'react';

/** One node in the rendered outline tree. */
export interface OutlineNode {
  /** The heading block's clientId (the canvas needs this for scroll). */
  clientId: string;
  /** Heading level 1-6. */
  level: number;
  /** The heading's rendered text. */
  text: string;
  /** Children — headings at a strictly higher level that follow. */
  children: OutlineNode[];
}

interface HeadingAttrs {
  level?: number;
  text?: string;
}

/**
 * Flatten the block tree into a list of heading blocks in document
 * order. We walk depth-first because the editor renders the same
 * way; preserving the visual order is what makes the outline match
 * what the author sees on the page.
 */
function collectHeadings(tree: BlockTree): Block[] {
  const out: Block[] = [];
  function walk(blocks: BlockTree) {
    for (const block of blocks) {
      if (block.type === 'core/heading') {
        out.push(block);
      }
      if (block.innerBlocks !== undefined && block.innerBlocks.length > 0) {
        walk(block.innerBlocks);
      }
    }
  }
  walk(tree);
  return out;
}

/**
 * Build a level-aware tree from a flat heading list. The trick: keep
 * a stack of "open" parents; when the next heading's level is <=
 * the top of the stack, pop until the top has a strictly lower
 * level. The current head of the stack is then the parent.
 */
export function buildOutline(tree: BlockTree): OutlineNode[] {
  const headings = collectHeadings(tree);
  const roots: OutlineNode[] = [];
  const stack: OutlineNode[] = [];
  for (const block of headings) {
    const attrs = (block.attributes ?? {}) as HeadingAttrs;
    const level = Math.max(1, Math.min(6, Math.trunc(attrs.level ?? 2)));
    const text = (attrs.text ?? '').trim();
    const node: OutlineNode = {
      clientId: block.clientId ?? `${block.type}-${headings.indexOf(block)}`,
      level,
      text,
      children: [],
    };
    while (stack.length > 0) {
      const top = stack[stack.length - 1];
      if (top !== undefined && top.level < level) break;
      stack.pop();
    }
    if (stack.length === 0) {
      roots.push(node);
    } else {
      const parent = stack[stack.length - 1];
      parent?.children.push(node);
    }
    stack.push(node);
  }
  return roots;
}

export interface DocumentOutlineProps {
  /** The block tree the canvas is showing. */
  blocks: BlockTree;
  /** Currently selected client id (gets the emerald accent). */
  selectedClientId?: string;
  /**
   * Called when the user clicks an outline row. The host should
   * select the matching block in the canvas + scroll it into view.
   */
  onSelect: (clientId: string) => void;
  /** Optional className for theme overrides. */
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

const emptyStyle: CSSProperties = {
  margin: 0,
  fontSize: 'var(--t-sm, 13px)',
  color: 'var(--fg-faint, #94A199)',
  fontStyle: 'italic',
  fontFamily:
    "var(--font-serif, 'Instrument Serif', Georgia, serif)",
};

const rowBaseStyle: CSSProperties = {
  display: 'flex',
  alignItems: 'baseline',
  gap: 8,
  padding: '6px 8px',
  borderRadius: 'var(--r-sm, 6px)',
  cursor: 'pointer',
  color: 'inherit',
  background: 'transparent',
  border: 'none',
  width: '100%',
  textAlign: 'left',
  fontFamily: 'inherit',
  fontSize: 'inherit',
};

const rowSelectedStyle: CSSProperties = {
  ...rowBaseStyle,
  background: 'var(--emerald-soft, #D1FAE5)',
  color: 'var(--emerald-deep, #047857)',
};

const levelChipStyle: CSSProperties = {
  fontFamily:
    "var(--font-mono, 'Geist Mono', ui-monospace, monospace)",
  fontSize: 'var(--t-xs, 11px)',
  color: 'var(--fg-faint, #94A199)',
  minWidth: 22,
};

export function DocumentOutline({
  blocks,
  selectedClientId,
  onSelect,
  className,
}: DocumentOutlineProps) {
  const outline = useMemo(() => buildOutline(blocks), [blocks]);

  return (
    <nav
      aria-label="Document outline"
      data-testid="document-outline"
      className={className}
      style={panelStyle}
    >
      <h3 data-testid="document-outline-header" style={headerStyle}>
        Outline
      </h3>
      {outline.length === 0 ? (
        <p data-testid="document-outline-empty" style={emptyStyle}>
          Add a heading to populate the outline.
        </p>
      ) : (
        <ul
          role="tree"
          data-testid="document-outline-tree"
          style={{ listStyle: 'none', margin: 0, padding: 0 }}
        >
          {outline.map((node) => (
            <OutlineRow
              key={node.clientId}
              node={node}
              depth={0}
              selectedClientId={selectedClientId}
              onSelect={onSelect}
            />
          ))}
        </ul>
      )}
    </nav>
  );
}

interface OutlineRowProps {
  node: OutlineNode;
  depth: number;
  selectedClientId?: string;
  onSelect: (clientId: string) => void;
}

function OutlineRow({
  node,
  depth,
  selectedClientId,
  onSelect,
}: OutlineRowProps) {
  const selected = selectedClientId === node.clientId;
  const onClick = (event: ReactMouseEvent<HTMLButtonElement>) => {
    event.preventDefault();
    onSelect(node.clientId);
  };

  return (
    <li role="treeitem" aria-selected={selected}>
      <button
        type="button"
        data-testid={`document-outline-row-${node.clientId}`}
        data-level={node.level}
        data-selected={selected ? 'true' : 'false'}
        onClick={onClick}
        // Anchor-ish: pressing Enter on a focused row should
        // navigate. <button> already handles Enter / Space, so we
        // don't need a custom keydown handler.
        style={{
          ...(selected ? rowSelectedStyle : rowBaseStyle),
          paddingLeft: `calc(var(--s-2, 8px) + ${depth * 16}px)`,
        }}
      >
        <span style={levelChipStyle}>H{node.level}</span>
        <span
          data-testid={`document-outline-row-${node.clientId}-text`}
          style={{ flex: 1 }}
        >
          {node.text.length > 0 ? node.text : 'Untitled heading'}
        </span>
      </button>
      {node.children.length > 0 ? (
        <ul
          role="group"
          style={{ listStyle: 'none', margin: 0, padding: 0 }}
        >
          {node.children.map((child) => (
            <OutlineRow
              key={child.clientId}
              node={child}
              depth={depth + 1}
              selectedClientId={selectedClientId}
              onSelect={onSelect}
            />
          ))}
        </ul>
      ) : null}
    </li>
  );
}
