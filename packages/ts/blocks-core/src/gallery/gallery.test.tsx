/**
 * `core/gallery` tests — round-trip, schema validation, and save snapshot.
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import { gallery, GalleryEdit } from './index.ts';
import { assertNoAxeViolations } from '../internal/axe.ts';

describe('core/gallery', () => {
  it('round-trips parse → save without mutating canonical attributes', () => {
    const attrs = {
      images: [
        { url: 'https://x/a.png', alt: 'A' },
        { url: 'https://x/b.png', alt: 'B' },
      ],
      columns: 2,
    };
    const html = gallery.save({ attributes: attrs });
    expect(gallery.save({ attributes: attrs })).toBe(html);
    expect(attrs.images).toHaveLength(2);
    expect(attrs.columns).toBe(2);
  });

  it('validates a well-formed gallery', () => {
    const r = new BlockRegistry();
    r.register(gallery.definition);
    expect(
      r.validate([
        {
          type: 'core/gallery',
          attributes: {
            images: [{ url: 'https://x/a.png', alt: 'A' }],
            columns: 3,
            imageCrop: true,
          },
        },
      ]).valid,
    ).toBe(true);
  });

  it('rejects column count out of bounds', () => {
    const r = new BlockRegistry();
    r.register(gallery.definition);
    expect(
      r.validate([
        {
          type: 'core/gallery',
          attributes: { images: [], columns: 0 },
        },
      ]).valid,
    ).toBe(false);
    expect(
      r.validate([
        {
          type: 'core/gallery',
          attributes: { images: [], columns: 9 },
        },
      ]).valid,
    ).toBe(false);
  });

  it('rejects images without a url', () => {
    const r = new BlockRegistry();
    r.register(gallery.definition);
    expect(
      r.validate([
        {
          type: 'core/gallery',
          attributes: { images: [{ url: '', alt: 'A' }] },
        },
      ]).valid,
    ).toBe(false);
  });

  it('snapshot: empty gallery', () => {
    expect(gallery.save({ attributes: { images: [] } })).toMatchSnapshot();
  });

  it('snapshot: 3-image cropped gallery with captions', () => {
    expect(
      gallery.save({
        attributes: {
          images: [
            {
              url: 'https://x/a.png',
              alt: 'A',
              caption: 'first',
              width: 800,
              height: 600,
            },
            { url: 'https://x/b.png', alt: 'B' },
            { url: 'https://x/c.png', alt: 'C', caption: 'last' },
          ],
          columns: 3,
          imageCrop: true,
        },
      }),
    ).toMatchSnapshot();
  });

  it('server-render parity: matches save() for the same input', () => {
    const attrs = { images: [{ url: 'x', alt: 'a' }] };
    expect(gallery.serverRender(attrs, '')).toBe(
      gallery.save({ attributes: attrs }),
    );
  });

  it('Edit component renders a placeholder when no images', () => {
    render(
      <GalleryEdit
        attributes={{ images: [] }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="g-1"
        context={{}}
      />,
    );
    expect(screen.getByText('Add images to build a gallery')).toBeInTheDocument();
  });

  it('Edit component wires reorder controls when selected', () => {
    const { container } = render(
      <GalleryEdit
        attributes={{
          images: [
            { url: 'https://x/a.png', alt: 'A' },
            { url: 'https://x/b.png', alt: 'B' },
          ],
        }}
        setAttributes={() => undefined}
        isSelected
        clientId="g-2"
        context={{}}
      />,
    );
    const controls = container.querySelectorAll(
      '.gn-block-gallery__controls button',
    );
    expect(controls.length).toBe(4);
  });

  // Issue #250 — WCAG 2.1 AA: every interactive surface must score clean.
  it('Edit component has no axe a11y violations', async () => {
    const { container } = render(
      <GalleryEdit
        attributes={{
          images: [
            { url: 'https://example.com/a.png', alt: 'First image' },
            { url: 'https://example.com/b.png', alt: 'Second image' },
          ],
          columns: 2,
        }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="g-axe"
        context={{}}
      />,
    );
    await assertNoAxeViolations(container);
  });
});
