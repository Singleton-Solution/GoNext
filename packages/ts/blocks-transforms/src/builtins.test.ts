/**
 * Tests for the first-party built-in transforms.
 *
 * Each transform is exercised with an input → expected-output snapshot.
 * The "ergonomics" suite at the bottom asserts the registry helper
 * registers every CORE_TRANSFORM under its documented id.
 */
import { describe, expect, it } from 'vitest';
import type { Block } from '@gonext/blocks-sdk';
import {
  CORE_TRANSFORMS,
  DEFAULT_COLUMNS,
  codeToParagraph,
  columnsToGroup,
  escapeHtml,
  groupToColumns,
  headingLevelDown,
  headingLevelUp,
  headingToParagraph,
  imageToGallery,
  listToParagraphs,
  paragraphsToList,
  paragraphToCode,
  paragraphToHeading,
  paragraphToQuote,
  quoteToParagraph,
} from './builtins.ts';
import { TransformRegistry } from './registry.ts';
import { registerBuiltinTransforms } from './registerBuiltins.ts';

function paragraph(content: string): Block {
  return { type: 'core/paragraph', attributes: { content } };
}

function heading(content: string, level: number): Block {
  return { type: 'core/heading', attributes: { content, level } };
}

describe('paragraph <-> heading', () => {
  it('paragraph → heading preserves the source text', () => {
    const out = paragraphToHeading.convert(paragraph('Hello, world.'));
    expect(out).toEqual({
      type: 'core/heading',
      attributes: { content: 'Hello, world.', level: 2 },
    });
  });

  it('heading → paragraph drops the level but keeps the content', () => {
    const out = headingToParagraph.convert(heading('Hi', 3));
    expect(out).toEqual({
      type: 'core/paragraph',
      attributes: { content: 'Hi' },
    });
  });

  it('paragraph → heading tolerates a missing content attribute', () => {
    const out = paragraphToHeading.convert({
      type: 'core/paragraph',
      attributes: {},
    });
    expect(out).toEqual({
      type: 'core/heading',
      attributes: { content: '', level: 2 },
    });
  });
});

describe('paragraph <-> quote', () => {
  it('paragraph → quote maps content to value', () => {
    const out = paragraphToQuote.convert(paragraph('great line.'));
    expect(out).toEqual({
      type: 'core/quote',
      attributes: { value: 'great line.' },
    });
  });

  it('quote → paragraph folds the citation in as a trailing dash line', () => {
    const out = quoteToParagraph.convert({
      type: 'core/quote',
      attributes: { value: 'great line.', citation: 'Anon' },
    });
    expect(out).toEqual({
      type: 'core/paragraph',
      attributes: { content: 'great line.\n\n— Anon' },
    });
  });

  it('quote → paragraph omits the dash when the quote has no citation', () => {
    const out = quoteToParagraph.convert({
      type: 'core/quote',
      attributes: { value: 'plain' },
    });
    expect(out).toEqual({
      type: 'core/paragraph',
      attributes: { content: 'plain' },
    });
  });
});

describe('list <-> paragraphs', () => {
  it('list → paragraphs splits item-by-item', () => {
    const out = listToParagraphs.convert({
      type: 'core/list',
      attributes: { ordered: false, values: ['one', 'two', 'three'] },
    });
    expect(out).toEqual([
      { type: 'core/paragraph', attributes: { content: 'one' } },
      { type: 'core/paragraph', attributes: { content: 'two' } },
      { type: 'core/paragraph', attributes: { content: 'three' } },
    ]);
  });

  it('list → paragraphs falls back to one empty paragraph when the list is empty', () => {
    const out = listToParagraphs.convert({
      type: 'core/list',
      attributes: { ordered: true, values: [] },
    });
    expect(out).toEqual({
      type: 'core/paragraph',
      attributes: { content: '' },
    });
  });

  it('paragraphs → list wraps the paragraph as a single-item unordered list', () => {
    const out = paragraphsToList.convert(paragraph('only line'));
    expect(out).toEqual({
      type: 'core/list',
      attributes: { ordered: false, values: ['only line'] },
    });
  });
});

