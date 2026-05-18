/**
 * `core/video` tests — round-trip, schema validation, and save snapshot.
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import { video, VideoEdit } from './index.ts';
import { assertNoAxeViolations } from '../internal/axe.ts';

describe('core/video', () => {
  it('round-trips parse → save without mutating canonical attributes', () => {
    const attrs = { src: 'https://x/v.mp4' };
    const html = video.save({ attributes: attrs });
    expect(video.save({ attributes: attrs })).toBe(html);
    expect(attrs).toStrictEqual({ src: 'https://x/v.mp4' });
  });

  it('validates a well-formed video', () => {
    const r = new BlockRegistry();
    r.register(video.definition);
    expect(
      r.validate([
        {
          type: 'core/video',
          attributes: {
            src: 'https://x/v.mp4',
            poster: 'https://x/p.jpg',
            controls: true,
            autoplay: false,
            loop: true,
            muted: true,
            caption: 'C',
          },
        },
      ]).valid,
    ).toBe(true);
  });

  it('rejects empty `src`', () => {
    const r = new BlockRegistry();
    r.register(video.definition);
    expect(
      r.validate([{ type: 'core/video', attributes: { src: '' } }]).valid,
    ).toBe(false);
  });

  it('snapshot: bare video output', () => {
    expect(
      video.save({ attributes: { src: 'https://x/v.mp4' } }),
    ).toMatchSnapshot();
  });

  it('snapshot: video with poster, caption and all flags', () => {
    expect(
      video.save({
        attributes: {
          src: 'https://x/v.mp4',
          poster: 'https://x/p.jpg',
          controls: true,
          autoplay: true,
          loop: true,
          muted: true,
          caption: 'A clip',
        },
      }),
    ).toMatchSnapshot();
  });

  it('drops `controls` when explicitly false', () => {
    const html = video.save({
      attributes: { src: 'https://x/v.mp4', controls: false },
    });
    expect(html).not.toContain(' controls');
  });

  it('server-render parity: matches save() for the same input', () => {
    const attrs = { src: 'https://x/v.mp4' };
    expect(video.serverRender(attrs, '')).toBe(
      video.save({ attributes: attrs }),
    );
  });

  it('Edit component renders the placeholder when src is empty', () => {
    render(
      <VideoEdit
        attributes={{ src: '' }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="v-1"
        context={{}}
      />,
    );
    expect(screen.getByText('Pick or upload a video')).toBeInTheDocument();
  });

  it('Edit component wires the src through to the <video> element', () => {
    const { container } = render(
      <VideoEdit
        attributes={{ src: 'https://x/v.mp4', caption: 'cap' }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="v-2"
        context={{}}
      />,
    );
    const v = container.querySelector('video');
    expect(v?.getAttribute('src')).toBe('https://x/v.mp4');
    expect(container.querySelector('figcaption')?.textContent).toBe('cap');
  });

  // Issue #250 — WCAG 2.1 AA: every interactive surface must score clean.
  it('Edit component has no axe a11y violations', async () => {
    const { container } = render(
      <VideoEdit
        attributes={{
          src: 'https://example.com/v.mp4',
          controls: true,
          caption: 'A short clip',
        }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="v-axe"
        context={{}}
      />,
    );
    await assertNoAxeViolations(container);
  });
});
