/**
 * Tests for the Patterns tab support inside <BlockInserter>.
 *
 * Contract under test:
 *   1. With no `patternRegistry`, the Patterns tab is not rendered.
 *   2. With a populated `patternRegistry`, the Patterns tab is rendered
 *      and clickable.
 *   3. Activating the tab swaps the panel content for pattern tiles
 *      grouped by category.
 *   4. Pattern tiles render every pattern returned by `list()`.
 *   5. Search filtering applies to both `name` and `keywords`.
 *   6. Clicking a pattern tile fires `onInsertPattern` with a deep copy
 *      of the pattern's BlockTree (no shared references).
 *   7. The Patterns tab is disabled when the registry is empty.
 *   8. `initialCategory: BLOCK_INSERTER_PATTERNS_TAB` lands on the
 *      Patterns tab.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import {
  BLOCK_INSERTER_PATTERNS_TAB,
  BlockInserter,
} from './block-inserter.tsx';
import { defaultCoreBlocks } from './default-core-blocks.ts';
import type { Pattern, PatternRegistry } from './pattern-types.ts';

/** Build a minimal pattern registry stub from a flat list of patterns. */
function makePatternRegistry(patterns: Pattern[]): PatternRegistry {
  return {
    list: () => patterns,
  };
}

/** A heading-only pattern so we can assert on tile content + insertion shape. */
const heroPattern: Pattern = {
  id: 'test/hero',
  name: 'Test Hero',
  category: 'hero',
  description: 'A test hero pattern',
  keywords: ['banner', 'landing'],
  preview: '/patterns/hero.svg',
  blocks: [
    {
      type: 'core/heading',
      attributes: { content: 'Hello', level: 1 },
    },
  ],
};

const featuresPattern: Pattern = {
  id: 'test/features',
  name: 'Test Features',
  category: 'features',
  keywords: ['grid'],
  blocks: [
    {
      type: 'core/paragraph',
      attributes: { content: 'Features intro' },
    },
  ],
};

const ctaPattern: Pattern = {
  id: 'test/cta',
  name: 'Test CTA',
  category: 'cta',
  blocks: [
    {
      type: 'core/paragraph',
      attributes: { content: 'Try it' },
    },
  ],
};

function buildRegistries(patterns: Pattern[]) {
  const blocks = new BlockRegistry();
  defaultCoreBlocks(blocks);
  const patternRegistry = makePatternRegistry(patterns);
  return { blocks, patternRegistry };
}

