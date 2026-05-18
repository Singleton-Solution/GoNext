/**
 * `core/button` tests — round-trip, schema validation, and save snapshot.
 */
import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import { button, ButtonEdit } from './index.ts';

describe('core/button', () => {
  it('round-trips parse → save without mutating canonical attributes', () => {
    const attrs = { text: 'Click', url: 'https://x' };
    const html = button.save({ attributes: attrs });
    expect(button.save({ attributes: attrs })).toBe(html);
    expect(attrs).toStrictEqual({ text: 'Click', url: 'https://x' });
  });

  it('validates a well-formed button', () => {
    const r = new BlockRegistry();
    r.register(button.definition);
    expect(
      r.validate([
        {
          type: 'core/button',
          attributes: {
            text: 'Click',
            url: 'https://x',
            linkTarget: '_blank',
            style: 'outline',
            borderRadius: 8,
            align: 'center',
          },
        },
      ]).valid,
    ).toBe(true);
  });

  it('rejects out-of-bound borderRadius', () => {
    const r = new BlockRegistry();
    r.register(button.definition);
    expect(
      r.validate([
        {
          type: 'core/button',
          attributes: { text: 'x', borderRadius: -1 },
        },
      ]).valid,
    ).toBe(false);
    expect(
      r.validate([
        {
          type: 'core/button',
          attributes: { text: 'x', borderRadius: 1000 },
        },
      ]).valid,
    ).toBe(false);
  });

  it('rejects unknown style enum', () => {
    const r = new BlockRegistry();
    r.register(button.definition);
    expect(
      r.validate([
        {
          type: 'core/button',
          attributes: { text: 'x', style: 'ghost' },
        },
      ]).valid,
    ).toBe(false);
  });

  it('snapshot: bare button', () => {
    expect(button.save({ attributes: { text: 'Click' } })).toMatchSnapshot();
  });

  it('snapshot: button with url + target + style + radius + align', () => {
    expect(
      button.save({
        attributes: {
          text: 'Read more',
          url: 'https://x',
          linkTarget: '_blank',
          style: 'fill',
          borderRadius: 12,
          align: 'center',
        },
      }),
    ).toMatchSnapshot();
  });

  it('adds rel=noopener noreferrer when target=_blank', () => {
    const html = button.save({
      attributes: { text: 't', url: 'https://x', linkTarget: '_blank' },
    });
    expect(html).toContain('target="_blank"');
    expect(html).toContain('rel="noopener noreferrer"');
  });

  it('server-render parity: matches save() for the same input', () => {
    const attrs = { text: 'x' };
    expect(button.serverRender(attrs, '')).toBe(
      button.save({ attributes: attrs }),
    );
  });

  it('Edit component renders an empty-state placeholder', () => {
    const { container } = render(
      <ButtonEdit
        attributes={{ text: '' }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="b-1"
        context={{}}
      />,
    );
    expect(container.querySelector('a')?.textContent).toBe('Add text…');
  });

  it('Edit component wires url + target through to the anchor', () => {
    const { container } = render(
      <ButtonEdit
        attributes={{ text: 'Click', url: 'https://x', linkTarget: '_blank' }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="b-2"
        context={{}}
      />,
    );
    const a = container.querySelector('a');
    expect(a?.getAttribute('href')).toBe('https://x');
    expect(a?.getAttribute('target')).toBe('_blank');
    expect(a?.getAttribute('rel')).toBe('noopener noreferrer');
  });
});
