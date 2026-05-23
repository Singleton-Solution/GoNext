/**
 * Tests for `<BlockEditCanvas>`'s transform-toolbar integration.
 *
 * Contract under test (this issue's editor-side acceptance criteria):
 *  1. When `transformRegistry` + `onApplyTransform` are passed, every
 *     block in the tree renders a "Transform to..." dropdown.
 *  2. The dropdown's options reflect the transforms registered for
 *     that block's type.
 *  3. Clicking an option calls `onApplyTransform(block, id, transform)`.
 *  4. When `transformRegistry` is omitted, the toolbar is not rendered
 *     — the canvas degrades back to its original walker shape.
 *  5. Child blocks (innerBlocks) also receive a toolbar.
 */
import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { Block } from '@gonext/blocks-sdk';
import {
  BlockRegistry,
  type BlockEditProps,
  type BlockTypeDefinition,
} from '@gonext/blocks-sdk';
import {
  BlockEditCanvas,
  clearEditModuleCache,
} from './block-edit-canvas.tsx';
import type {
  Transform,
  TransformRegistry,
} from './transform-types.ts';

function buildBlockRegistry(): BlockRegistry {
  const r = new BlockRegistry();

  const Paragraph = ({
    attributes,
  }: BlockEditProps<{ text: string }>) => (
    <p data-block="core/paragraph">{attributes.text}</p>
  );

  const paragraph: BlockTypeDefinition<{ text: string }> = {
    name: 'core/paragraph',
    title: 'Paragraph',
    category: 'text',
    attributes: {
      type: 'object',
      additionalProperties: false,
      required: ['text'],
      properties: { text: { type: 'string' } },
    },
    edit: async () => ({ default: Paragraph as unknown as never }),
  };

  const Container = () => <section data-block="core/container" />;

  const container: BlockTypeDefinition = {
    name: 'core/container',
    title: 'Container',
    category: 'design',
    attributes: { type: 'object', additionalProperties: true },
    edit: async () => ({ default: Container as unknown as never }),
  };

  r.register(paragraph);
  r.register(container);
  return r;
}

