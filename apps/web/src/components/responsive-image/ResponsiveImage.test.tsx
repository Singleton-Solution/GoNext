/**
 * Unit tests for ResponsiveImage. Run under vitest + happy-dom.
 *
 * The contract we care about: given the inputs an admin/migrator would
 * pass, the emitted DOM is a `<picture>` with the right source order,
 * fallback `<img>`, and the lazy/async loading attributes.
 */
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ResponsiveImage, buildWidthSrcSet } from './ResponsiveImage';

describe('buildWidthSrcSet', () => {
  it('returns an empty string for no widths', () => {
    expect(buildWidthSrcSet('/img.jpg', [])).toBe('');
  });

  it('appends ?w=N for plain URLs', () => {
    expect(buildWidthSrcSet('/img.jpg', [256, 1024])).toBe(
      '/img.jpg?w=256 256w, /img.jpg?w=1024 1024w',
    );
  });

  it('uses & when the URL already has a query string', () => {
    expect(buildWidthSrcSet('/img.jpg?v=1', [256])).toBe('/img.jpg?v=1&w=256 256w');
  });

  it('sorts widths narrowest-first', () => {
    expect(buildWidthSrcSet('/x.jpg', [1024, 256, 768])).toBe(
      '/x.jpg?w=256 256w, /x.jpg?w=768 768w, /x.jpg?w=1024 1024w',
    );
  });
});

describe('<ResponsiveImage>', () => {
  it('renders a fallback <img> with the canonical src', () => {
    render(<ResponsiveImage src="/img.jpg" alt="hero" />);
    const img = screen.getByAltText('hero') as HTMLImageElement;
    expect(img.tagName).toBe('IMG');
    expect(img.getAttribute('src')).toBe('/img.jpg');
  });

  it('defaults to lazy + async', () => {
    render(<ResponsiveImage src="/img.jpg" alt="hero" />);
    const img = screen.getByAltText('hero');
    expect(img.getAttribute('loading')).toBe('lazy');
    expect(img.getAttribute('decoding')).toBe('async');
  });

  it('priority flips loading/decoding to eager/sync', () => {
    render(<ResponsiveImage src="/img.jpg" alt="hero" priority />);
    const img = screen.getByAltText('hero');
    expect(img.getAttribute('loading')).toBe('eager');
    expect(img.getAttribute('decoding')).toBe('sync');
  });

  it('renders a <source> per format when sources are provided', () => {
    const { container } = render(
      <ResponsiveImage
        src="/img.jpg"
        alt="hero"
        sources={[
          { srcset: '/img.avif?w=480 480w, /img.avif?w=1024 1024w', type: 'image/avif' },
          { srcset: '/img.webp?w=480 480w', type: 'image/webp' },
        ]}
      />,
    );
    const sources = container.querySelectorAll('source');
    expect(sources).toHaveLength(2);
    expect(sources[0]?.getAttribute('type')).toBe('image/avif');
    expect(sources[1]?.getAttribute('type')).toBe('image/webp');
  });

  it('synthesises a width-based srcset when only widths are provided', () => {
    const { container } = render(
      <ResponsiveImage src="/img.jpg" alt="hero" widths={[256, 1024]} />,
    );
    const source = container.querySelector('source');
    expect(source).not.toBeNull();
    expect(source?.getAttribute('srcset')).toBe('/img.jpg?w=256 256w, /img.jpg?w=1024 1024w');
  });

  it('passes through width/height for aspect-ratio preservation', () => {
    render(<ResponsiveImage src="/img.jpg" alt="hero" width={1200} height={630} />);
    const img = screen.getByAltText('hero');
    expect(img.getAttribute('width')).toBe('1200');
    expect(img.getAttribute('height')).toBe('630');
  });

  it('uses the default sizes attribute on synthesised sources', () => {
    const { container } = render(
      <ResponsiveImage src="/img.jpg" alt="hero" widths={[256]} />,
    );
    const source = container.querySelector('source');
    expect(source?.getAttribute('sizes')).toContain('max-width: 480px');
  });

  it('honours an explicit sizes attribute', () => {
    const { container } = render(
      <ResponsiveImage src="/img.jpg" alt="hero" widths={[256]} sizes="100vw" />,
    );
    const source = container.querySelector('source');
    expect(source?.getAttribute('sizes')).toBe('100vw');
  });
});
