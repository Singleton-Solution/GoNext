/**
 * Tests for <DocumentOutline> + buildOutline.
 *
 * The tree-builder is the part most likely to drift — we cover the
 * canonical nesting cases plus the edge cases that crop up in
 * real-world authored documents (skipped levels, level-1-only
 * trees, headings inside containers).
 *
 * The component itself is tested via DOM assertions: rows render in
 * order, clicking a row fires `onSelect` with the matching clientId,
 * and the selected row gets the emerald chrome.
 */
import { describe, expect, it, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import type { BlockTree } from '@gonext/blocks-sdk';
import { buildOutline, DocumentOutline } from './DocumentOutline.tsx';

describe('buildOutline', () => {
  it('returns empty when the tree has no headings', () => {
    expect(
      buildOutline([
        {
          type: 'core/paragraph',
          attributes: { text: 'hi' },
          clientId: 'p1',
        },
      ]),
    ).toEqual([]);
  });

  it('builds a one-level tree from H2-only blocks', () => {
    const tree: BlockTree = [
      {
        type: 'core/heading',
        attributes: { level: 2, text: 'A' },
        clientId: 'h-a',
      },
      {
        type: 'core/heading',
        attributes: { level: 2, text: 'B' },
        clientId: 'h-b',
      },
    ];
    const outline = buildOutline(tree);
    expect(outline).toHaveLength(2);
    expect(outline[0]?.text).toBe('A');
    expect(outline[0]?.children).toHaveLength(0);
    expect(outline[1]?.text).toBe('B');
  });

  it('nests higher-level headings under the most recent lower-level one', () => {
    const tree: BlockTree = [
      {
        type: 'core/heading',
        attributes: { level: 1, text: 'Top' },
        clientId: 'h1',
      },
      {
        type: 'core/heading',
        attributes: { level: 2, text: 'Section' },
        clientId: 'h2',
      },
      {
        type: 'core/heading',
        attributes: { level: 3, text: 'Subsection' },
        clientId: 'h3',
      },
      {
        type: 'core/heading',
        attributes: { level: 2, text: 'Next Section' },
        clientId: 'h4',
      },
    ];
    const outline = buildOutline(tree);
    expect(outline).toHaveLength(1); // single H1 root
    expect(outline[0]?.children).toHaveLength(2); // two H2 under it
    expect(outline[0]?.children[0]?.text).toBe('Section');
    expect(outline[0]?.children[0]?.children).toHaveLength(1); // H3
    expect(outline[0]?.children[0]?.children[0]?.text).toBe('Subsection');
    expect(outline[0]?.children[1]?.text).toBe('Next Section');
  });

  it('handles a skipped level (H1 → H3) by attaching to the nearest lower level', () => {
    const tree: BlockTree = [
      {
        type: 'core/heading',
        attributes: { level: 1, text: 'Top' },
        clientId: 'h1',
      },
      {
        type: 'core/heading',
        attributes: { level: 3, text: 'Skipped' },
        clientId: 'h2',
      },
    ];
    const outline = buildOutline(tree);
    expect(outline[0]?.children).toHaveLength(1);
    expect(outline[0]?.children[0]?.text).toBe('Skipped');
  });

  it('recurses into innerBlocks so headings inside containers count', () => {
    const tree: BlockTree = [
      {
        type: 'core/container',
        attributes: {},
        clientId: 'c1',
        innerBlocks: [
          {
            type: 'core/heading',
            attributes: { level: 2, text: 'Inside container' },
            clientId: 'h-inner',
          },
        ],
      },
    ];
    const outline = buildOutline(tree);
    expect(outline).toHaveLength(1);
    expect(outline[0]?.text).toBe('Inside container');
  });

  it('clamps malformed levels into [1..6]', () => {
    const tree: BlockTree = [
      {
        type: 'core/heading',
        attributes: { level: 99, text: 'Too high' },
        clientId: 'h99',
      },
      {
        type: 'core/heading',
        attributes: { level: 0, text: 'Too low' },
        clientId: 'h0',
      },
    ];
    const outline = buildOutline(tree);
    expect(outline[0]?.level).toBe(6);
    expect(outline[1]?.level).toBe(1);
  });
});

describe('<DocumentOutline>', () => {
  it('renders an empty state when the tree has no headings', () => {
    render(
      <DocumentOutline
        blocks={[
          {
            type: 'core/paragraph',
            attributes: { text: 'p' },
            clientId: 'p',
          },
        ]}
        onSelect={() => undefined}
      />,
    );
    expect(screen.getByTestId('document-outline-empty')).toBeInTheDocument();
    expect(
      screen.queryByTestId('document-outline-tree'),
    ).not.toBeInTheDocument();
  });

  it('renders one row per heading with the H{level} chip and text', () => {
    render(
      <DocumentOutline
        blocks={[
          {
            type: 'core/heading',
            attributes: { level: 1, text: 'Hello' },
            clientId: 'h-1',
          },
          {
            type: 'core/heading',
            attributes: { level: 2, text: 'World' },
            clientId: 'h-2',
          },
        ]}
        onSelect={() => undefined}
      />,
    );
    const row1 = screen.getByTestId('document-outline-row-h-1');
    expect(row1).toBeInTheDocument();
    expect(row1.getAttribute('data-level')).toBe('1');
    expect(row1).toHaveTextContent('H1');
    expect(row1).toHaveTextContent('Hello');

    const row2 = screen.getByTestId('document-outline-row-h-2');
    expect(row2.getAttribute('data-level')).toBe('2');
    expect(row2).toHaveTextContent('World');
  });

  it('emits onSelect(clientId) when a row is clicked', () => {
    const onSelect = vi.fn();
    render(
      <DocumentOutline
        blocks={[
          {
            type: 'core/heading',
            attributes: { level: 2, text: 'Section' },
            clientId: 'h-s',
          },
        ]}
        onSelect={onSelect}
      />,
    );
    act(() => {
      screen.getByTestId('document-outline-row-h-s').click();
    });
    expect(onSelect).toHaveBeenCalledWith('h-s');
  });

  it('marks the currently selected row with emerald chrome', () => {
    render(
      <DocumentOutline
        blocks={[
          {
            type: 'core/heading',
            attributes: { level: 2, text: 'A' },
            clientId: 'h-a',
          },
          {
            type: 'core/heading',
            attributes: { level: 2, text: 'B' },
            clientId: 'h-b',
          },
        ]}
        selectedClientId="h-b"
        onSelect={() => undefined}
      />,
    );
    expect(
      screen
        .getByTestId('document-outline-row-h-b')
        .getAttribute('data-selected'),
    ).toBe('true');
    expect(
      screen
        .getByTestId('document-outline-row-h-a')
        .getAttribute('data-selected'),
    ).toBe('false');
    expect(
      screen
        .getByTestId('document-outline-row-h-b')
        .getAttribute('style'),
    ).toMatch(/--emerald-soft/);
  });

  it('shows "Untitled heading" when the heading text is empty', () => {
    render(
      <DocumentOutline
        blocks={[
          {
            type: 'core/heading',
            attributes: { level: 2, text: '' },
            clientId: 'h-empty',
          },
        ]}
        onSelect={() => undefined}
      />,
    );
    expect(
      screen.getByTestId('document-outline-row-h-empty'),
    ).toHaveTextContent('Untitled heading');
  });
});
