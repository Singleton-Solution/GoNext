/**
 * Tests for <BlockTransformToolbar>.
 *
 * Contract under test:
 *  1. Reads available transforms via `registry.from(block.type, block)`.
 *  2. Renders one option per transform with stable test ids.
 *  3. `initialOpen` skips the toggle-click warmup (assertion convenience).
 *  4. Clicking an option fires `onApply(transformId, transform)`.
 *  5. The toggle button is disabled when no transforms apply.
 *  6. `isMatch` predicates filter options out (e.g. heading level shifts
 *     opt out at the clamp boundary).
 *  7. The dropdown closes after a selection.
 *  8. The toggle's aria-label includes the source block type.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import type { Block } from '@gonext/blocks-sdk';
import { BlockTransformToolbar } from './block-transform-toolbar.tsx';
import type {
  Transform,
  TransformRegistry,
} from './transform-types.ts';

/** Build a registry stub that returns the supplied transforms. */
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

const paragraphToHeading: Transform = {
  id: 'core/paragraph-to-heading',
  from: 'core/paragraph',
  to: 'core/heading',
  label: 'Heading',
  description: 'Promote this paragraph to a heading.',
  convert: (b) => ({
    type: 'core/heading',
    attributes: { content: (b.attributes['content'] as string) ?? '', level: 2 },
  }),
};

const paragraphToQuote: Transform = {
  id: 'core/paragraph-to-quote',
  from: 'core/paragraph',
  to: 'core/quote',
  label: 'Quote',
  convert: (b) => ({
    type: 'core/quote',
    attributes: { value: (b.attributes['content'] as string) ?? '' },
  }),
};

const headingLevelUp: Transform = {
  id: 'core/heading-level-up',
  from: 'core/heading',
  to: 'core/heading',
  label: 'Level up',
  isMatch: (b) => ((b.attributes['level'] as number) ?? 2) > 1,
  convert: (b) => ({
    type: 'core/heading',
    attributes: {
      ...b.attributes,
      level: Math.max(1, ((b.attributes['level'] as number) ?? 2) - 1),
    },
  }),
};

describe('<BlockTransformToolbar>', () => {
  it('renders one option per available transform', () => {
    render(
      <BlockTransformToolbar
        block={paragraph()}
        registry={stubRegistry([paragraphToHeading, paragraphToQuote])}
        onApply={vi.fn()}
        initialOpen
      />,
    );

    expect(
      screen.getByTestId(
        'block-transform-toolbar-option-core/paragraph-to-heading',
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByTestId(
        'block-transform-toolbar-option-core/paragraph-to-quote',
      ),
    ).toBeInTheDocument();
  });

  it('does not render the menu until the toggle is clicked', () => {
    render(
      <BlockTransformToolbar
        block={paragraph()}
        registry={stubRegistry([paragraphToHeading])}
        onApply={vi.fn()}
      />,
    );

    expect(
      screen.queryByTestId('block-transform-toolbar-menu'),
    ).toBeNull();

    fireEvent.click(
      screen.getByTestId('block-transform-toolbar-toggle'),
    );

    expect(
      screen.getByTestId('block-transform-toolbar-menu'),
    ).toBeInTheDocument();
  });

  it('clicking an option fires onApply with the transform id', () => {
    const onApply = vi.fn();
    render(
      <BlockTransformToolbar
        block={paragraph()}
        registry={stubRegistry([paragraphToHeading, paragraphToQuote])}
        onApply={onApply}
        initialOpen
      />,
    );

    fireEvent.click(
      screen.getByTestId(
        'block-transform-toolbar-option-core/paragraph-to-quote',
      ),
    );

    expect(onApply).toHaveBeenCalledTimes(1);
    expect(onApply).toHaveBeenCalledWith(
      'core/paragraph-to-quote',
      paragraphToQuote,
    );
  });

  it('closes the menu after a selection', () => {
    render(
      <BlockTransformToolbar
        block={paragraph()}
        registry={stubRegistry([paragraphToHeading])}
        onApply={vi.fn()}
        initialOpen
      />,
    );

    fireEvent.click(
      screen.getByTestId(
        'block-transform-toolbar-option-core/paragraph-to-heading',
      ),
    );

    expect(
      screen.queryByTestId('block-transform-toolbar-menu'),
    ).toBeNull();
  });

  it('disables the toggle when no transforms apply', () => {
    render(
      <BlockTransformToolbar
        block={paragraph()}
        registry={stubRegistry([])}
        onApply={vi.fn()}
      />,
    );

    const toggle = screen.getByTestId('block-transform-toolbar-toggle');
    expect(toggle).toBeDisabled();
  });

  it('honours per-transform isMatch predicates (h1 hides level up)', () => {
    const block: Block = {
      type: 'core/heading',
      attributes: { content: 'top', level: 1 },
    };
    render(
      <BlockTransformToolbar
        block={block}
        registry={stubRegistry([headingLevelUp])}
        onApply={vi.fn()}
        initialOpen
      />,
    );

    // The toggle has no available options, so the toolbar disables it
    // and never renders the menu.
    expect(
      screen.getByTestId('block-transform-toolbar-toggle'),
    ).toBeDisabled();
    expect(
      screen.queryByTestId(
        'block-transform-toolbar-option-core/heading-level-up',
      ),
    ).toBeNull();
  });

  it("exposes the source block type via the toggle's aria-label", () => {
    render(
      <BlockTransformToolbar
        block={paragraph()}
        registry={stubRegistry([paragraphToHeading])}
        onApply={vi.fn()}
      />,
    );

    const toggle = screen.getByTestId('block-transform-toolbar-toggle');
    expect(toggle).toHaveAttribute('aria-label', 'Transform to: core/paragraph');
  });

  it('respects a custom toggleLabel', () => {
    render(
      <BlockTransformToolbar
        block={paragraph()}
        registry={stubRegistry([paragraphToHeading])}
        onApply={vi.fn()}
        toggleLabel="Convert"
      />,
    );

    const toggle = screen.getByTestId('block-transform-toolbar-toggle');
    expect(toggle).toHaveTextContent('Convert');
    expect(toggle).toHaveAttribute('aria-label', 'Convert: core/paragraph');
  });

  it('renders the option description as a secondary line when present', () => {
    render(
      <BlockTransformToolbar
        block={paragraph()}
        registry={stubRegistry([paragraphToHeading])}
        onApply={vi.fn()}
        initialOpen
      />,
    );

    const option = screen.getByTestId(
      'block-transform-toolbar-option-core/paragraph-to-heading',
    );
    expect(option).toHaveTextContent('Heading');
    expect(option).toHaveTextContent(
      'Promote this paragraph to a heading.',
    );
  });
});
