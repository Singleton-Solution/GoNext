/**
 * `core/embed` tests — round-trip, schema validation, save snapshot, and
 * provider detection.
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import { embed, EmbedEdit, detectProvider } from './index.ts';

describe('core/embed', () => {
  it('round-trips parse → save without mutating canonical attributes', () => {
    const attrs = { url: 'https://youtu.be/abc' };
    const html = embed.save({ attributes: attrs });
    expect(embed.save({ attributes: attrs })).toBe(html);
    expect(attrs).toStrictEqual({ url: 'https://youtu.be/abc' });
  });

  it('validates a well-formed embed', () => {
    const r = new BlockRegistry();
    r.register(embed.definition);
    expect(
      r.validate([
        {
          type: 'core/embed',
          attributes: {
            url: 'https://youtu.be/abc',
            providerNameSlug: 'youtube',
            responsive: true,
            aspectRatio: '16-9',
          },
        },
      ]).valid,
    ).toBe(true);
  });

  it('rejects empty url', () => {
    const r = new BlockRegistry();
    r.register(embed.definition);
    expect(
      r.validate([{ type: 'core/embed', attributes: { url: '' } }]).valid,
    ).toBe(false);
  });

  it('snapshot: bare embed with no detected provider', () => {
    expect(
      embed.save({ attributes: { url: 'https://example.com/page' } }),
    ).toMatchSnapshot();
  });

  it('snapshot: YouTube embed with full options', () => {
    expect(
      embed.save({
        attributes: {
          url: 'https://www.youtube.com/watch?v=dQw4w9WgXcQ',
          providerNameSlug: 'youtube',
          responsive: true,
          aspectRatio: '16-9',
        },
      }),
    ).toMatchSnapshot();
  });

  it('detectProvider recognises YouTube, Vimeo, X/Twitter and Spotify', () => {
    expect(detectProvider('https://youtu.be/abc')?.slug).toBe('youtube');
    expect(detectProvider('https://www.youtube.com/watch?v=x')?.slug).toBe(
      'youtube',
    );
    expect(detectProvider('https://vimeo.com/12345')?.slug).toBe('vimeo');
    expect(
      detectProvider('https://x.com/jack/status/12345')?.slug,
    ).toBe('twitter');
    expect(
      detectProvider('https://twitter.com/jack/status/12345')?.slug,
    ).toBe('twitter');
    expect(
      detectProvider('https://open.spotify.com/track/x')?.slug,
    ).toBe('spotify');
    expect(detectProvider('https://example.com/page')).toBeNull();
  });

  it('emits the provider modifier class when slug is present', () => {
    const html = embed.save({
      attributes: { url: 'https://x', providerNameSlug: 'youtube' },
    });
    expect(html).toContain('is-provider-youtube');
    expect(html).toContain('wp-block-embed-youtube');
  });

  it('server-render uses oEmbed innerHtml when provided', () => {
    const html = embed.serverRender(
      { url: 'https://youtu.be/abc', providerNameSlug: 'youtube' },
      '<iframe src="https://youtube.com/embed/abc"></iframe>',
    );
    expect(html).toContain(
      '<iframe src="https://youtube.com/embed/abc"></iframe>',
    );
    expect(html).toContain('wp-block-embed__wrapper');
  });

  it('server-render falls back to save() output when innerHtml is empty', () => {
    const attrs = { url: 'https://youtu.be/abc', providerNameSlug: 'youtube' };
    expect(embed.serverRender(attrs, '')).toBe(
      embed.save({ attributes: attrs }),
    );
  });

  it('Edit component renders a placeholder when url is empty', () => {
    render(
      <EmbedEdit
        attributes={{ url: '' }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="e-1"
        context={{}}
      />,
    );
    expect(screen.getByText('Paste a link to embed')).toBeInTheDocument();
  });

  it('Edit component renders the wrapper + provider class', () => {
    const { container } = render(
      <EmbedEdit
        attributes={{ url: 'https://youtu.be/abc' }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="e-2"
        context={{}}
      />,
    );
    const fig = container.querySelector('figure[data-block="core/embed"]');
    expect(fig?.className).toContain('is-provider-youtube');
    expect(container.querySelector('.wp-block-embed__wrapper a')?.getAttribute('href')).toBe(
      'https://youtu.be/abc',
    );
  });
});