function stubTransformRegistry(
  transforms: Transform[],
): TransformRegistry {
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

const paragraphToHeading: Transform = {
  id: 'core/paragraph-to-heading',
  from: 'core/paragraph',
  to: 'core/heading',
  label: 'Heading',
  convert: (b) => ({
    type: 'core/heading',
    attributes: {
      content: (b.attributes['text'] as string) ?? '',
      level: 2,
    },
  }),
};

const paragraphToQuote: Transform = {
  id: 'core/paragraph-to-quote',
  from: 'core/paragraph',
  to: 'core/quote',
  label: 'Quote',
  convert: (b) => ({
    type: 'core/quote',
    attributes: { value: (b.attributes['text'] as string) ?? '' },
  }),
};

describe('<BlockEditCanvas> transform-toolbar integration', () => {
  it('renders a "Transform to..." toolbar next to every block when wired', async () => {
    const blockRegistry = buildBlockRegistry();
    const transformRegistry = stubTransformRegistry([
      paragraphToHeading,
      paragraphToQuote,
    ]);
    clearEditModuleCache(blockRegistry);

    await act(async () => {
      render(
        <BlockEditCanvas
          registry={blockRegistry}
          blocks={[
            {
              type: 'core/paragraph',
              attributes: { text: 'hello' },
            },
          ]}
          transformRegistry={transformRegistry}
          onApplyTransform={vi.fn()}
        />,
      );
    });

    await waitFor(() => {
      expect(
        screen.getByTestId('block-edit-canvas-node-core/paragraph'),
      ).toBeInTheDocument();
    });

    // The toolbar is rendered (the toggle test id is stable per block).
    const toolbar = screen.getByTestId('block-transform-toolbar');
    expect(toolbar).toBeInTheDocument();
    expect(toolbar).toHaveAttribute('data-block-type', 'core/paragraph');
  });

  it('the dropdown shows the right options for the current selection', async () => {
    const blockRegistry = buildBlockRegistry();
    const transformRegistry = stubTransformRegistry([
      paragraphToHeading,
      paragraphToQuote,
    ]);
    clearEditModuleCache(blockRegistry);

    await act(async () => {
      render(
        <BlockEditCanvas
          registry={blockRegistry}
          blocks={[
            {
              type: 'core/paragraph',
              attributes: { text: 'hi' },
            },
          ]}
          transformRegistry={transformRegistry}
          onApplyTransform={vi.fn()}
        />,
      );
    });

    await waitFor(() => {
      expect(
        screen.getByTestId('block-edit-canvas-node-core/paragraph'),
      ).toBeInTheDocument();
    });

    // Open the dropdown.
    fireEvent.click(
      screen.getByTestId('block-transform-toolbar-toggle'),
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

  it('picking an option fires onApplyTransform with (block, id, transform)', async () => {
    const blockRegistry = buildBlockRegistry();
    const transformRegistry = stubTransformRegistry([paragraphToHeading]);
    clearEditModuleCache(blockRegistry);

    const onApplyTransform = vi.fn();
    const sourceBlock: Block = {
      type: 'core/paragraph',
      attributes: { text: 'hi' },
    };

    await act(async () => {
      render(
        <BlockEditCanvas
          registry={blockRegistry}
          blocks={[sourceBlock]}
          transformRegistry={transformRegistry}
          onApplyTransform={onApplyTransform}
        />,
      );
    });

    await waitFor(() => {
      expect(
        screen.getByTestId('block-edit-canvas-node-core/paragraph'),
      ).toBeInTheDocument();
    });

    fireEvent.click(
      screen.getByTestId('block-transform-toolbar-toggle'),
    );
    fireEvent.click(
      screen.getByTestId(
        'block-transform-toolbar-option-core/paragraph-to-heading',
      ),
    );

    expect(onApplyTransform).toHaveBeenCalledTimes(1);
    const [calledBlock, calledId, calledTransform] =
      onApplyTransform.mock.calls[0]!;
    expect(calledBlock).toBe(sourceBlock);
    expect(calledId).toBe('core/paragraph-to-heading');
    expect(calledTransform).toBe(paragraphToHeading);
  });

  it('does not render the toolbar when transformRegistry is omitted', async () => {
    const blockRegistry = buildBlockRegistry();
    clearEditModuleCache(blockRegistry);

    await act(async () => {
      render(
        <BlockEditCanvas
          registry={blockRegistry}
          blocks={[
            {
              type: 'core/paragraph',
              attributes: { text: 'hi' },
            },
          ]}
        />,
      );
    });

    await waitFor(() => {
      expect(
        screen.getByTestId('block-edit-canvas-node-core/paragraph'),
      ).toBeInTheDocument();
    });

    expect(
      screen.queryByTestId('block-transform-toolbar'),
    ).toBeNull();
  });

  it('renders a toolbar for every nested child too', async () => {
    const blockRegistry = buildBlockRegistry();
    const transformRegistry = stubTransformRegistry([paragraphToHeading]);
    clearEditModuleCache(blockRegistry);

    await act(async () => {
      render(
        <BlockEditCanvas
          registry={blockRegistry}
          blocks={[
            {
              type: 'core/container',
              attributes: {},
              innerBlocks: [
                {
                  type: 'core/paragraph',
                  attributes: { text: 'inner' },
                },
              ],
            },
          ]}
          transformRegistry={transformRegistry}
          onApplyTransform={vi.fn()}
        />,
      );
    });

    await waitFor(() => {
      expect(
        screen.getByTestId('block-edit-canvas-node-core/container'),
      ).toBeInTheDocument();
    });

    // Two toolbars in total: container + nested paragraph.
    const toolbars = screen.getAllByTestId('block-transform-toolbar');
    expect(toolbars).toHaveLength(2);

    const types = toolbars.map((el) =>
      el.getAttribute('data-block-type'),
    );
    expect(types).toContain('core/container');
    expect(types).toContain('core/paragraph');
  });
});