describe('image → gallery', () => {
  it('wraps a single image in a one-item gallery', () => {
    const out = imageToGallery.convert({
      type: 'core/image',
      attributes: {
        url: 'https://example.com/cat.jpg',
        alt: 'A cat',
        caption: 'cute',
        width: 600,
        height: 400,
      },
    });
    expect(out).toEqual({
      type: 'core/gallery',
      attributes: {
        images: [
          {
            url: 'https://example.com/cat.jpg',
            alt: 'A cat',
            caption: 'cute',
            width: 600,
            height: 400,
          },
        ],
        columns: 1,
      },
    });
  });

  it('omits caption / width / height when absent on the source image', () => {
    const out = imageToGallery.convert({
      type: 'core/image',
      attributes: { url: 'https://example.com/x.jpg', alt: '' },
    });
    expect(out).toEqual({
      type: 'core/gallery',
      attributes: {
        images: [{ url: 'https://example.com/x.jpg', alt: '' }],
        columns: 1,
      },
    });
  });
});

describe('heading level shifts', () => {
  it('level up subtracts one (h3 → h2)', () => {
    const out = headingLevelUp.convert(heading('Section', 3));
    expect(out).toEqual({
      type: 'core/heading',
      attributes: { content: 'Section', level: 2 },
    });
  });

  it('level down adds one (h3 → h4)', () => {
    const out = headingLevelDown.convert(heading('Section', 3));
    expect(out).toEqual({
      type: 'core/heading',
      attributes: { content: 'Section', level: 4 },
    });
  });

  it('level up clamps to 1 (h1 stays h1)', () => {
    const out = headingLevelUp.convert(heading('top', 1));
    expect((out as Block).attributes['level']).toBe(1);
  });

  it('level down clamps to 6 (h6 stays h6)', () => {
    const out = headingLevelDown.convert(heading('bottom', 6));
    expect((out as Block).attributes['level']).toBe(6);
  });

  it('level up isMatch hides the transform on h1', () => {
    expect(headingLevelUp.isMatch?.(heading('x', 1))).toBe(false);
    expect(headingLevelUp.isMatch?.(heading('x', 2))).toBe(true);
  });

  it('level down isMatch hides the transform on h6', () => {
    expect(headingLevelDown.isMatch?.(heading('x', 6))).toBe(false);
    expect(headingLevelDown.isMatch?.(heading('x', 5))).toBe(true);
  });

  it('treats a missing level as h2 (the registry default)', () => {
    // Defensive: a malformed block tree should not throw inside the transform.
    const out = headingLevelUp.convert({
      type: 'core/heading',
      attributes: { content: 'x' },
    });
    expect((out as Block).attributes['level']).toBe(1);
  });
});

describe('code <-> paragraph', () => {
  it('code → paragraph escapes HTML by default', () => {
    const out = codeToParagraph.convert({
      type: 'core/code',
      attributes: { content: '<script>alert(1)</script>' },
    });
    expect(out).toEqual({
      type: 'core/paragraph',
      attributes: {
        content: '&lt;script&gt;alert(1)&lt;/script&gt;',
      },
    });
  });

  it('code → paragraph preserves bytes when escapeHtml is false', () => {
    const out = codeToParagraph.convert(
      {
        type: 'core/code',
        attributes: { content: '<b>raw</b>' },
      },
      { escapeHtml: false },
    );
    expect(out).toEqual({
      type: 'core/paragraph',
      attributes: { content: '<b>raw</b>' },
    });
  });

  it('paragraph → code keeps the text intact', () => {
    const out = paragraphToCode.convert(paragraph('a := 1'));
    expect(out).toEqual({
      type: 'core/code',
      attributes: { content: 'a := 1' },
    });
  });

  it('escapeHtml handles every special character', () => {
    expect(escapeHtml(`<>&"'`)).toBe('&lt;&gt;&amp;&quot;&#39;');
  });
});

