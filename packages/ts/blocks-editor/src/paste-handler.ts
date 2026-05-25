/**
 * `paste-handler` — convert pasted clipboard content into GoNext blocks.
 *
 * Authors paste from many places. Google Docs and Microsoft Word stamp
 * their own bespoke HTML on the clipboard with telltale class prefixes
 * (`docs-*`, `ms-Office-*`, `OfficeStyle*`); Notion uses `notion-` data
 * attributes plus its own internal MIME type; everything else is either
 * plain Markdown text or "some random HTML". The job of this module is
 * to *sniff* the source, then funnel into a per-source converter that
 * walks the HTML and emits a `BlockTree`.
 *
 * Design rules we hold to (these match the contract the autosave
 * pipeline expects of "freshly inserted blocks"):
 *
 *  1. Every emitted block is shape-valid against the SDK type — `type`,
 *     `attributes`, optional `innerBlocks`. We never emit a `clientId`
 *     here; the host assigns one on insert so the autosave dirty bit
 *     stays correct (a paste that mints new clientIds would otherwise
 *     ping-pong against the last-saved snapshot).
 *  2. Unknown / undecorated HTML degrades to `core/paragraph` with the
 *     text content. Better to surface something than to silently drop a
 *     paste — authors notice the latter and don't notice the former.
 *  3. The handler is *pure* — no DOM mutation, no clipboard side effects.
 *     The exported `onPaste(event)` reads the `ClipboardEvent` and
 *     returns a `BlockTree`. Wiring that tree into the canvas is the
 *     caller's job (so the same handler works in both controlled and
 *     uncontrolled editor shells).
 *
 * The detection is intentionally string-sniffing rather than fancy DOM
 * inspection: clipboard HTML wrappers from Docs/Word/Notion all carry
 * stable class or attribute fingerprints. A regex check is O(n) on the
 * payload, runs once per paste, and stays robust to minor markup
 * changes (Docs in particular tweaks the inline styles every few
 * months without touching the class names).
 */

import type { Block, BlockTree } from '@gonext/blocks-sdk';

/**
 * The known clipboard sources we can convert with high fidelity.
 *
 *   - `'gdocs'`   — Google Docs (HTML carries `class="docs-internal-guid-..."`
 *     or `id="docs-internal-..."`)
 *   - `'word'`    — Microsoft Word / Office (`class="ms-Office-*"`,
 *     `OfficeStyle*`, or `<meta name=Generator content="Microsoft Word ...">`)
 *   - `'notion'`  — Notion (`class="notion-..."` or the
 *     `application/x-notion-text-block` MIME type)
 *   - `'markdown'` — text/plain looking like Markdown (no HTML)
 *   - `'html'`    — generic HTML, no recognisable source
 *   - `'text'`    — plain text fallback (no HTML, no Markdown markers)
 */
export type PasteSource =
  | 'gdocs'
  | 'word'
  | 'notion'
  | 'markdown'
  | 'html'
  | 'text';

/**
 * What `detectPasteSource()` returns. We keep both the source and the
 * raw payloads so the per-source converter doesn't have to re-read
 * the DataTransfer.
 */
export interface DetectedPaste {
  source: PasteSource;
  /** The HTML payload (`text/html`), if any. */
  html: string;
  /** The plain text payload (`text/plain`), if any. */
  text: string;
}

/**
 * Sniff the clipboard payload for known wrappers.
 *
 * The order matters: Notion's HTML wraps Docs-flavored snippets when
 * users paste Docs into Notion and then re-copy, so we check Notion
 * before Docs. Word last because its fingerprints overlap with generic
 * Office HTML email signatures.
 */
