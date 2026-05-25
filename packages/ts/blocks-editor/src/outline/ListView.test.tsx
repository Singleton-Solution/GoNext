/**
 * Tests for <ListView> + flattenBlocks.
 *
 * The flattener is a DFS — we cover the canonical depth-first order
 * and the clientId fallback (depth-derived path when the block has
 * no explicit id).
 *
 * The component is tested via DOM assertions: rows render in order,
 * the type chip + preview show up, hover fires `onHover`, click
 * fires `onSelect`, the selected row uses emerald chrome, and the
 * multi-select set tints rows softly.
 */
import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import type { BlockTree } from '@gonext/blocks-sdk';
import { flattenBlocks, ListView } from './ListView.tsx';

describe('flattenBlocks', () => {
  it('returns one entry per block in DFS order with correct depths', () => {
    const tree: BlockTree = [
      {
        type: 'core/heading',
        attributes: { level: 1, text: 'Top' },
        clientId: 'h-1',
      },
      {
        type: 'core/container',
        attributes: {},
        clientId: 'c-1',
        innerBlocks: [
          {
            type: 'core/paragraph',
            attributes: { text: 'one' },
            clientId: 'p-1',
          },
          {
            type: 'core/paragraph',
            attributes: { text: 'two' },
            clientId: 'p-2',
          },
        ],
      },
      {
        type: 'core/paragraph',
        attributes: { text: 'tail' },
        clientId: 'p-3',
      },
    ];
    const rows = flattenBlocks(tree);
    expect(rows.map((r) => r.clientId)).toEqual([
      'h-1',
      'c-1',
      'p-1',
      'p-2',
      'p-3',
    ]);
    expect(rows.map((r) => r.depth)).toEqual([0, 0, 1, 1, 0]);
  });

  it('synthesises a clientId when none is present', () => {
    const tree: BlockTree = [
      { type: 'core/paragraph', attributes: { text: 'no-id' } },
    ];
    const rows = flattenBlocks(tree);
    expect(rows[0]?.clientId).toMatch(/^core\/paragraph-/);
  });
});

describe('<ListView>', () => {
  const tree: BlockTree = [
    {
      type: 'core/heading',
      attributes: { level: 2, text: 'Section' },
      clientId: 'h-1',
    },
    {
      type: 'core/paragraph',
      attributes: { text: 'Some prose text here.' },
      clientId: 'p-1',
    },
  ];

  it('renders the empty state when the tree is empty', () => {
    render(<ListView blocks={[]} onSelect={() => undefined} />);
    expect(screen.getByTestId('list-view-empty')).toBeInTheDocument();
  });

  it('renders one row per block with the type chip + preview', () => {
    render(<ListView blocks={tree} onSelect={() => undefined} />);
    const heading = screen.getByTestId('list-view-row-h-1');
    expect(heading.getAttribute('data-block-type')).toBe('core/heading');
    expect(heading).toHaveTextContent('Section');

    const para = screen.getByTestId('list-view-row-p-1');
    expect(para).toHaveTextContent('Some prose text here.');
  });

  it('emits onSelect when a row is clicked', () => {
    const onSelect = vi.fn();
    render(<ListView blocks={tree} onSelect={onSelect} />);
    act(() => {
      screen.getByTestId('list-view-row-p-1').click();
    });
    expect(onSelect).toHaveBeenCalledWith('p-1');
  });

  it('emits onHover on mouse-enter / mouse-leave', () => {
    const onHover = vi.fn();
    render(
      <ListView
        blocks={tree}
        onSelect={() => undefined}
        onHover={onHover}
      />,
    );
    fireEvent.mouseEnter(screen.getByTestId('list-view-row-h-1'));
    expect(onHover).toHaveBeenLastCalledWith('h-1');
    fireEvent.mouseLeave(screen.getByTestId('list-view-row-h-1'));
    expect(onHover).toHaveBeenLastCalledWith(null);
  });

  it('marks the selected row with emerald chrome', () => {
    render(
      <ListView
        blocks={tree}
        selectedClientId="p-1"
        onSelect={() => undefined}
      />,
    );
    expect(
      screen.getByTestId('list-view-row-p-1').getAttribute('data-selected'),
    ).toBe('true');
    expect(
      screen.getByTestId('list-view-row-p-1').getAttribute('style'),
    ).toMatch(/--emerald-soft/);
  });

  it('tints rows in the multi-select set without overriding the primary selection', () => {
    render(
      <ListView
        blocks={tree}
        selectedClientId="h-1"
        selectedIds={new Set(['h-1', 'p-1'])}
        onSelect={() => undefined}
      />,
    );
    // h-1 is the primary selection → emerald-soft, NOT multi tint.
    expect(
      screen.getByTestId('list-view-row-h-1').getAttribute('data-multi'),
    ).toBe('false');
    expect(
      screen.getByTestId('list-view-row-h-1').getAttribute('data-selected'),
    ).toBe('true');
    // p-1 is in the set but not primary → multi tint.
    expect(
      screen.getByTestId('list-view-row-p-1').getAttribute('data-multi'),
    ).toBe('true');
  });

  it('previews fall back to code / url when text is missing', () => {
    render(
      <ListView
        blocks={[
          {
            type: 'core/code',
            attributes: { code: "console.log('hi')" },
            clientId: 'c1',
          },
          {
            type: 'core/image',
            attributes: { url: 'https://example.com/x.png' },
            clientId: 'img',
          },
        ]}
        onSelect={() => undefined}
      />,
    );
    expect(screen.getByTestId('list-view-row-c1')).toHaveTextContent(
      "console.log('hi')",
    );
    expect(screen.getByTestId('list-view-row-img')).toHaveTextContent(
      'https://example.com/x.png',
    );
  });

  it('truncates long previews to ~60 chars with an ellipsis', () => {
    const long = 'x'.repeat(120);
    render(
      <ListView
        blocks={[
          {
            type: 'core/paragraph',
            attributes: { text: long },
            clientId: 'p-long',
          },
        ]}
        onSelect={() => undefined}
      />,
    );
    expect(screen.getByTestId('list-view-row-p-long')).toHaveTextContent(
      /x{57}…/,
    );
  });
});
