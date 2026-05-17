/**
 * Tests for the inline-run serializer.
 *
 * Pin three properties:
 *  1. Plain text round-trips byte-for-byte through `serializeInline`.
 *  2. Each mark (bold, italic, code) emits the correct tag wrapper.
 *  3. Empty / single-run / multi-run sequences produce the expected
 *     canonical bytes — including the adjacent-text-run collapse rule.
 *
 * The deserializer is exercised separately in `deserialize.test.ts`;
 * round-trip stability is asserted in `roundtrip.test.ts`.
 */
import { describe, it, expect } from 'vitest';
import { serializeInline, flattenInlineToText } from './serialize.ts';
import { text, type InlineRun } from './inline.ts';

describe('serializeInline', () => {
  it('returns empty string for an empty run array', () => {
    expect(serializeInline([])).toBe('');
  });

  it('emits plain text without any wrapper tags', () => {
    expect(serializeInline([text('Hello, world.')])).toBe('Hello, world.');
  });

  it('escapes HTML special characters in text runs', () => {
    expect(serializeInline([text('<script>')])).toBe('&lt;script&gt;');
    expect(serializeInline([text('A & B')])).toBe('A &amp; B');
  });

  it('wraps a bold run in <strong>', () => {
    expect(serializeInline([text('bold', { bold: true })])).toBe(
      '<strong>bold</strong>',
    );
  });

  it('wraps an italic run in <em>', () => {
    expect(serializeInline([text('italic', { italic: true })])).toBe(
      '<em>italic</em>',
    );
  });

  it('wraps an inline-code run in <code>', () => {
    expect(serializeInline([text('code', { code: true })])).toBe(
      '<code>code</code>',
    );
  });

  it('nests marks in a fixed outermost-to-innermost order: strong → em → code', () => {
    expect(
      serializeInline([
        text('all-three', { bold: true, italic: true, code: true }),
      ]),
    ).toBe('<strong><em><code>all-three</code></em></strong>');
  });

  it('collapses adjacent text runs that share identical marks', () => {
    expect(
      serializeInline([
        text('Hel'),
        text('lo, '),
        text('world.'),
      ]),
    ).toBe('Hello, world.');
  });

  it('does not collapse text runs across a mark boundary', () => {
    expect(
      serializeInline([
        text('plain '),
        text('bold', { bold: true }),
        text(' plain'),
      ]),
    ).toBe('plain <strong>bold</strong> plain');
  });

  it('emits <br> for linebreak runs', () => {
    const runs: InlineRun[] = [
      text('line 1'),
      { type: 'linebreak' },
      text('line 2'),
    ];
    expect(serializeInline(runs)).toBe('line 1<br>line 2');
  });

  it('emits an anchor for link runs with href + rel + target', () => {
    const runs: InlineRun[] = [
      {
        type: 'link',
        href: 'https://example.com',
        rel: 'noopener noreferrer',
        target: '_blank',
        children: [text('click here')],
      },
    ];
    expect(serializeInline(runs)).toBe(
      '<a href="https://example.com" rel="noopener noreferrer" target="_blank">click here</a>',
    );
  });

  it('drops empty rel / target attributes on link runs', () => {
    const runs: InlineRun[] = [
      { type: 'link', href: '/about', children: [text('About')] },
    ];
    expect(serializeInline(runs)).toBe('<a href="/about">About</a>');
  });

  it('HTML-escapes the href value', () => {
    const runs: InlineRun[] = [
      {
        type: 'link',
        href: '/search?q="x"&y=<z>',
        children: [text('search')],
      },
    ];
    expect(serializeInline(runs)).toBe(
      '<a href="/search?q=&quot;x&quot;&amp;y=&lt;z&gt;">search</a>',
    );
  });

  it('allows marks inside a link', () => {
    const runs: InlineRun[] = [
      {
        type: 'link',
        href: '/x',
        children: [text('bold link', { bold: true })],
      },
    ];
    expect(serializeInline(runs)).toBe(
      '<a href="/x"><strong>bold link</strong></a>',
    );
  });
});

describe('flattenInlineToText', () => {
  it('drops marks but keeps text', () => {
    expect(
      flattenInlineToText([
        text('plain '),
        text('bold', { bold: true }),
        text(' end'),
      ]),
    ).toBe('plain bold end');
  });

  it('flattens link children inline', () => {
    expect(
      flattenInlineToText([
        text('see '),
        {
          type: 'link',
          href: '/x',
          children: [text('here')],
        },
      ]),
    ).toBe('see here');
  });

  it('emits newline for linebreak runs', () => {
    expect(
      flattenInlineToText([text('a'), { type: 'linebreak' }, text('b')]),
    ).toBe('a\nb');
  });
});