export function detectPasteSource(payload: {
  html?: string;
  text?: string;
}): DetectedPaste {
  const html = payload.html ?? '';
  const text = payload.text ?? '';

  if (
    html.includes('class="notion-') ||
    html.includes("class='notion-") ||
    html.includes('data-notion-')
  ) {
    return { source: 'notion', html, text };
  }
  if (
    html.includes('docs-internal-guid') ||
    html.includes('id="docs-internal') ||
    html.includes('class="docs-')
  ) {
    return { source: 'gdocs', html, text };
  }
  if (
    html.includes('ms-Office-') ||
    html.includes('mso-') ||
    /content="Microsoft Word/i.test(html) ||
    /<o:p\b/i.test(html)
  ) {
    return { source: 'word', html, text };
  }
  if (html.length > 0) {
    return { source: 'html', html, text };
  }
  // No HTML — decide between Markdown and plain text. Markdown is
  // characterised by line-leading `#`, `*`, `-`, fenced code blocks,
  // or `[text](url)` links. We err toward "markdown" only when at
  // least one strong signal is present so we don't mis-format a
  // user's plain paragraph.
  if (
    text.length > 0 &&
    (/^\s*#{1,6}\s+/m.test(text) ||
      /^\s*[-*+]\s+/m.test(text) ||
      /^\s*\d+\.\s+/m.test(text) ||
      /```/m.test(text) ||
      /\[[^\]]+\]\([^)]+\)/.test(text))
  ) {
    return { source: 'markdown', html, text };
  }
  return { source: 'text', html, text };
}

/**
 * Read an HTML payload into a `DocumentFragment` we can walk. Uses
 * `DOMParser` so the same code runs in jsdom (tests) and the browser.
 * Returns the body's child nodes — the parser otherwise wraps every
 * snippet in `<html><body>…`, which would bias the walker.
 */
function parseHtmlBody(html: string): HTMLElement {
  const parser = new DOMParser();
  const doc = parser.parseFromString(html, 'text/html');
  return doc.body;
}

/** Trim and collapse internal whitespace; common across all converters. */
function normaliseText(input: string): string {
  return input.replace(/\s+/g, ' ').trim();
}

/**
 * Convert a `<ul>` / `<ol>` element into a `core/list` block. Lists
 * keep their `<li>` items as `innerBlocks` of type `core/list-item`
 * (the SDK's list shape). Nested lists are flattened to plain text
 * inside each item — the editor's list block supports a `nested`
 * attribute, but we keep paste output conservative and let authors
 * indent after the fact.
 */
function listElementToBlock(el: Element, ordered: boolean): Block {
  const items: Block[] = [];
  for (const child of Array.from(el.children)) {
    if (child.tagName.toLowerCase() !== 'li') continue;
    items.push({
      type: 'core/list-item',
      attributes: { text: normaliseText(child.textContent ?? '') },
    });
  }
  return {
    type: 'core/list',
    attributes: { ordered },
    innerBlocks: items,
  };
}

/**
 * Pull a heading level (1-6) out of an `hN` tag name. Defaults to 2 so
 * a malformed element still produces a usable block.
 */
function headingLevel(tagName: string): number {
  const match = tagName.match(/^h([1-6])$/i);
  return match !== null ? Number(match[1]) : 2;
}

/**
 * Generic HTML → blocks walker. Used as the *base* for the
 * Docs / Word / Notion converters, then specialised with per-source
 * pre-processing (e.g. strip Docs' bogus `<b>` wrappers, drop Word's
 * MSO conditional comments, etc.). Returning early on text-only
 * payloads keeps the per-source code paths short.
 */
function htmlElementToBlocks(root: HTMLElement): BlockTree {
  const out: BlockTree = [];
  for (const node of Array.from(root.childNodes)) {
    if (node.nodeType === 3 /* TEXT_NODE */) {
      const text = normaliseText(node.textContent ?? '');
      if (text.length > 0) {
        out.push({ type: 'core/paragraph', attributes: { text } });
      }
      continue;
    }
    if (node.nodeType !== 1 /* ELEMENT_NODE */) continue;
    const el = node as Element;
    const tag = el.tagName.toLowerCase();

    if (/^h[1-6]$/.test(tag)) {
      out.push({
        type: 'core/heading',
        attributes: {
          level: headingLevel(tag),
          text: normaliseText(el.textContent ?? ''),
        },
      });
      continue;
    }
    if (tag === 'ul') {
      out.push(listElementToBlock(el, false));
      continue;
    }
    if (tag === 'ol') {
      out.push(listElementToBlock(el, true));
      continue;
    }
    if (tag === 'blockquote') {
      out.push({
        type: 'core/quote',
        attributes: { text: normaliseText(el.textContent ?? '') },
      });
      continue;
    }
    if (tag === 'pre' || tag === 'code') {
      // Preserve inner formatting for code — only collapse leading/trailing.
      out.push({
        type: 'core/code',
        attributes: { code: (el.textContent ?? '').replace(/^\s+|\s+$/g, '') },
      });
      continue;
    }
    if (tag === 'hr') {
      out.push({ type: 'core/separator', attributes: {} });
      continue;
    }
    if (tag === 'img') {
      const img = el as HTMLImageElement;
      out.push({
        type: 'core/image',
        attributes: {
          url: img.getAttribute('src') ?? '',
          alt: img.getAttribute('alt') ?? '',
        },
      });
      continue;
    }
    if (tag === 'p' || tag === 'div' || tag === 'span') {
      // Recurse: a Docs `<b id="docs-internal-...">` wrapper contains the
      // real blocks. Likewise Word emits `<div class="WordSection1">`
      // around the actual paragraphs. Treat these as transparent.
      const nested = htmlElementToBlocks(el as HTMLElement);
      if (nested.length > 0) {
        out.push(...nested);
        continue;
      }
      const text = normaliseText(el.textContent ?? '');
      if (text.length > 0) {
        out.push({ type: 'core/paragraph', attributes: { text } });
      }
      continue;
    }
    // Anything else (`<table>`, `<figure>`, etc.) collapses to a
    // paragraph so the author's prose survives even if the structure
    // doesn't. Block-author plugins can extend this list later.
    const text = normaliseText(el.textContent ?? '');
    if (text.length > 0) {
      out.push({ type: 'core/paragraph', attributes: { text } });
    }
  }
  return out;
}

/**
 * Google Docs wraps every paste in a top-level `<b id="docs-internal-guid-...">`
 * (yes, really, a `<b>` element — Docs uses it as a marker, not for
 * styling). Strip the wrapper before walking so the resulting block
 * tree is flat rather than wrapped in an accidental "bold" paragraph.
 */
function gdocsHtmlToBlocks(html: string): BlockTree {
  const body = parseHtmlBody(html);
  const wrapper = body.querySelector('b[id^="docs-internal"]');
  const root = wrapper !== null ? (wrapper as HTMLElement) : body;
  return htmlElementToBlocks(root);
}

/**
 * Microsoft Word emits an `<html xmlns:o="urn:schemas-microsoft-com:office:office">`
 * shell with MSO conditional comments wrapping every paragraph. The
 * `<o:p>` filler elements and the `class="MsoNormal"` divs are noise.
 * We strip both, then run the generic walker over the rest.
 */
function wordHtmlToBlocks(html: string): BlockTree {
  // Strip MSO conditional comments (they confuse the parser in some
  // browsers and add no semantic content).
  const cleaned = html
    .replace(/<!--\[if[^\]]*\]>[\s\S]*?<!\[endif\]-->/g, '')
    .replace(/<o:p>[\s\S]*?<\/o:p>/g, '')
    .replace(/<o:p\s*\/>/g, '');
  const body = parseHtmlBody(cleaned);
  return htmlElementToBlocks(body);
}

/**
 * Notion exports a clean-ish HTML but tags every block with a
 * `class="notion-..."` and wraps paragraphs in extra `<div>`s. The
 * generic walker is transparent across `<div>`s, so we only need to
 * strip Notion's wrapper if it exists.
 */
function notionHtmlToBlocks(html: string): BlockTree {
  const body = parseHtmlBody(html);
  // Notion sometimes wraps the whole paste in a single
  // `<div class="notion-selectable">`. Treat it as transparent.
  const wrapper = body.querySelector('div.notion-selectable');
  const root = wrapper !== null ? (wrapper as HTMLElement) : body;
  return htmlElementToBlocks(root);
}

/**
 * Convert Markdown text into blocks. We don't take a Markdown parser
 * dependency — paste workflows want predictable output more than they
 * want CommonMark fidelity. Supported syntax:
 *
 *   - `#`..`######` headings
 *   - `-`, `*`, `+` unordered lists
 *   - `1.` ordered lists
 *   - ``` ``` fenced code blocks ```
 *   - blank line as paragraph separator
 *   - everything else is a paragraph
 *
 * Future work: link parsing, inline emphasis. Out of scope for the
 * paste-pipeline issue — the editor's rich-text layer handles those
 * once a block exists.
 */
export function markdownToBlocks(input: string): BlockTree {
  const out: BlockTree = [];
  const lines = input.split(/\r?\n/);
  let i = 0;
  while (i < lines.length) {
    const line = lines[i] ?? '';
    // Fenced code.
    if (/^```/.test(line)) {
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i] ?? '')) {
        codeLines.push(lines[i] ?? '');
        i++;
      }
      if (i < lines.length) i++; // consume closing fence
      out.push({
        type: 'core/code',
        attributes: { code: codeLines.join('\n') },
      });
      continue;
    }
    // Heading.
    const heading = line.match(/^(#{1,6})\s+(.*)$/);
    if (heading !== null) {
      out.push({
        type: 'core/heading',
        attributes: {
          level: heading[1]?.length ?? 2,
          text: heading[2]?.trim() ?? '',
        },
      });
      i++;
      continue;
    }
    // Unordered list.
    if (/^\s*[-*+]\s+/.test(line)) {
      const items: Block[] = [];
      while (i < lines.length && /^\s*[-*+]\s+/.test(lines[i] ?? '')) {
        const text = (lines[i] ?? '').replace(/^\s*[-*+]\s+/, '').trim();
        items.push({ type: 'core/list-item', attributes: { text } });
        i++;
      }
      out.push({
        type: 'core/list',
        attributes: { ordered: false },
        innerBlocks: items,
      });
      continue;
    }
    // Ordered list.
    if (/^\s*\d+\.\s+/.test(line)) {
      const items: Block[] = [];
      while (i < lines.length && /^\s*\d+\.\s+/.test(lines[i] ?? '')) {
        const text = (lines[i] ?? '').replace(/^\s*\d+\.\s+/, '').trim();
        items.push({ type: 'core/list-item', attributes: { text } });
        i++;
      }
      out.push({
        type: 'core/list',
        attributes: { ordered: true },
        innerBlocks: items,
      });
      continue;
    }
    // Blank line — flush.
    if (/^\s*$/.test(line)) {
      i++;
      continue;
    }
    // Paragraph — gather consecutive non-empty lines.
    const paraLines: string[] = [];
    while (
      i < lines.length &&
      !/^\s*$/.test(lines[i] ?? '') &&
      !/^(#{1,6})\s+/.test(lines[i] ?? '') &&
      !/^\s*[-*+]\s+/.test(lines[i] ?? '') &&
      !/^\s*\d+\.\s+/.test(lines[i] ?? '') &&
      !/^```/.test(lines[i] ?? '')
    ) {
      paraLines.push((lines[i] ?? '').trim());
      i++;
    }
    if (paraLines.length > 0) {
      out.push({
        type: 'core/paragraph',
        attributes: { text: paraLines.join(' ') },
      });
    }
  }
  return out;
}

/**
 * Top-level converter. Dispatches to the per-source converter based on
 * the detected source. Exported for tests + callers that already have
 * a `DetectedPaste` in hand.
 */
export function convertPaste(detected: DetectedPaste): BlockTree {
  switch (detected.source) {
    case 'gdocs':
      return gdocsHtmlToBlocks(detected.html);
    case 'word':
      return wordHtmlToBlocks(detected.html);
    case 'notion':
      return notionHtmlToBlocks(detected.html);
    case 'markdown':
      return markdownToBlocks(detected.text);
    case 'html':
      return htmlElementToBlocks(parseHtmlBody(detected.html));
    case 'text':
      // Split on blank lines so each paragraph becomes its own block.
      return detected.text
        .split(/\r?\n\s*\r?\n/)
        .map((para) => para.trim())
        .filter((para) => para.length > 0)
        .map((para) => ({
          type: 'core/paragraph' as const,
          attributes: { text: para },
        }));
  }
}

/**
 * The shape host code wires into the canvas via `onPaste`. The handler
 * reads the clipboard, runs detection + conversion, and returns the
 * resulting tree. The host decides where to splice it into the
 * document (at caret, after selected block, etc).
 *
 * Calling `preventDefault()` is the host's call — sometimes the user
 * wants the browser's default paste (e.g. into a text input inside an
 * inspector control). We return `null` when there is nothing to
 * convert so the host can decide whether to suppress the default.
 */
export function onPaste(event: ClipboardEvent): BlockTree | null {
  const data = event.clipboardData;
  if (data === null) return null;
  const detected = detectPasteSource({
    html: data.getData('text/html'),
    text: data.getData('text/plain'),
  });
  const blocks = convertPaste(detected);
  return blocks.length > 0 ? blocks : null;
}
