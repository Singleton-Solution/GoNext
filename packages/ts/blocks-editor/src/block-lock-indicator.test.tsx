/**
 * Tests for `<BlockLockIndicator>`.
 *
 * The chip renders only when at least one lock flag is set. The
 * label lists which actions are blocked. We assert on data-testid
 * and the label rather than CSS — the styles are token-driven and
 * tested at the canvas snapshot level.
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import type { Block } from '@gonext/blocks-sdk';
import { BlockLockIndicator } from './block-lock-indicator.tsx';

const paragraph = (attrs: Record<string, unknown> = {}): Block => ({
  type: 'core/paragraph',
  attributes: { text: 'hi', ...attrs },
});

describe('BlockLockIndicator', () => {
  it('renders nothing when the block is unlocked', () => {
    const { container } = render(<BlockLockIndicator block={paragraph()} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders with move-only lock', () => {
    render(
      <BlockLockIndicator block={paragraph({ lock: { move: true } })} />,
    );
    const chip = screen.getByTestId('block-lock-indicator');
    expect(chip).toHaveAttribute('data-lock-move', 'true');
    expect(chip).toHaveAttribute('data-lock-remove', 'false');
    expect(chip).toHaveTextContent('Locked: move');
  });

  it('renders with remove-only lock', () => {
    render(
      <BlockLockIndicator block={paragraph({ lock: { remove: true } })} />,
    );
    const chip = screen.getByTestId('block-lock-indicator');
    expect(chip).toHaveAttribute('data-lock-remove', 'true');
    expect(chip).toHaveTextContent('Locked: delete');
  });

  it('renders with both flags', () => {
    render(
      <BlockLockIndicator
        block={paragraph({ lock: { move: true, remove: true } })}
      />,
    );
    const chip = screen.getByTestId('block-lock-indicator');
    expect(chip).toHaveTextContent('Locked: move, delete');
  });

  it('forwards the className prop', () => {
    render(
      <BlockLockIndicator
        block={paragraph({ lock: { move: true } })}
        className="extra"
      />,
    );
    expect(screen.getByTestId('block-lock-indicator')).toHaveClass('extra');
  });
});
