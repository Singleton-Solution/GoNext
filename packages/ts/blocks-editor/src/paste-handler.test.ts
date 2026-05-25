/**
 * Tests for the paste-handler.
 *
 * The handler is mostly source-detection + per-source HTML-to-blocks
 * conversion. We cover:
 *
 *   1. Source detection sniffs the clipboard fingerprint correctly for
 *      Google Docs, Word, Notion, Markdown, generic HTML, and plain
 *      text. Mis-detection here breaks every downstream path, so the
 *      sniffer gets the most coverage.
 *   2. Each per-source converter produces the expected block tree for
 *      its "happy path" snippet (a heading, a paragraph, a list).
 *   3. `onPaste()` reads `ClipboardData`, dispatches to the right
 *      converter, and returns `null` when there's nothing to insert
 *      (so the host can fall through to the browser's default).
 *
 * We deliberately don't snapshot — these trees are small and asserting
 * shape directly catches regressions faster than diffing snapshot
 * blobs.
 */
import { describe, expect, it } from 'vitest';
import {
  convertPaste,
  detectPasteSource,
  markdownToBlocks,
  onPaste,
  type DetectedPaste,
} from './paste-handler.ts';

describe('detectPasteSource', () => {
  it('detects Google Docs via the docs-internal-guid wrapper', () => {
    const html =
      '<meta charset="utf-8"><b id="docs-internal-guid-1234" style="font-weight:normal"><h1>Hi</h1></b>';
    expect(detectPasteSource({ html }).source).toBe('gdocs');
  });

  it('detects Microsoft Word via mso- prefixes and Generator meta', () => {
    const wordHtml = `<html xmlns:o="urn:schemas-microsoft-com:office:office">
      <head><meta name=Generator content="Microsoft Word 16"></head>
      <body><p class=MsoNormal>Hello<o:p></o:p></p></body></html>`;
    expect(detectPasteSource({ html: wordHtml }).source).toBe('word');
  });

  it('detects Notion via the notion- class prefix', () => {
    const html =
      '<div class="notion-selectable"><h2 class="notion-header-block">Hi</h2></div>';
    expect(detectPasteSource({ html }).source).toBe('notion');
  });

  it('detects Markdown when text/plain carries Markdown markers', () => {
    expect(
      detectPasteSource({ text: '# Heading\n\n- item\n- another' }).source,
    ).toBe('markdown');
    expect(detectPasteSource({ text: '```\ncode\n```' }).source).toBe(
      'markdown',
    );
  });

  it('falls back to "html" for generic markup with no fingerprint', () => {
    expect(detectPasteSource({ html: '<p>plain</p>' }).source).toBe('html');
  });

  it('falls back to "text" when there is no HTML and no Markdown signals', () => {
    expect(detectPasteSource({ text: 'just a sentence' }).source).toBe(
      'text',
    );
    // Empty payload — nothing on the clipboard.
    expect(detectPasteSource({}).source).toBe('text');
  });

  it('prefers Notion over Docs when both fingerprints are present', () => {
    // Notion-then-Docs is a real case: user paints Docs into Notion,
    // re-copies, gets a Notion wrapper around a Docs snippet.
    const html =
      '<div class="notion-selectable"><b id="docs-internal-guid-x">Doc text</b></div>';
    expect(detectPasteSource({ html }).source).toBe('notion');
  });
});

describe('Google Docs converter', () => {
  it('strips the docs-internal-guid wrapper and emits headings + paragraphs', () => {
    const html =
      '<meta charset="utf-8"><b id="docs-internal-guid-abc"><h1>Hello</h1><p>world.</p></b>';
    const detected: DetectedPaste = { source: 'gdocs', html, text: '' };
    const blocks = convertPaste(detected);
    expect(blocks).toHaveLength(2);
    expect(blocks[0]).toMatchObject({
      type: 'core/heading',
      attributes: { level: 1, text: 'Hello' },
    });
    expect(blocks[1]).toMatchObject({
      type: 'core/paragraph',
      attributes: { text: 'world.' },
    });
  });

  it('converts <ul> into a core/list with list-item children', () => {
    const html =
      '<b id="docs-internal-guid-1"><ul><li>One</li><li>Two</li></ul></b>';
    const blocks = convertPaste({ source: 'gdocs', html, text: '' });
    expect(blocks).toHaveLength(1);
    expect(blocks[0]).toMatchObject({
      type: 'core/list',
      attributes: { ordered: false },
    });
    expect(blocks[0]?.innerBlocks).toHaveLength(2);
    expect(blocks[0]?.innerBlocks?.[0]).toMatchObject({
      type: 'core/list-item',
      attributes: { text: 'One' },
    });
  });
});

describe('Microsoft Word converter', () => {
  it('strips MSO conditional comments and <o:p> filler', () => {
    const html = `<html xmlns:o="urn:schemas-microsoft-com:office:office">
      <body>
        <!--[if gte mso 9]><xml><o:DocumentProperties></o:DocumentProperties></xml><![endif]-->
        <h2>Section</h2>
        <p class="MsoNormal">Hello <o:p></o:p></p>
      </body></html>`;
    const blocks = convertPaste({ source: 'word', html, text: '' });
    expect(blocks).toEqual([
      { type: 'core/heading', attributes: { level: 2, text: 'Section' } },
      { type: 'core/paragraph', attributes: { text: 'Hello' } },
    ]);
  });

  it('preserves ordered lists', () => {
    const html =
      '<html><body><ol><li>Alpha</li><li>Beta</li></ol></body></html>';
    const blocks = convertPaste({ source: 'word', html, text: '' });
    expect(blocks[0]).toMatchObject({
      type: 'core/list',
      attributes: { ordered: true },
    });
    expect(blocks[0]?.innerBlocks).toHaveLength(2);
  });
});

