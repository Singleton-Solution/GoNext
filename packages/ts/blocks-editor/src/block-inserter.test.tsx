/**
 * Tests for <BlockInserter>.
 *
 * The contract under test (issue #79):
 *  1. Renders every registered block matching the active category.
 *  2. The search input filters by `block.title`, case-insensitively.
 *  3. Switching category tabs swaps the visible list.
 *  4. Clicking a tile invokes `onInsert` with `{ type, attributes: {}, innerBlocks: [] }`.
 *  5. `defaultCoreBlocks` registers `core/paragraph` + `core/heading` with
 *     real attribute schemas the registry's validator accepts.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import {
  BlockRegistry,
  type Block,
  type BlockTypeDefinition,
} from '@gonext/blocks-sdk';
import { BlockInserter } from './block-inserter.tsx';
import { defaultCoreBlocks } from './default-core-blocks.ts';

/** Convenience: a registry already populated with paragraph + heading. */
function buildCoreRegistry(): BlockRegistry {
  const r = new BlockRegistry();
  defaultCoreBlocks(r);
  return r;
}

/** A media block we register on top of core for the category-switch test. */
const imageBlock: BlockTypeDefinition = {
  name: 'core/image',
  title: 'Image',
  category: 'media',
  attributes: {
    type: 'object',
    additionalProperties: false,
    required: ['url'],
    properties: { url: { type: 'string', format: 'uri' } },
  },
  edit: async () => ({ default: () => null }),
};

