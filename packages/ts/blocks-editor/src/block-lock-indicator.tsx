/**
 * `<BlockLockIndicator>` — toolbar chip surfaced next to a locked
 * block. Reads the lock state off the block's attributes and renders
 * a small badge listing which actions are blocked.
 *
 * The chip is informational only — clicking it does NOT open the
 * lock inspector (that's the inspector tab's job). It exists so
 * authors who try to drag a locked block see *why* the drag handle
 * didn't respond.
 *
 * Visual: cream chip on the "Living systems" amber tint, hairline
 * border, lock glyph + textual hint ("Locked: move, delete"). The
 * tokens follow the same fallback pattern as `block-edit-canvas.tsx`
 * so the package stays renderable in isolation.
 */
'use client';

import type { CSSProperties } from 'react';
import type { Block } from '@gonext/blocks-sdk';
import { isMoveLocked, isRemoveLocked } from './locks.ts';

export interface BlockLockIndicatorProps {
  /** The block whose lock state should be surfaced. */
  block: Block;
  /**
   * Optional className override. The chip ships with its own styling
   * tokens; this is for consumers (e.g. the editor's chrome) that
   * want to nudge layout without copying the visual treatment.
   */
  className?: string;
}

const chipStyle: CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 'var(--s-2, 6px)',
  padding: 'var(--s-1, 2px) var(--s-3, 10px)',
  borderRadius: 'var(--r-pill, 999px)',
  background: 'var(--amber-soft, #FEF3C7)',
  border: '1px solid var(--amber, #D97706)',
  color: 'var(--amber-ink, #92400E)',
  fontFamily:
    "var(--font-sans, 'Geist', -apple-system, system-ui, sans-serif)",
  fontSize: 'var(--t-xs, 12px)',
  fontWeight: 500,
  lineHeight: 1,
};

const iconStyle: CSSProperties = {
  width: 12,
  height: 12,
};

/**
 * Render the lock-status chip for the given block. Returns `null`
 * when the block has no lock attribute — the canvas calls this
 * unconditionally inside the toolbar slot, and an unlocked block
 * just renders nothing.
 */
export function BlockLockIndicator({
  block,
  className,
}: BlockLockIndicatorProps): React.ReactNode {
  const move = isMoveLocked(block);
  const remove = isRemoveLocked(block);
  if (!move && !remove) return null;

  // Build the textual hint. Listing both verbs separately ("move,
  // delete") is more honest than a single "fully locked" when only
  // one flag is set, which is the common case.
  const parts: string[] = [];
  if (move) parts.push('move');
  if (remove) parts.push('delete');
  const label = `Locked: ${parts.join(', ')}`;

  return (
    <span
      role="status"
      aria-label={label}
      data-testid="block-lock-indicator"
      data-lock-move={move ? 'true' : 'false'}
      data-lock-remove={remove ? 'true' : 'false'}
      className={
        'gonext-block-lock-indicator' + (className ? ' ' + className : '')
      }
      style={chipStyle}
    >
      <svg
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth={2}
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
        style={iconStyle}
      >
        <rect x="3" y="11" width="18" height="11" rx="2" />
        <path d="M7 11V7a5 5 0 0 1 10 0v4" />
      </svg>
      {label}
    </span>
  );
}
