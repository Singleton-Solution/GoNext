/**
 * Visual-chrome snapshots for `<BlockEditCanvas>`.
 *
 * These are intentionally narrow — we don't want to lock in
 * the entire tree, just the brand-relevant class hooks and
 * data-attribute markers the editor-theme.css restyle depends
 * on. If somebody renames `gonext-block-edit-canvas` or removes
 * `data-depth`, the css breaks and we want to know loud and early.
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

function buildRegistry(): BlockRegistry {
  const r = new BlockRegistry();
  const Paragraph = ({ attributes }: BlockEditProps<{ text: string }>) => (
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

describe('<BlockEditCanvas> brand chrome', () => {
  it('exposes the document-chip class hook on the root', () => {
    const registry = buildRegistry();
    clearEditModuleCache(registry);

    const { container } = render(
      <BlockEditCanvas registry={registry} blocks={[]} />,
    );

    const root = container.querySelector('.gonext-block-edit-canvas');
    expect(root).not.toBeNull();
  });

  it('top-level blocks carry data-depth="0"; nested carry depth+1', async () => {
    const registry = buildRegistry();
    clearEditModuleCache(registry);

    let canvas!: HTMLElement;
    await act(async () => {
      const { container } = render(
        <BlockEditCanvas
          registry={registry}
          blocks={[
            {
              type: 'core/container',
              attributes: {},
              innerBlocks: [
                {
                  type: 'core/paragraph',
                  attributes: { text: 'nested' },
                },
              ],
            },
          ]}
        />,
      );
      canvas = container as unknown as HTMLElement;
    });

    const top = canvas.querySelector(
      '[data-block-type="core/container"]',
    ) as HTMLElement;
    expect(top.getAttribute('data-depth')).toBe('0');

    const nested = canvas.querySelector(
      '[data-block-type="core/paragraph"]',
    ) as HTMLElement;
    expect(nested.getAttribute('data-depth')).toBe('1');
  });

  it('matches the brand-chrome snapshot for a single paragraph', async () => {
    const registry = buildRegistry();
    clearEditModuleCache(registry);

    await act(async () => {
      render(
        <BlockEditCanvas
          registry={registry}
          blocks={[
            {
              type: 'core/paragraph',
              attributes: { text: 'Hello.' },
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

    const root = screen.getByTestId('block-edit-canvas');
    // Tiny, stable snapshot — covers the class hooks our CSS reads.
    expect(root.outerHTML).toMatchInlineSnapshot(
      `"<div class="gonext-block-edit-canvas" data-testid="block-edit-canvas"><div class="gonext-block-edit-canvas__node" data-block-type="core/paragraph" data-depth="0" data-testid="block-edit-canvas-node-core/paragraph"><p data-block="core/paragraph">Hello.</p></div></div>"`,
    );
  });
});
