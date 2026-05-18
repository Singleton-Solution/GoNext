/**
 * `core/file` tests — round-trip, schema validation, and save snapshot.
 */
import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import { file, FileEdit } from './index.ts';
import { assertNoAxeViolations } from '../internal/axe.ts';

describe('core/file', () => {
  it('round-trips parse → save without mutating canonical attributes', () => {
    const attrs = { href: 'https://x/a.pdf', fileName: 'a.pdf' };
    const html = file.save({ attributes: attrs });
    expect(file.save({ attributes: attrs })).toBe(html);
    expect(attrs).toStrictEqual({ href: 'https://x/a.pdf', fileName: 'a.pdf' });
  });

  it('validates a well-formed file', () => {
    const r = new BlockRegistry();
    r.register(file.definition);
    expect(
      r.validate([
        {
          type: 'core/file',
          attributes: {
            href: 'https://x/a.pdf',
            fileName: 'a.pdf',
            downloadButton: true,
            textLinkHref: true,
          },
        },
      ]).valid,
    ).toBe(true);
  });

  it('rejects empty href or fileName', () => {
    const r = new BlockRegistry();
    r.register(file.definition);
    expect(
      r.validate([
        { type: 'core/file', attributes: { href: '', fileName: 'a.pdf' } },
      ]).valid,
    ).toBe(false);
    expect(
      r.validate([
        { type: 'core/file', attributes: { href: 'x', fileName: '' } },
      ]).valid,
    ).toBe(false);
  });

  it('snapshot: default file (both link + button)', () => {
    expect(
      file.save({
        attributes: { href: 'https://x/a.pdf', fileName: 'a.pdf' },
      }),
    ).toMatchSnapshot();
  });

  it('snapshot: file with download button disabled', () => {
    expect(
      file.save({
        attributes: {
          href: 'https://x/a.pdf',
          fileName: 'a.pdf',
          downloadButton: false,
        },
      }),
    ).toMatchSnapshot();
  });

  it('snapshot: file with text link disabled (span fallback)', () => {
    expect(
      file.save({
        attributes: {
          href: 'https://x/a.pdf',
          fileName: 'a.pdf',
          textLinkHref: false,
        },
      }),
    ).toMatchSnapshot();
  });

  it('escapes the file name', () => {
    expect(
      file.save({
        attributes: { href: 'https://x/a.pdf', fileName: '<bad>.pdf' },
      }),
    ).toContain('&lt;bad&gt;.pdf');
  });

  it('server-render parity: matches save() for the same input', () => {
    const attrs = { href: 'x', fileName: 'a' };
    expect(file.serverRender(attrs, '')).toBe(
      file.save({ attributes: attrs }),
    );
  });

  it('Edit component renders both link and download button by default', () => {
    const { container } = render(
      <FileEdit
        attributes={{ href: 'https://x/a.pdf', fileName: 'a.pdf' }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="f-1"
        context={{}}
      />,
    );
    const links = container.querySelectorAll('a');
    expect(links.length).toBe(2);
    expect(links[1]?.className).toBe('wp-block-file__button');
  });

  // Issue #250 — WCAG 2.1 AA: every interactive surface must score clean.
  it('Edit component has no axe a11y violations', async () => {
    const { container } = render(
      <FileEdit
        attributes={{
          href: 'https://example.com/a.pdf',
          fileName: 'Annual report (PDF)',
        }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="f-axe"
        context={{}}
      />,
    );
    await assertNoAxeViolations(container);
  });
});