describe('<BlockInserter> Patterns tab', () => {
  it('does not render the Patterns tab when patternRegistry is absent', () => {
    const { blocks } = buildRegistries([]);
    render(<BlockInserter registry={blocks} onInsert={vi.fn()} />);
    expect(screen.queryByTestId('block-inserter-tab-patterns')).toBeNull();
  });

  it('renders the Patterns tab when a patternRegistry is provided', () => {
    const { blocks, patternRegistry } = buildRegistries([heroPattern]);
    render(
      <BlockInserter
        registry={blocks}
        patternRegistry={patternRegistry}
        onInsert={vi.fn()}
        onInsertPattern={vi.fn()}
      />,
    );
    expect(
      screen.getByTestId('block-inserter-tab-patterns'),
    ).toBeInTheDocument();
  });

  it('disables the Patterns tab when the registry is empty', () => {
    const { blocks, patternRegistry } = buildRegistries([]);
    render(
      <BlockInserter
        registry={blocks}
        patternRegistry={patternRegistry}
        onInsert={vi.fn()}
        onInsertPattern={vi.fn()}
      />,
    );
    expect(
      screen.getByTestId('block-inserter-tab-patterns'),
    ).toBeDisabled();
  });

  it('renders every registered pattern when the Patterns tab is active', () => {
    const { blocks, patternRegistry } = buildRegistries([
      heroPattern,
      featuresPattern,
      ctaPattern,
    ]);
    render(
      <BlockInserter
        registry={blocks}
        patternRegistry={patternRegistry}
        onInsert={vi.fn()}
        onInsertPattern={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByTestId('block-inserter-tab-patterns'));

    expect(
      screen.getByTestId('block-inserter-pattern-tile-test/hero'),
    ).toBeInTheDocument();
    expect(
      screen.getByTestId('block-inserter-pattern-tile-test/features'),
    ).toBeInTheDocument();
    expect(
      screen.getByTestId('block-inserter-pattern-tile-test/cta'),
    ).toBeInTheDocument();
  });

  it('groups patterns by category', () => {
    const { blocks, patternRegistry } = buildRegistries([
      heroPattern,
      featuresPattern,
      ctaPattern,
    ]);
    render(
      <BlockInserter
        registry={blocks}
        patternRegistry={patternRegistry}
        onInsert={vi.fn()}
        onInsertPattern={vi.fn()}
        initialCategory={BLOCK_INSERTER_PATTERNS_TAB}
      />,
    );

    const heroGroup = screen.getByTestId('block-inserter-pattern-group-hero');
    expect(
      within(heroGroup).getByTestId('block-inserter-pattern-tile-test/hero'),
    ).toBeInTheDocument();
    // Cross-category tiles must NOT leak into the hero group.
    expect(
      within(heroGroup).queryByTestId(
        'block-inserter-pattern-tile-test/features',
      ),
    ).toBeNull();
  });

  it('search filters patterns by name (case-insensitive)', () => {
    const { blocks, patternRegistry } = buildRegistries([
      heroPattern,
      featuresPattern,
    ]);
    render(
      <BlockInserter
        registry={blocks}
        patternRegistry={patternRegistry}
        onInsert={vi.fn()}
        onInsertPattern={vi.fn()}
        initialCategory={BLOCK_INSERTER_PATTERNS_TAB}
      />,
    );

    fireEvent.change(screen.getByTestId('block-inserter-search'), {
      target: { value: 'HERO' },
    });

    expect(
      screen.getByTestId('block-inserter-pattern-tile-test/hero'),
    ).toBeInTheDocument();
    expect(
      screen.queryByTestId('block-inserter-pattern-tile-test/features'),
    ).toBeNull();
  });

  it('search also matches keywords', () => {
    const { blocks, patternRegistry } = buildRegistries([
      heroPattern,
      featuresPattern,
    ]);
    render(
      <BlockInserter
        registry={blocks}
        patternRegistry={patternRegistry}
        onInsert={vi.fn()}
        onInsertPattern={vi.fn()}
        initialCategory={BLOCK_INSERTER_PATTERNS_TAB}
      />,
    );

    // "landing" only appears in heroPattern.keywords, not in any name.
    fireEvent.change(screen.getByTestId('block-inserter-search'), {
      target: { value: 'landing' },
    });

    expect(
      screen.getByTestId('block-inserter-pattern-tile-test/hero'),
    ).toBeInTheDocument();
    expect(
      screen.queryByTestId('block-inserter-pattern-tile-test/features'),
    ).toBeNull();
  });

  it('shows an empty state when search filters every pattern out', () => {
    const { blocks, patternRegistry } = buildRegistries([heroPattern]);
    render(
      <BlockInserter
        registry={blocks}
        patternRegistry={patternRegistry}
        onInsert={vi.fn()}
        onInsertPattern={vi.fn()}
        initialCategory={BLOCK_INSERTER_PATTERNS_TAB}
      />,
    );

    fireEvent.change(screen.getByTestId('block-inserter-search'), {
      target: { value: 'no-match-string' },
    });
    expect(
      screen.getByTestId('block-inserter-empty-patterns'),
    ).toBeInTheDocument();
  });

  it('clicking a pattern tile fires onInsertPattern with the pattern blocks', () => {
    const { blocks, patternRegistry } = buildRegistries([heroPattern]);
    const onInsertPattern = vi.fn();
    render(
      <BlockInserter
        registry={blocks}
        patternRegistry={patternRegistry}
        onInsert={vi.fn()}
        onInsertPattern={onInsertPattern}
        initialCategory={BLOCK_INSERTER_PATTERNS_TAB}
      />,
    );

    fireEvent.click(
      screen.getByTestId('block-inserter-pattern-tile-test/hero'),
    );

    expect(onInsertPattern).toHaveBeenCalledTimes(1);
    const [insertedTree, insertedPattern] = onInsertPattern.mock.calls[0]!;
    expect(insertedPattern.id).toBe('test/hero');
    expect(insertedTree).toEqual(heroPattern.blocks);
  });

  it('hands onInsertPattern a deep copy (no aliasing)', () => {
    const { blocks, patternRegistry } = buildRegistries([heroPattern]);
    const onInsertPattern = vi.fn();
    render(
      <BlockInserter
        registry={blocks}
        patternRegistry={patternRegistry}
        onInsert={vi.fn()}
        onInsertPattern={onInsertPattern}
        initialCategory={BLOCK_INSERTER_PATTERNS_TAB}
      />,
    );

    fireEvent.click(
      screen.getByTestId('block-inserter-pattern-tile-test/hero'),
    );

    const insertedTree = onInsertPattern.mock.calls[0]![0];
    // Reference equality must fail at every level — root array, root
    // block, attribute object.
    expect(insertedTree).not.toBe(heroPattern.blocks);
    expect(insertedTree[0]).not.toBe(heroPattern.blocks[0]);
    expect(insertedTree[0].attributes).not.toBe(
      heroPattern.blocks[0]?.attributes,
    );
  });

  it('honors initialCategory: BLOCK_INSERTER_PATTERNS_TAB', () => {
    const { blocks, patternRegistry } = buildRegistries([heroPattern]);
    render(
      <BlockInserter
        registry={blocks}
        patternRegistry={patternRegistry}
        onInsert={vi.fn()}
        onInsertPattern={vi.fn()}
        initialCategory={BLOCK_INSERTER_PATTERNS_TAB}
      />,
    );

    expect(
      screen.getByTestId('block-inserter-panel-patterns'),
    ).toBeInTheDocument();
  });

  it('still renders the regular block category tabs when patterns are present', () => {
    const { blocks, patternRegistry } = buildRegistries([heroPattern]);
    render(
      <BlockInserter
        registry={blocks}
        patternRegistry={patternRegistry}
        onInsert={vi.fn()}
        onInsertPattern={vi.fn()}
      />,
    );

    // Existing block tabs must be unaffected by the Patterns addition.
    expect(
      screen.getByTestId('block-inserter-tab-text'),
    ).toBeInTheDocument();
    // And the regular tile for paragraph still renders.
    expect(
      screen.getByTestId('block-inserter-tile-core/paragraph'),
    ).toBeInTheDocument();
  });

  it('renders the placeholder preview span when pattern.preview is absent', () => {
    const noPreview: Pattern = { ...ctaPattern };
    const { blocks, patternRegistry } = buildRegistries([noPreview]);
    render(
      <BlockInserter
        registry={blocks}
        patternRegistry={patternRegistry}
        onInsert={vi.fn()}
        onInsertPattern={vi.fn()}
        initialCategory={BLOCK_INSERTER_PATTERNS_TAB}
      />,
    );
    expect(
      screen.getByTestId(
        'block-inserter-pattern-preview-empty-test/cta',
      ),
    ).toBeInTheDocument();
  });
});