describe('Notion converter', () => {
  it('strips notion-selectable wrapper and converts notion-header blocks', () => {
    const html =
      '<div class="notion-selectable"><h2 class="notion-header-block">Title</h2><p class="notion-text-block">Body.</p></div>';
    const blocks = convertPaste({ source: 'notion', html, text: '' });
    expect(blocks).toHaveLength(2);
    expect(blocks[0]).toMatchObject({
      type: 'core/heading',
      attributes: { level: 2, text: 'Title' },
    });
    expect(blocks[1]).toMatchObject({
      type: 'core/paragraph',
      attributes: { text: 'Body.' },
    });
  });

  it('converts notion blockquotes', () => {
    const html =
      '<div class="notion-selectable"><blockquote class="notion-quote">Said someone.</blockquote></div>';
    const blocks = convertPaste({ source: 'notion', html, text: '' });
    expect(blocks).toEqual([
      { type: 'core/quote', attributes: { text: 'Said someone.' } },
    ]);
  });
});

describe('Markdown converter', () => {
  it('parses ATX headings, paragraphs, lists, and fenced code', () => {
    const md = [
      '# Hello',
      '',
      'A paragraph spanning',
      'two lines.',
      '',
      '- one',
      '- two',
      '',
      '1. first',
      '2. second',
      '',
      '```',
      "console.log('hi')",
      '```',
    ].join('\n');
    const blocks = markdownToBlocks(md);
    expect(blocks[0]).toMatchObject({
      type: 'core/heading',
      attributes: { level: 1, text: 'Hello' },
    });
    expect(blocks[1]).toMatchObject({
      type: 'core/paragraph',
      attributes: { text: 'A paragraph spanning two lines.' },
    });
    expect(blocks[2]).toMatchObject({
      type: 'core/list',
      attributes: { ordered: false },
    });
    expect(blocks[2]?.innerBlocks).toHaveLength(2);
    expect(blocks[3]).toMatchObject({
      type: 'core/list',
      attributes: { ordered: true },
    });
    expect(blocks[4]).toMatchObject({
      type: 'core/code',
      attributes: { code: "console.log('hi')" },
    });
  });

  it('handles a heading-only paste with no trailing newline', () => {
    expect(markdownToBlocks('### Just a heading')).toEqual([
      {
        type: 'core/heading',
        attributes: { level: 3, text: 'Just a heading' },
      },
    ]);
  });
});

describe('plain text fallback', () => {
  it('splits paragraphs on blank lines', () => {
    const blocks = convertPaste({
      source: 'text',
      html: '',
      text: 'first paragraph.\n\nsecond paragraph.',
    });
    expect(blocks).toEqual([
      { type: 'core/paragraph', attributes: { text: 'first paragraph.' } },
      { type: 'core/paragraph', attributes: { text: 'second paragraph.' } },
    ]);
  });
});

describe('generic HTML converter', () => {
  it('walks paragraphs, headings, hr, and code', () => {
    const html =
      '<h3>Title</h3><p>Hello.</p><hr><pre>code()</pre><img src="x.png" alt="X">';
    const blocks = convertPaste({ source: 'html', html, text: '' });
    expect(blocks).toMatchObject([
      { type: 'core/heading', attributes: { level: 3, text: 'Title' } },
      { type: 'core/paragraph', attributes: { text: 'Hello.' } },
      { type: 'core/separator', attributes: {} },
      { type: 'core/code', attributes: { code: 'code()' } },
      { type: 'core/image', attributes: { url: 'x.png', alt: 'X' } },
    ]);
  });
});

describe('onPaste', () => {
  /**
   * Build a minimal `ClipboardEvent` stand-in. jsdom 24 only ships a
   * partial `DataTransfer`, so we hand-roll the surface the handler
   * actually reads (`getData`).
   */
  function makeEvent(html: string, text: string): ClipboardEvent {
    const data = {
      getData: (type: string) => {
        if (type === 'text/html') return html;
        if (type === 'text/plain') return text;
        return '';
      },
    };
    return { clipboardData: data } as unknown as ClipboardEvent;
  }

  it('returns a block tree for a Google Docs paste', () => {
    const event = makeEvent(
      '<b id="docs-internal-guid-1"><h2>Title</h2></b>',
      'Title',
    );
    const blocks = onPaste(event);
    expect(blocks).not.toBeNull();
    expect(blocks?.[0]).toMatchObject({
      type: 'core/heading',
      attributes: { level: 2, text: 'Title' },
    });
  });

  it('returns null when there is nothing on the clipboard', () => {
    const event = makeEvent('', '');
    expect(onPaste(event)).toBeNull();
  });

  it('returns null when clipboardData is missing entirely', () => {
    const event = { clipboardData: null } as unknown as ClipboardEvent;
    expect(onPaste(event)).toBeNull();
  });
});