describe('columns <-> group', () => {
  const columnsBlock: Block = {
    type: 'core/columns',
    attributes: { columns: 3, isStackedOnMobile: true },
    innerBlocks: [
      { type: 'core/paragraph', attributes: { content: 'a' } },
      { type: 'core/paragraph', attributes: { content: 'b' } },
      { type: 'core/paragraph', attributes: { content: 'c' } },
    ],
  };

  it('columns → group preserves the inner blocks', () => {
    const out = columnsToGroup.convert(columnsBlock);
    expect(out).toEqual({
      type: 'core/group',
      attributes: { tagName: 'div', layout: 'default' },
      innerBlocks: [
        { type: 'core/paragraph', attributes: { content: 'a' } },
        { type: 'core/paragraph', attributes: { content: 'b' } },
        { type: 'core/paragraph', attributes: { content: 'c' } },
      ],
    });
  });

  it('group → columns defaults to the documented column count', () => {
    const out = groupToColumns.convert({
      type: 'core/group',
      attributes: { tagName: 'div', layout: 'default' },
      innerBlocks: [
        { type: 'core/paragraph', attributes: { content: 'one' } },
        { type: 'core/paragraph', attributes: { content: 'two' } },
      ],
    });
    expect(out).toEqual({
      type: 'core/columns',
      attributes: { columns: DEFAULT_COLUMNS, isStackedOnMobile: true },
      innerBlocks: [
        { type: 'core/paragraph', attributes: { content: 'one' } },
        { type: 'core/paragraph', attributes: { content: 'two' } },
      ],
    });
  });

  it('group → columns honors the host-supplied column count', () => {
    const out = groupToColumns.convert(
      {
        type: 'core/group',
        attributes: { tagName: 'div', layout: 'default' },
        innerBlocks: [
          { type: 'core/paragraph', attributes: { content: 'one' } },
        ],
      },
      { columns: 4 },
    );
    expect((out as Block).attributes['columns']).toBe(4);
    expect((out as Block).innerBlocks).toHaveLength(4);
  });

  it('group → columns pads with empty groups when the source is too thin', () => {
    const out = groupToColumns.convert(
      {
        type: 'core/group',
        attributes: { tagName: 'div', layout: 'default' },
        innerBlocks: [],
      },
      { columns: 3 },
    );
    const inner = (out as Block).innerBlocks ?? [];
    expect(inner).toHaveLength(3);
    for (const child of inner) {
      expect(child.type).toBe('core/group');
    }
  });

  it('group → columns clamps the requested column count to the registry range', () => {
    const out = groupToColumns.convert(
      {
        type: 'core/group',
        attributes: { tagName: 'div', layout: 'default' },
        innerBlocks: [],
      },
      { columns: 99 },
    );
    expect((out as Block).attributes['columns']).toBe(6);
  });

  it('group → columns falls back to DEFAULT_COLUMNS on garbage input', () => {
    const out = groupToColumns.convert(
      {
        type: 'core/group',
        attributes: { tagName: 'div', layout: 'default' },
        innerBlocks: [],
      },
      { columns: 1.5 },
    );
    expect((out as Block).attributes['columns']).toBe(DEFAULT_COLUMNS);
  });
});

describe('registerBuiltinTransforms ergonomics', () => {
  it('registers every CORE_TRANSFORM in order', () => {
    const r = new TransformRegistry();
    registerBuiltinTransforms(r);
    expect(r.list().map((t) => t.id)).toEqual(
      CORE_TRANSFORMS.map((t) => t.id),
    );
  });

  it('second call without replace throws, with replace it succeeds', () => {
    const r = new TransformRegistry();
    registerBuiltinTransforms(r);
    expect(() => registerBuiltinTransforms(r)).toThrow();
    expect(() =>
      registerBuiltinTransforms(r, { replace: true }),
    ).not.toThrow();
  });

  it('every transform has a non-empty id, label, from, to', () => {
    for (const t of CORE_TRANSFORMS) {
      expect(t.id.length).toBeGreaterThan(0);
      expect(t.label.length).toBeGreaterThan(0);
      expect(t.from.length).toBeGreaterThan(0);
      expect(t.to.length).toBeGreaterThan(0);
    }
  });

  it('apply() works end-to-end via the registry', () => {
    const r = new TransformRegistry();
    registerBuiltinTransforms(r);
    const out = r.apply(
      'core/paragraph-to-heading',
      paragraph('Hello via apply'),
    );
    expect(out).toEqual({
      type: 'core/heading',
      attributes: { content: 'Hello via apply', level: 2 },
    });
  });
});
