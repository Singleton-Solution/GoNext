/**
 * `core/media-text` tests — round-trip, schema validation, snapshots,
 * RTL-aware positioning, and the inner-blocks sentinel contract.
 */
import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import {
  mediaText,
  MediaTextEdit,
  MEDIA_TEXT_INNER_SENTINEL,
  normalizeMediaWidth,
} from './index.ts';
import { assertNoAxeViolations } from '../internal/axe.ts';

describe('core/media-text', () => {
  it('save() emits the inner-blocks sentinel inside the content column', () => {
    const html = mediaText.save({
      attributes: { mediaUrl: 'https://x/a.png', mediaAlt: 'A' },
    });
    expect(html).toContain(MEDIA_TEXT_INNER_SENTINEL);
    expect(html).toMatch(
      /gn-block-media-text__content[^>]*>.*<!--gn-inner-blocks-->/,
    );
  });

  it('save() places the figure before content when mediaPosition is left (default)', () => {
    const html = mediaText.save({
      attributes: { mediaUrl: 'https://x/a.png', mediaAlt: 'A' },
    });
    const figureIdx = html.indexOf('gn-block-media-text__media');
    const contentIdx = html.indexOf('gn-block-media-text__content');
    expect(figureIdx).toBeGreaterThan(-1);
    expect(contentIdx).toBeGreaterThan(figureIdx);
    expect(html).toContain('is-media-on-the-left');
  });

  it('save() swaps the column order when mediaPosition is right', () => {
    const html = mediaText.save({
      attributes: {
        mediaUrl: 'https://x/a.png',
        mediaAlt: 'A',
        mediaPosition: 'right',
      },
    });
    const figureIdx = html.indexOf('gn-block-media-text__media');
    const contentIdx = html.indexOf('gn-block-media-text__content');
    expect(contentIdx).toBeGreaterThan(-1);
    expect(figureIdx).toBeGreaterThan(contentIdx);
    expect(html).toContain('is-media-on-the-right');
  });

  it('save() honours mediaWidth in the inline grid-template-columns', () => {
    const html = mediaText.save({
      attributes: {
        mediaUrl: 'https://x/a.png',
        mediaAlt: 'A',
        mediaWidth: 30,
      },
    });
    expect(html).toContain('grid-template-columns:30% 70%');
  });

  it('save() inverts the inline column ratio when media is on the right', () => {
    const html = mediaText.save({
      attributes: {
        mediaUrl: 'https://x/a.png',
        mediaAlt: 'A',
        mediaPosition: 'right',
        mediaWidth: 30,
      },
    });
    // Right-positioned media still occupies 30% — content takes the 70%
    // first cell so the visual split matches.
    expect(html).toContain('grid-template-columns:70% 30%');
  });

  it('save() escapes the media URL, alt, and caption text', () => {
    const html = mediaText.save({
      attributes: {
        mediaUrl: 'https://x/a.png?q=&z=<',
        mediaAlt: '"alt"',
        mediaCaption: 'a > b',
      },
    });
    expect(html).toContain('q=&amp;z=&lt;');
    expect(html).toContain('alt="&quot;alt&quot;"');
    expect(html).toContain('a &gt; b');
  });

  it('save() emits the imageFill class when set', () => {
    const html = mediaText.save({
      attributes: {
        mediaUrl: 'https://x/a.png',
        mediaAlt: 'A',
        imageFill: true,
      },
    });
    expect(html).toContain('has-media-on-the-fill');
  });

  it('save() emits a verticalAlignment class when set', () => {
    const html = mediaText.save({
      attributes: {
        mediaUrl: 'https://x/a.png',
        mediaAlt: 'A',
        verticalAlignment: 'center',
      },
    });
    expect(html).toContain('is-vertically-aligned-center');
  });

  it('serverRender() substitutes the inner HTML into the content slot', () => {
    const rendered = mediaText.serverRender(
      { mediaUrl: 'https://x/a.png', mediaAlt: 'A' },
      '<p>hi</p>',
    );
    expect(rendered).toContain('<p>hi</p>');
    expect(rendered).not.toContain(MEDIA_TEXT_INNER_SENTINEL);
  });

  it('normalizeMediaWidth clamps to the 10..90 range', () => {
    expect(normalizeMediaWidth(undefined)).toBe(50);
    expect(normalizeMediaWidth(0)).toBe(10);
    expect(normalizeMediaWidth(5)).toBe(10);
    expect(normalizeMediaWidth(50)).toBe(50);
    expect(normalizeMediaWidth(95)).toBe(90);
    expect(normalizeMediaWidth(Number.NaN)).toBe(50);
  });

  it('validates a well-formed media-text block', () => {
    const r = new BlockRegistry();
    r.register(mediaText.definition);
    expect(
      r.validate([
        {
          type: 'core/media-text',
          attributes: { mediaUrl: 'https://x/a.png', mediaAlt: 'A' },
        },
      ]).valid,
    ).toBe(true);
  });

  it('rejects mediaWidth values outside 10..90', () => {
    const r = new BlockRegistry();
    r.register(mediaText.definition);
    expect(
      r.validate([
        {
          type: 'core/media-text',
          attributes: { mediaUrl: 'https://x/a.png', mediaAlt: 'A', mediaWidth: 5 },
        },
      ]).valid,
    ).toBe(false);
    expect(
      r.validate([
        {
          type: 'core/media-text',
          attributes: { mediaUrl: 'https://x/a.png', mediaAlt: 'A', mediaWidth: 95 },
        },
      ]).valid,
    ).toBe(false);
  });

  it('rejects unknown mediaPosition values', () => {
    const r = new BlockRegistry();
    r.register(mediaText.definition);
    expect(
      r.validate([
        {
          type: 'core/media-text',
          attributes: {
            mediaUrl: 'https://x/a.png',
            mediaAlt: 'A',
            mediaPosition: 'top',
          },
        },
      ]).valid,
    ).toBe(false);
  });

  it('snapshot: default media-text', () => {
    expect(
      mediaText.save({
        attributes: { mediaUrl: 'https://x/a.png', mediaAlt: 'A' },
      }),
    ).toMatchSnapshot();
  });

  it('snapshot: media on the right with fill + center alignment', () => {
    expect(
      mediaText.save({
        attributes: {
          mediaUrl: 'https://x/a.png',
          mediaAlt: 'A',
          mediaPosition: 'right',
          imageFill: true,
          verticalAlignment: 'center',
          mediaWidth: 40,
        },
      }),
    ).toMatchSnapshot();
  });

  it('supports.innerBlocks is true so the editor accepts text-column children', () => {
    expect(mediaText.definition.supports?.innerBlocks).toBe(true);
  });

  it('Edit component renders the wrapper with the position class', () => {
    const { container } = render(
      <MediaTextEdit
        attributes={{
          mediaUrl: 'https://x/a.png',
          mediaAlt: 'A',
          mediaPosition: 'right',
        }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="mt-1"
        context={{}}
      />,
    );
    const root = container.querySelector('div[data-block="core/media-text"]');
    expect(root?.className).toContain('is-media-on-the-right');
  });

  it('Edit component renders an empty placeholder when no media URL is set', () => {
    const { container, getByText } = render(
      <MediaTextEdit
        attributes={{ mediaUrl: '', mediaAlt: '' }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="mt-empty"
        context={{}}
      />,
    );
    expect(getByText('Add media')).toBeTruthy();
    expect(container.querySelector('img')).toBeNull();
  });

  // Issue #250 — WCAG 2.1 AA: every interactive surface must score clean.
  it('Edit component has no axe a11y violations', async () => {
    const { container } = render(
      <MediaTextEdit
        attributes={{ mediaUrl: 'https://x/a.png', mediaAlt: 'Decorative' }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="mt-axe"
        context={{}}
      />,
    );
    await assertNoAxeViolations(container);
  });
});
