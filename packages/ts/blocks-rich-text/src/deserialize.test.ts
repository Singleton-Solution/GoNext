/**
 * Tests for the inline-run deserializer.
 *
 * The deserializer is the inverse of `serializeInline`, so each test
 * pairs the expected HTML input with the run array it should produce.
 * We also pin a handful of robustness properties: tolerated unknown
 * tags, mismatched closes, and entity decoding.
 */
import { describe, it, expect } from 'vitest';
import { deserializeInline, stringToInline } from './deserialize.ts';

describe('deserializeInline', () => {
  it('returns empty array for empty input', () => {
    expect(deserializeInline('')).toEqual([]);
  });

  it('parses a plain text fragment as a single text run', () => {
    expect(deserializeInline('Hello')).toEqual([
      { type: 'text', text: 'Hello' },
    ]);
  });

  it('decodes core HTML entities', () => {
    expect(deserializeInline('A &amp; B')).toEqual([
      { type: 'text', text: 'A & B' },
    ]);
    expect(deserializeInline('&lt;script&gt;')).toEqual([
      { type: 'text', text: '<script>' },
    ]);
  });

  it('parses <strong> as a bold run', () => {
    expect(deserializeInline('<strong>x</strong>')).toEqual([
      { type: 'text', text: 'x', marks: { bold: true } },
    ]);
  });

  it('parses <em> as an italic run', () => {
    expect(deserializeInline('<em>x</em>')).toEqual([
      { type: 'text', text: 'x', marks: { italic: true } },
    ]);
  });

  it('parses <code> as a code run', () => {
    expect(deserializeInline('<code>x</code>')).toEqual([
      { type: 'text', text: 'x', marks: { code: true } },
    ]);
  });

  it('parses nested mark tags as a single multi-marked run', () => {
    expect(
      deserializeInline('<strong><em>x</em></strong>'),
    ).toEqual([{ type: 'text', text: 'x', marks: { bold: true, italic: true } }]);
  });

  it('parses <b> / <i> as aliases for bold / italic', () => {
    expect(deserializeInline('<b>x</b>')).toEqual([
      { type: 'text', text: 'x', marks: { bold: true } },
    ]);
    expect(deserializeInline('<i>x</i>')).toEqual([
      { type: 'text', text: 'x', marks: { italic: true } },
    ]);
  });

  it('parses <br> and <br/> as linebreak runs', () => {
    expect(deserializeInline('a<br>b')).toEqual([
      { type: 'text', text: 'a' },
      { type: 'linebreak' },
      { type: 'text', text: 'b' },
    ]);
    expect(deserializeInline('a<br/>b')).toEqual([
      { type: 'text', text: 'a' },
      { type: 'linebreak' },
      { type: 'text', text: 'b' },
    ]);
  });

  it('parses <a href> with text children', () => {
    expect(
      deserializeInline('<a href="/about">About</a>'),
    ).toEqual([
      {
        type: 'link',
        href: '/about',
        children: [{ type: 'text', text: 'About' }],
      },
    ]);
  });

  it('parses <a href + rel + target>', () => {
    expect(
      deserializeInline(
        '<a href="https://x" rel="noopener" target="_blank">x</a>',
      ),
    ).toEqual([
      {
        type: 'link',
        href: 'https://x',
        rel: 'noopener',
        target: '_blank',
        children: [{ type: 'text', text: 'x' }],
      },
    ]);
  });

  it('parses marks inside a link', () => {
    expect(
      deserializeInline('<a href="/x"><strong>bold link</strong></a>'),
    ).toEqual([
      {
        type: 'link',
        href: '/x',
        children: [
          { type: 'text', text: 'bold link', marks: { bold: true } },
        ],
      },
    ]);
  });

  it('drops unknown tag wrappers but keeps their text content', () => {
    expect(deserializeInline('<span class="x">inner</span>')).toEqual([
      { type: 'text', text: 'inner' },
    ]);
  });

  it('tolerates unmatched closing tags', () => {
    expect(deserializeInline('hello</em>')).toEqual([
      { type: 'text', text: 'hello' },
    ]);
  });

  it('tolerates unclosed mark tags', () => {
    // The text continues to apply the open mark until end of input.
    expect(deserializeInline('<strong>hello')).toEqual([
      { type: 'text', text: 'hello', marks: { bold: true } },
    ]);
  });

  it('finishes any open <a> at end of input', () => {
    expect(deserializeInline('<a href="/x">unclosed')).toEqual([
      {
        type: 'link',
        href: '/x',
        children: [{ type: 'text', text: 'unclosed' }],
      },
    ]);
  });

  it('decodes entities inside attribute values', () => {
    expect(
      deserializeInline('<a href="/q?a=&amp;b">x</a>'),
    ).toEqual([
      {
        type: 'link',
        href: '/q?a=&b',
        children: [{ type: 'text', text: 'x' }],
      },
    ]);
  });

  it('strips HTML comments', () => {
    expect(deserializeInline('a<!--comment-->b')).toEqual([
      { type: 'text', text: 'a' },
      { type: 'text', text: 'b' },
    ]);
  });
});

describe('stringToInline', () => {
  it('lifts a non-empty string into a single text run', () => {
    expect(stringToInline('hello')).toEqual([{ type: 'text', text: 'hello' }]);
  });

  it('returns an empty array for the empty string', () => {
    expect(stringToInline('')).toEqual([]);
  });
});