describe('<BlockInserter>', () => {
  it('renders every registered block in the active category', () => {
    const registry = buildCoreRegistry();
    render(<BlockInserter registry={registry} onInsert={vi.fn()} />);

    expect(
      screen.getByTestId('block-inserter-tile-core/paragraph'),
    ).toHaveTextContent('Paragraph');
    expect(
      screen.getByTestId('block-inserter-tile-core/heading'),
    ).toHaveTextContent('Heading');
  });

  it('filters by search query (case-insensitive title match)', () => {
    const registry = buildCoreRegistry();
    render(<BlockInserter registry={registry} onInsert={vi.fn()} />);

    const search = screen.getByTestId('block-inserter-search');
    fireEvent.change(search, { target: { value: 'head' } });

    // Heading remains, paragraph filtered out.
    expect(
      screen.getByTestId('block-inserter-tile-core/heading'),
    ).toBeInTheDocument();
    expect(
      screen.queryByTestId('block-inserter-tile-core/paragraph'),
    ).toBeNull();

    // Now type something that matches nothing → empty state.
    fireEvent.change(search, { target: { value: 'zzzzz' } });
    expect(screen.getByTestId('block-inserter-empty')).toBeInTheDocument();
  });

  it('uppercase query still matches (case-insensitive)', () => {
    const registry = buildCoreRegistry();
    render(<BlockInserter registry={registry} onInsert={vi.fn()} />);

    fireEvent.change(screen.getByTestId('block-inserter-search'), {
      target: { value: 'PARAGRAPH' },
    });
    expect(
      screen.getByTestId('block-inserter-tile-core/paragraph'),
    ).toBeInTheDocument();
  });

  it('switches list when a category tab is clicked', () => {
    const registry = buildCoreRegistry();
    registry.register(imageBlock);
    render(<BlockInserter registry={registry} onInsert={vi.fn()} />);

    // Default tab is text — paragraph & heading visible, image is not.
    expect(
      screen.getByTestId('block-inserter-tile-core/paragraph'),
    ).toBeInTheDocument();
    expect(
      screen.queryByTestId('block-inserter-tile-core/image'),
    ).toBeNull();

    // Switch to media.
    fireEvent.click(screen.getByTestId('block-inserter-tab-media'));

    expect(
      screen.queryByTestId('block-inserter-tile-core/paragraph'),
    ).toBeNull();
    expect(
      screen.getByTestId('block-inserter-tile-core/image'),
    ).toBeInTheDocument();
  });

  it('calls onInsert with the expected block shape', () => {
    const registry = buildCoreRegistry();
    const onInsert = vi.fn();
    render(<BlockInserter registry={registry} onInsert={onInsert} />);

    fireEvent.click(
      screen.getByTestId('block-inserter-tile-core/paragraph'),
    );

    expect(onInsert).toHaveBeenCalledTimes(1);
    const inserted = onInsert.mock.calls[0]?.[0] as Block;
    expect(inserted).toEqual({
      type: 'core/paragraph',
      attributes: {},
      innerBlocks: [],
    });
  });

  it('renders all seven category tabs and disables empty ones', () => {
    const registry = buildCoreRegistry();
    render(<BlockInserter registry={registry} onInsert={vi.fn()} />);

    const tablist = screen.getByRole('tablist');
    const tabs = within(tablist).getAllByRole('tab');
    expect(tabs.map((t) => t.textContent)).toEqual([
      'text',
      'media',
      'design',
      'widgets',
      'theme',
      'embed',
      'custom',
    ]);

    // `text` has entries, `media` does not (no image registered).
    expect(screen.getByTestId('block-inserter-tab-text')).not.toBeDisabled();
    expect(screen.getByTestId('block-inserter-tab-media')).toBeDisabled();
  });

  it('honors initialCategory and initialQuery props', () => {
    const registry = buildCoreRegistry();
    registry.register(imageBlock);
    render(
      <BlockInserter
        registry={registry}
        onInsert={vi.fn()}
        initialCategory="media"
        initialQuery="image"
      />,
    );

    expect(
      screen.getByTestId('block-inserter-tile-core/image'),
    ).toBeInTheDocument();
    expect(
      (screen.getByTestId('block-inserter-search') as HTMLInputElement).value,
    ).toBe('image');
  });

  it('renders inline SVG icons as markup, not text', () => {
    const registry = buildCoreRegistry();
    render(<BlockInserter registry={registry} onInsert={vi.fn()} />);

    const iconHost = screen.getByTestId(
      'block-inserter-tile-icon-core/paragraph',
    );
    expect(iconHost.querySelector('svg')).not.toBeNull();
  });

  it('renders non-SVG icon strings as plain text', () => {
    const registry = new BlockRegistry();
    registry.register({
      name: 'plugin/widget',
      title: 'Widget',
      category: 'widgets',
      icon: 'lucide:widget',
      attributes: { type: 'object', additionalProperties: true },
      edit: async () => ({ default: () => null }),
    });
    render(
      <BlockInserter
        registry={registry}
        onInsert={vi.fn()}
        initialCategory="widgets"
      />,
    );

    const iconHost = screen.getByTestId(
      'block-inserter-tile-icon-plugin/widget',
    );
    expect(iconHost.querySelector('svg')).toBeNull();
    expect(iconHost.textContent).toBe('lucide:widget');
  });

  it('handles blocks with no icon at all', () => {
    const registry = new BlockRegistry();
    registry.register({
      name: 'plugin/noicon',
      title: 'No Icon',
      category: 'custom',
      attributes: { type: 'object', additionalProperties: true },
      edit: async () => ({ default: () => null }),
    });
    render(
      <BlockInserter
        registry={registry}
        onInsert={vi.fn()}
        initialCategory="custom"
      />,
    );

    expect(
      screen.getByTestId('block-inserter-tile-plugin/noicon'),
    ).toBeInTheDocument();
  });

  it('renders a brand Lucide glyph for icon-less blocks (no plain-text id)', () => {
    const registry = new BlockRegistry();
    registry.register({
      name: 'plugin/noicon',
      title: 'No Icon',
      category: 'text',
      attributes: { type: 'object', additionalProperties: true },
      edit: async () => ({ default: () => null }),
    });
    render(<BlockInserter registry={registry} onInsert={vi.fn()} />);

    // No icon string means the inserter falls back to its inline-SVG
    // Lucide glyph map. The icon host should carry an <svg> element
    // *and* not leak the block name into the visible label.
    const iconHost = screen.getByTestId('block-inserter-tile-icon-plugin/noicon');
    expect(iconHost.querySelector('svg')).not.toBeNull();
    expect(iconHost.textContent).toBe('');
  });

  it('applies brand surface tokens to the inserter panel', () => {
    const registry = buildCoreRegistry();
    render(<BlockInserter registry={registry} onInsert={vi.fn()} />);

    const panel = screen.getByTestId('block-inserter');
    expect(panel.getAttribute('style')).toMatch(/--paper-2/);
    expect(panel.getAttribute('style')).toMatch(/--border/);
    const search = screen.getByTestId('block-inserter-search');
    expect(search.getAttribute('style')).toMatch(/--paper-3/);
    // The active tab carries the emerald-soft pill.
    const activeTab = screen.getByTestId('block-inserter-tab-text');
    expect(activeTab.getAttribute('style')).toMatch(/--emerald-soft/);
  });

  it('matches the snapshot for the default text-tab view', () => {
    const registry = buildCoreRegistry();
    const { container } = render(
      <BlockInserter registry={registry} onInsert={vi.fn()} />,
    );
    expect(container.firstChild).toMatchSnapshot();
  });
});
