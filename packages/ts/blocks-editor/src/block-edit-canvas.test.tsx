/**
 * Tests for <BlockEditCanvas>.
 *
 * The canvas is a scaffold: its job is to walk the tree, resolve each
 * block's lazy `edit()` import, and render the result. We check:
 *
 *  - The expected `edit` component is rendered for each known block.
 *  - Unknown block types fall back to an "Unknown block" placeholder
 *    without breaking the rest of the tree.
 *  - `innerBlocks` are recursively rendered.
 *  - `clearEditModuleCache` does what it says on the tin.
 */
import { describe, expect, it } from 'vitest';
import { act, render, screen, waitFor } from '@testing-library/react';
import {
  BlockRegistry,
  type BlockEditProps,
  type BlockTypeDefinition,
} from '@gonext/blocks-sdk';
import {
  BlockEditCanvas,
  clearEditModuleCache,
} from './block-edit-canvas.tsx';

/**
 * Build a registry pre-populated with a paragraph that renders a real
 * React element (so we can assert on its DOM output). The eager edit
 * factory is still wrapped in a Promise to mirror the lazy-import shape.
 */
function buildRegistry(): BlockRegistry {
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

describe('<BlockEditCanvas>', () => {
  it('renders the registered edit component for each block', async () => {
    const registry = buildRegistry();
    clearEditModuleCache(registry);

    await act(async () => {
      render(
        <BlockEditCanvas
          registry={registry}
          blocks={[
            {
              type: 'core/paragraph',
              attributes: { text: 'Hello, world.' },
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
      screen.getByTestId('block-edit-canvas-node-core/paragraph'),
    ).toHaveTextContent('Hello, world.');
  });

  it('falls back to an "Unknown block" placeholder for unregistered types', async () => {
    const registry = buildRegistry();
    clearEditModuleCache(registry);

    await act(async () => {
      render(
        <BlockEditCanvas
          registry={registry}
          blocks={[
            { type: 'plugin/missing', attributes: {} },
            {
              type: 'core/paragraph',
              attributes: { text: 'still rendered' },
            },
          ]}
        />,
      );
    });

    await waitFor(() => {
      expect(
        screen.getByTestId('block-edit-canvas-unknown'),
      ).toBeInTheDocument();
    });

    // Sibling rendering must not be affected.
    expect(
      screen.getByTestId('block-edit-canvas-node-core/paragraph'),
    ).toHaveTextContent('still rendered');
  });

  it('recurses into innerBlocks', async () => {
    const registry = buildRegistry();
    clearEditModuleCache(registry);

    await act(async () => {
      render(
        <BlockEditCanvas
          registry={registry}
          blocks={[
            {
              type: 'core/container',
              attributes: {},
              innerBlocks: [
                {
                  type: 'core/paragraph',
                  attributes: { text: 'nested child' },
                },
              ],
            },
          ]}
        />,
      );
    });

    await waitFor(() => {
      expect(
        screen.getByTestId('block-edit-canvas-node-core/container'),
      ).toBeInTheDocument();
    });
    expect(
      screen.getByTestId('block-edit-canvas-node-core/paragraph'),
    ).toHaveTextContent('nested child');
  });

  it('renders nothing when blocks is empty', () => {
    const registry = buildRegistry();
    clearEditModuleCache(registry);

    render(<BlockEditCanvas registry={registry} blocks={[]} />);

    expect(screen.getByTestId('block-edit-canvas')).toBeEmptyDOMElement();
  });

  it('clearEditModuleCache forgets cached imports for a registry', () => {
    const registry = buildRegistry();
    // We can't easily observe module cache contents from outside, but
    // we can at least make sure the call doesn't throw and is idempotent.
    expect(() => clearEditModuleCache(registry)).not.toThrow();
    expect(() => clearEditModuleCache(registry)).not.toThrow();
  });

  it('wraps the tree in the paper document chip when blocks are present', async () => {
    const registry = buildRegistry();
    clearEditModuleCache(registry);

    await act(async () => {
      render(
        <BlockEditCanvas
          registry={registry}
          blocks={[
            { type: 'core/paragraph', attributes: { text: 'Hello.' } },
          ]}
        />,
      );
    });

    await waitFor(() => {
      expect(
        screen.getByTestId('block-edit-canvas-node-core/paragraph'),
      ).toBeInTheDocument();
    });
    const chip = screen.getByTestId('block-edit-canvas-doc');
    expect(chip).toBeInTheDocument();
    // The doc chip carries the brand surface tokens — paper-2 background,
    // the soft drop shadow, a rounded corner. We sniff inline styles
    // here rather than the computed CSS so the assertion stays stable
    // in jsdom (no tokens.css loaded).
    expect(chip.getAttribute('style')).toMatch(/--paper-2/);
    expect(chip.getAttribute('style')).toMatch(/--sh-md/);
    expect(chip.getAttribute('style')).toMatch(/--r-lg/);
  });

  it('applies emerald selection chrome to the matching block', async () => {
    const registry = buildRegistry();
    clearEditModuleCache(registry);

    await act(async () => {
      render(
        <BlockEditCanvas
          registry={registry}
          blocks={[
            {
              type: 'core/paragraph',
              clientId: 'block-1',
              attributes: { text: 'Selected.' },
            },
            {
              type: 'core/paragraph',
              clientId: 'block-2',
              attributes: { text: 'Not selected.' },
            },
          ]}
          selectedClientId="block-1"
        />,
      );
    });

    await waitFor(() => {
      expect(
        screen.getAllByTestId('block-edit-canvas-node-core/paragraph'),
      ).toHaveLength(2);
    });
    const nodes = screen.getAllByTestId(
      'block-edit-canvas-node-core/paragraph',
    );
    expect(nodes[0]?.getAttribute('data-selected')).toBe('true');
    expect(nodes[0]?.getAttribute('style')).toMatch(/--emerald/);
    expect(nodes[1]?.getAttribute('data-selected')).toBe('false');
  });

  it('matches the canvas snapshot for a single-paragraph tree', async () => {
    const registry = buildRegistry();
    clearEditModuleCache(registry);

    let container: HTMLElement;
    await act(async () => {
      const result = render(
        <BlockEditCanvas
          registry={registry}
          blocks={[
            {
              type: 'core/paragraph',
              clientId: 'p-1',
              attributes: { text: 'Snapshot.' },
            },
          ]}
        />,
      );
      container = result.container;
    });

    await waitFor(() => {
      expect(
        screen.getByTestId('block-edit-canvas-node-core/paragraph'),
      ).toBeInTheDocument();
    });
    expect(container!.firstChild).toMatchSnapshot();
  });
});
