/**
 * Brand-chrome snapshot for `<BlockTransformToolbar>`.
 *
 * Locks in the BEM hooks + `data-open` marker the editor-theme.css
 * reads to flip the toggle pill into its emerald-hover state.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import type { Block } from '@gonext/blocks-sdk';
import { BlockTransformToolbar } from './block-transform-toolbar.tsx';
import type {
  Transform,
  TransformRegistry,
} from './transform-types.ts';

function stubRegistry(transforms: Transform[]): TransformRegistry {
  return {
    from(blockName, block) {
      return transforms.filter((t) => {
        if (t.from !== blockName) return false;
        if (block !== undefined && t.isMatch !== undefined && !t.isMatch(block)) {
          return false;
        }
        return true;
      });
    },
  };
}

function paragraph(content = 'hi'): Block {
  return { type: 'core/paragraph', attributes: { content } };
}

const toHeading: Transform = {
  id: 'core/paragraph-to-heading',
  from: 'core/paragraph',
  to: 'core/heading',
  label: 'Heading',
  convert: (b) => ({ type: 'core/heading', attributes: b.attributes }),
};

describe('<BlockTransformToolbar> brand chrome', () => {
  it('exposes the BEM hooks the editor-theme stylesheet reads', () => {
    render(
      <BlockTransformToolbar
        block={paragraph()}
        registry={stubRegistry([toHeading])}
        onApply={vi.fn()}
      />,
    );

    const root = screen.getByTestId('block-transform-toolbar');
    expect(root.className).toContain('gonext-block-transform-toolbar');

    const toggle = screen.getByTestId('block-transform-toolbar-toggle');
    expect(toggle.className).toBe('gonext-block-transform-toolbar__toggle');
    expect(toggle.getAttribute('data-open')).toBe('false');
  });

  it('toggle flips data-open when clicked', () => {
    render(
      <BlockTransformToolbar
        block={paragraph()}
        registry={stubRegistry([toHeading])}
        onApply={vi.fn()}
      />,
    );

    const toggle = screen.getByTestId('block-transform-toolbar-toggle');
    expect(toggle.getAttribute('data-open')).toBe('false');
    fireEvent.click(toggle);
    expect(toggle.getAttribute('data-open')).toBe('true');
  });
});
