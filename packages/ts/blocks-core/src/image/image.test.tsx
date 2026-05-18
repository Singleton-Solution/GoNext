/**
 * `core/image` tests — round-trip, schema validation, and save snapshot.
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import { image, ImageEdit } from './index.ts';
import { assertNoAxeViolations } from '../internal/axe.ts';

describe('core/image', () => {
  it('round-trips parse → save without mutating canonical attributes', () => {
    const attrs = { url: 'https://example.com/a.png', alt: 'A' };
    const html = image.save({ attributes: attrs });
    expect(image.save({ attributes: attrs })).toBe(html);
    expect(attrs).toStrictEqual({ url: 'https://example.com/a.png', alt: 'A' });
  });

  it('validates a well-formed image', () => {
    const r = new BlockRegistry();
    r.register(image.definition);
    expect(
      r.validate([
        {
          type: 'core/image',
          attributes: {
            url: 'https://example.com/a.png',
            alt: 'A',
            width: 100,
            height: 80,
          },
        },
      ]).valid,
    ).toBe(true);
  });

  it('rejects empty `url`', () => {
    const r = new BlockRegistry();
    r.register(image.definition);
    expect(
      r.validate([
        { type: 'core/image', attributes: { url: '', alt: 'A' } },
      ]).valid,
    ).toBe(false);
  });

  it('rejects unknown `align` enum', () => {
    const r = new BlockRegistry();
    r.register(image.definition);
    expect(
      r.validate([
        {
          type: 'core/image',
          attributes: { url: 'x', alt: 'a', align: 'diagonal' },
        },
      ]).valid,
    ).toBe(false);
  });

  it('snapshot: bare image output', () => {
    expect(
      image.save({
        attributes: { url: 'https://example.com/a.png', alt: 'A' },
      }),
    ).toMatchSnapshot();
  });

  it('snapshot: full image output with caption + href + align', () => {
    expect(
      image.save({
        attributes: {
          url: 'https://example.com/a.png',
          alt: 'A photo',
          caption: 'A caption',
          width: 800,
          height: 600,
          align: 'wide',
          href: 'https://example.com/full.png',
        },
      }),
    ).toMatchSnapshot();
  });

  it('escapes attribute payloads (the URL is treated as untrusted text)', () => {
    expect(
      image.save({
        attributes: { url: '"><script>', alt: '"x"' },
      }),
    ).toBe(
      '<figure class="gn-block-image"><img src="&quot;&gt;&lt;script&gt;" alt="&quot;x&quot;"/></figure>',
    );
  });

  it('server-render parity: matches save() for the same input', () => {
    const attrs = { url: 'x', alt: 'a' };
    expect(image.serverRender(attrs, '')).toBe(image.save({ attributes: attrs }));
  });

  it('Edit component renders the placeholder when url is empty', () => {
    render(
      <ImageEdit
        attributes={{ url: '', alt: '' }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="img-1"
        context={{}}
      />,
    );
    expect(screen.getByText('Pick or upload an image')).toBeInTheDocument();
  });

  // Issue #250 — WCAG 2.1 AA: every interactive surface must score clean.
  it('Edit component has no axe a11y violations (with alt)', async () => {
    const { container } = render(
      <ImageEdit
        attributes={{
          url: 'https://example.com/a.png',
          alt: 'A descriptive alt text',
          width: 800,
          height: 600,
        }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="img-axe"
        context={{}}
      />,
    );
    await assertNoAxeViolations(container);
  });
});
