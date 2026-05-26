/**
 * Tests for the composable `<BlockToolbar>` and its built-in providers
 * (#100). Pins the contract:
 *
 *   1. The toolbar renders one item per action, in declaration order.
 *   2. Button actions fire onSelect once on click.
 *   3. Dropdown actions toggle their panel; selecting an option calls
 *      the action's onApply (via the panel close handler).
 *   4. Chip actions render their icon but have no toggle button.
 *   5. The transform provider mirrors the legacy
 *      <BlockTransformToolbar> behaviour: one dropdown action, one
 *      menuitem per available transform, disabled when no transforms
 *      apply.
 *   6. The lock provider only contributes a chip when the block is
 *      locked.
 *   7. Provider order + explicit actions compose: explicit actions
 *      come before provider output in the rendered row.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import type { Block } from '@gonext/blocks-sdk';
import {
  BlockToolbar,
  lockActionProvider,
  transformActionProvider,
  type ToolbarAction,
} from './block-toolbar.tsx';
import type {
  Transform,
  TransformRegistry,
} from './transform-types.ts';
import { LOCK_ATTRIBUTE_KEY } from './locks.ts';

function paragraph(): Block {
  return { type: 'core/paragraph', attributes: { content: 'hi' } };
}

function lockedParagraph(): Block {
  return {
    type: 'core/paragraph',
    attributes: {
      content: 'hi',
      [LOCK_ATTRIBUTE_KEY]: { move: true, remove: true },
    },
  };
}

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

const para2heading: Transform = {
  id: 'p2h',
  from: 'core/paragraph',
  to: 'core/heading',
  label: 'Heading',
  convert: (b) => ({
    type: 'core/heading',
    attributes: { content: b.attributes['content'] ?? '' },
  }),
};

describe('<BlockToolbar>', () => {
  it('renders one item per explicit action in order', () => {
    const actions: ToolbarAction[] = [
      { id: 'a', label: 'A' },
      { id: 'b', label: 'B' },
      { id: 'c', label: 'C' },
    ];
    render(<BlockToolbar block={paragraph()} actions={actions} />);
    const items = screen.getAllByTestId(/^block-toolbar-item-/);
    expect(items.map((el) => el.dataset['actionId'])).toEqual(['a', 'b', 'c']);
  });

  it('button action fires onSelect on click', () => {
    const onSelect = vi.fn();
    render(
      <BlockToolbar
        block={paragraph()}
        actions={[{ id: 'go', label: 'Go', onSelect }]}
      />,
    );
    fireEvent.click(screen.getByTestId('block-toolbar-toggle-go'));
    expect(onSelect).toHaveBeenCalledTimes(1);
  });

  it('dropdown action toggles its panel and closes after selection', () => {
    const onApply = vi.fn();
    render(
      <BlockToolbar
        block={paragraph()}
        providers={[
          transformActionProvider({
            registry: stubRegistry([para2heading]),
            onApply,
          }),
        ]}
      />,
    );

    expect(screen.queryByTestId('block-toolbar-panel-transform')).toBeNull();
    fireEvent.click(screen.getByTestId('block-toolbar-toggle-transform'));
    expect(
      screen.getByTestId('block-toolbar-panel-transform'),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('block-toolbar-transform-option-p2h'));
    expect(onApply).toHaveBeenCalledWith('p2h', para2heading);
    expect(screen.queryByTestId('block-toolbar-panel-transform')).toBeNull();
  });

  it('disables the transform toggle when no transforms apply', () => {
    render(
      <BlockToolbar
        block={paragraph()}
        providers={[
          transformActionProvider({
            registry: stubRegistry([]),
            onApply: vi.fn(),
          }),
        ]}
      />,
    );
    const toggle = screen.getByTestId('block-toolbar-toggle-transform');
    expect(toggle).toBeDisabled();
  });

  it('lock provider only contributes a chip when the block is locked', () => {
    const { rerender } = render(
      <BlockToolbar block={paragraph()} providers={[lockActionProvider()]} />,
    );
    expect(screen.queryByTestId('block-toolbar-chip-lock')).toBeNull();
    expect(screen.queryByTestId('block-toolbar')).toBeNull();

    rerender(
      <BlockToolbar
        block={lockedParagraph()}
        providers={[lockActionProvider()]}
      />,
    );
    expect(screen.getByTestId('block-toolbar-chip-lock')).toBeInTheDocument();
    // The chip is informational — there is no toggle button rendered.
    expect(screen.queryByTestId('block-toolbar-toggle-lock')).toBeNull();
  });

  it('composes explicit actions before provider output', () => {
    const extra: ToolbarAction = { id: 'extra', label: 'X' };
    render(
      <BlockToolbar
        block={lockedParagraph()}
        actions={[extra]}
        providers={[
          transformActionProvider({
            registry: stubRegistry([para2heading]),
            onApply: vi.fn(),
          }),
          lockActionProvider(),
        ]}
      />,
    );

    const items = screen.getAllByTestId(
      /^block-toolbar-(item|chip)-/,
    );
    const order = items.map((el) => el.dataset['actionId']);
    expect(order).toEqual(['extra', 'transform', 'lock']);
  });

  it('renders nothing when no actions are produced', () => {
    const { container } = render(
      <BlockToolbar block={paragraph()} providers={[lockActionProvider()]} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('exposes role="toolbar" so AT users can land on it as a group', () => {
    render(
      <BlockToolbar
        block={paragraph()}
        actions={[{ id: 'a', label: 'A' }]}
      />,
    );
    expect(screen.getByRole('toolbar')).toBeInTheDocument();
  });
});
