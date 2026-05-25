/**
 * Brand-chrome snapshots for `<BlockInserter>`.
 *
 * Locks in the BEM class hooks + data-active markers the editor-theme
 * stylesheet reads from, plus the new search-row wrapper that carries
 * the Lucide search glyph.
 */
import { describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { BlockInserter } from './block-inserter.tsx';
import { defaultCoreBlocks } from './default-core-blocks.ts';
import { BlockRegistry } from '@gonext/blocks-sdk';

function buildCoreRegistry(): BlockRegistry {
  const r = new BlockRegistry();
  defaultCoreBlocks(r);
  return r;
}

describe('<BlockInserter> brand chrome', () => {
  it('wraps the search input in a search-row carrying the Lucide search glyph', () => {
    const registry = buildCoreRegistry();
    render(<BlockInserter registry={registry} onInsert={vi.fn()} />);

    const row = screen.getByTestId('block-inserter-search-row');
    expect(row).not.toBeNull();
    expect(row.querySelector('.gonext-block-inserter__search-icon svg')).not.toBeNull();
  });

  it('the active category tab carries data-active="true"; others false', () => {
    const registry = buildCoreRegistry();
    // Register a media block so the media tab is clickable.
    registry.register({
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
    });
    render(<BlockInserter registry={registry} onInsert={vi.fn()} />);

    expect(
      screen.getByTestId('block-inserter-tab-text').getAttribute('data-active'),
    ).toBe('true');
    expect(
      screen.getByTestId('block-inserter-tab-media').getAttribute('data-active'),
    ).toBe('false');

    fireEvent.click(screen.getByTestId('block-inserter-tab-media'));
    expect(
      screen.getByTestId('block-inserter-tab-text').getAttribute('data-active'),
    ).toBe('false');
    expect(
      screen.getByTestId('block-inserter-tab-media').getAttribute('data-active'),
    ).toBe('true');
  });

  it('renders the paragraph + heading tiles with Lucide-style inline SVG icons', () => {
    const registry = buildCoreRegistry();
    render(<BlockInserter registry={registry} onInsert={vi.fn()} />);

    const paragraphIcon = screen.getByTestId(
      'block-inserter-tile-icon-core/paragraph',
    );
    const headingIcon = screen.getByTestId(
      'block-inserter-tile-icon-core/heading',
    );

    // Both icons render as proper SVG (Lucide-glyph) — not the legacy
    // serif "P" / "H" text used in the pre-brand placeholder.
    expect(paragraphIcon.querySelector('svg')).not.toBeNull();
    expect(headingIcon.querySelector('svg')).not.toBeNull();
    expect(paragraphIcon.querySelector('text')).toBeNull();
    expect(headingIcon.querySelector('text')).toBeNull();
    // Lucide icons use stroke; the legacy glyph used text fill.
    expect(
      paragraphIcon.querySelector('svg')?.getAttribute('stroke'),
    ).toBe('currentColor');
  });
});
