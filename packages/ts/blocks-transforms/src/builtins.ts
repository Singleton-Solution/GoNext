/**
 * First-party block transforms.
 *
 * Each transform is a pure function over `Block` / `BlockTree` — no
 * React, no DOM, no IO. Every transform is exported individually so
 * apps can pick a subset (e.g. theme-only installs that ship without
 * the columns block) without touching the registry helpers.
 *
 * The catalogue covers the "change block type" conversions the task
 * description asks for, plus a small handful of obvious wins:
 *
 *  - `core/paragraph-to-heading`         paragraph → heading
 *  - `core/heading-to-paragraph`         heading   → paragraph
 *  - `core/paragraph-to-quote`           paragraph → quote
 *  - `core/quote-to-paragraph`           quote     → paragraph
 *  - `core/list-to-paragraphs`           list      → N paragraphs
 *  - `core/paragraphs-to-list`           paragraph → list (single seed)
 *  - `core/image-to-gallery`             image     → gallery
 *  - `core/heading-level-up`             heading   → heading (-1, clamped to 1)
 *  - `core/heading-level-down`           heading   → heading (+1, clamped to 6)
 *  - `core/code-to-paragraph`            code      → paragraph (HTML-escaped)
 *  - `core/paragraph-to-code`            paragraph → code
 *  - `core/columns-to-group`             columns   → group
 *  - `core/group-to-columns`             group     → columns
 *
 * The list-to-paragraphs and paragraphs-to-list pair lean on the
 * editor's tree-mutation API (the host normalises a `Block[]` return
 * value by splicing the siblings into the parent's `innerBlocks`).
 *
 * Heading level shifts use `isMatch` to opt themselves out at the
 * clamp boundary so the dropdown only surfaces the transform when it
 * would actually change something.
 */
import type { Block } from '@gonext/blocks-sdk';
import type { Transform, TransformContext } from './types.ts';

/** Default column count when the host doesn't supply one. */
export const DEFAULT_COLUMNS = 2;

/**
 * Conservative HTML escape — turns `<`, `>`, `&`, `"`, `'` into the
 * matching named/numeric entities. Used by `code → paragraph` so a
 * code block's raw bytes don't suddenly render as live markup.
 */
export function escapeHtml(input: string): string {
  return input
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/** Read a string attribute with a default. */
function readString(
  attrs: Record<string, unknown>,
  key: string,
  fallback = '',
): string {
  const value = attrs[key];
  return typeof value === 'string' ? value : fallback;
}

/** Read an integer attribute clamped to [min, max] with a default. */
function readClampedInt(
  attrs: Record<string, unknown>,
  key: string,
  fallback: number,
  min: number,
  max: number,
): number {
  const value = attrs[key];
  if (typeof value !== 'number' || !Number.isInteger(value)) return fallback;
  return Math.max(min, Math.min(max, value));
}

/* -------------------------------------------------------------------- *
 * Paragraph <-> Heading
 * -------------------------------------------------------------------- */

export const paragraphToHeading: Transform = {
  id: 'core/paragraph-to-heading',
  from: 'core/paragraph',
  to: 'core/heading',
  label: 'Heading',
  description: 'Promote this paragraph to a level-2 heading.',
  convert(block) {
    return {
      type: 'core/heading',
      attributes: {
        content: readString(block.attributes, 'content'),
        level: 2,
      },
    };
  },
};

export const headingToParagraph: Transform = {
  id: 'core/heading-to-paragraph',
  from: 'core/heading',
  to: 'core/paragraph',
  label: 'Paragraph',
  description: 'Demote this heading to a paragraph.',
  convert(block) {
    return {
      type: 'core/paragraph',
      attributes: {
        content: readString(block.attributes, 'content'),
      },
    };
  },
};

/* -------------------------------------------------------------------- *
 * Paragraph <-> Quote
 * -------------------------------------------------------------------- */

export const paragraphToQuote: Transform = {
  id: 'core/paragraph-to-quote',
  from: 'core/paragraph',
  to: 'core/quote',
  label: 'Quote',
  description: 'Promote this paragraph to a pull-quote.',
  convert(block) {
    return {
      type: 'core/quote',
      attributes: {
        value: readString(block.attributes, 'content'),
      },
    };
  },
};

export const quoteToParagraph: Transform = {
  id: 'core/quote-to-paragraph',
  from: 'core/quote',
  to: 'core/paragraph',
  label: 'Paragraph',
  description: 'Demote this quote to a paragraph.',
  convert(block) {
    // The quote's citation is preserved as a trailing dash-prefixed line
    // so the conversion stays lossless from the author's point of view.
    const value = readString(block.attributes, 'value');
    const citation = readString(block.attributes, 'citation');
    const content =
      citation.length > 0 ? `${value}\n\n— ${citation}` : value;
    return {
      type: 'core/paragraph',
      attributes: { content },
    };
  },
};

/* -------------------------------------------------------------------- *
 * List <-> Paragraphs
 * -------------------------------------------------------------------- */

export const listToParagraphs: Transform = {
  id: 'core/list-to-paragraphs',
  from: 'core/list',
  to: 'core/paragraph',
  label: 'Paragraphs',
  description: 'Split this list into one paragraph per item.',
  convert(block) {
    const raw = block.attributes['values'];
    const values: string[] = Array.isArray(raw)
      ? raw.filter((v): v is string => typeof v === 'string')
      : [];

    if (values.length === 0) {
      // Preserve a single empty paragraph as the conversion output so
      // the host always has at least one sibling to splice in.
      return { type: 'core/paragraph', attributes: { content: '' } };
    }

    return values.map((value) => ({
      type: 'core/paragraph',
      attributes: { content: value },
    }));
  },
};

export const paragraphsToList: Transform = {
  id: 'core/paragraphs-to-list',
  from: 'core/paragraph',
  to: 'core/list',
  label: 'List',
  description: 'Wrap this paragraph in a single-item unordered list.',
  convert(block) {
    return {
      type: 'core/list',
      attributes: {
        ordered: false,
        values: [readString(block.attributes, 'content')],
      },
    };
  },
};

/* -------------------------------------------------------------------- *
 * Image -> Gallery
 * -------------------------------------------------------------------- */

export const imageToGallery: Transform = {
  id: 'core/image-to-gallery',
  from: 'core/image',
  to: 'core/gallery',
  label: 'Gallery',
  description: 'Wrap this image in a single-item gallery.',
  convert(block) {
    const url = readString(block.attributes, 'url');
    const alt = readString(block.attributes, 'alt');
    const caption = readString(block.attributes, 'caption');
    const widthAttr = block.attributes['width'];
    const heightAttr = block.attributes['height'];

    const image: Record<string, unknown> = { url, alt };
    if (caption.length > 0) image['caption'] = caption;
    if (typeof widthAttr === 'number') image['width'] = widthAttr;
    if (typeof heightAttr === 'number') image['height'] = heightAttr;

    return {
      type: 'core/gallery',
      attributes: {
        images: [image],
        columns: 1,
      },
    };
  },
};

/* -------------------------------------------------------------------- *
 * Heading level shifts
 * -------------------------------------------------------------------- */

export const headingLevelUp: Transform = {
  id: 'core/heading-level-up',
  from: 'core/heading',
  to: 'core/heading',
  label: 'Heading (one level up)',
  description: 'Promote this heading to the next level up (h2 → h1).',
  isMatch(block) {
    const level = readClampedInt(block.attributes, 'level', 2, 1, 6);
    return level > 1;
  },
  convert(block) {
    const level = readClampedInt(block.attributes, 'level', 2, 1, 6);
    const next = Math.max(1, level - 1);
    return {
      type: 'core/heading',
      attributes: { ...block.attributes, level: next },
    };
  },
};

export const headingLevelDown: Transform = {
  id: 'core/heading-level-down',
  from: 'core/heading',
  to: 'core/heading',
  label: 'Heading (one level down)',
  description: 'Demote this heading to the next level down (h2 → h3).',
  isMatch(block) {
    const level = readClampedInt(block.attributes, 'level', 2, 1, 6);
    return level < 6;
  },
  convert(block) {
    const level = readClampedInt(block.attributes, 'level', 2, 1, 6);
    const next = Math.min(6, level + 1);
    return {
      type: 'core/heading',
      attributes: { ...block.attributes, level: next },
    };
  },
};

/* -------------------------------------------------------------------- *
 * Code <-> Paragraph
 * -------------------------------------------------------------------- */

export const codeToParagraph: Transform = {
  id: 'core/code-to-paragraph',
  from: 'core/code',
  to: 'core/paragraph',
  label: 'Paragraph',
  description: 'Demote this code block to a paragraph (HTML-escaped).',
  convert(block, context) {
    const raw = readString(block.attributes, 'content');
    const shouldEscape = context?.escapeHtml ?? true;
    return {
      type: 'core/paragraph',
      attributes: {
        content: shouldEscape ? escapeHtml(raw) : raw,
      },
    };
  },
};

export const paragraphToCode: Transform = {
  id: 'core/paragraph-to-code',
  from: 'core/paragraph',
  to: 'core/code',
  label: 'Code',
  description: 'Promote this paragraph to a code block.',
  convert(block) {
    return {
      type: 'core/code',
      attributes: {
        content: readString(block.attributes, 'content'),
      },
    };
  },
};

/* -------------------------------------------------------------------- *
 * Columns <-> Group
 * -------------------------------------------------------------------- */

export const columnsToGroup: Transform = {
  id: 'core/columns-to-group',
  from: 'core/columns',
  to: 'core/group',
  label: 'Group',
  description:
    'Unwrap these columns into a single Group, preserving the inner blocks.',
  convert(block) {
    return {
      type: 'core/group',
      attributes: { tagName: 'div', layout: 'default' },
      innerBlocks: cloneInnerBlocks(block.innerBlocks),
    };
  },
};

export const groupToColumns: Transform = {
  id: 'core/group-to-columns',
  from: 'core/group',
  to: 'core/columns',
  label: 'Columns',
  description:
    'Wrap this group as Columns. The editor prompts for the column count; defaults to 2.',
  convert(block, context) {
    const requested = context?.columns ?? DEFAULT_COLUMNS;
    const columns = readClampedIntFromNumber(requested, DEFAULT_COLUMNS, 2, 6);

    const inner = cloneInnerBlocks(block.innerBlocks);

    // If the group has at most one child, we keep the child as the
    // single column entry — the columns block requires at least two
    // children for the layout to be meaningful, so we pad with empty
    // group children up to `columns` to give the author a real
    // starting tree.
    while (inner.length < columns) {
      inner.push({
        type: 'core/group',
        attributes: { tagName: 'div', layout: 'default' },
        innerBlocks: [],
      });
    }

    return {
      type: 'core/columns',
      attributes: { columns, isStackedOnMobile: true },
      innerBlocks: inner,
    };
  },
};

/* -------------------------------------------------------------------- *
 * Helpers
 * -------------------------------------------------------------------- */

function readClampedIntFromNumber(
  value: number,
  fallback: number,
  min: number,
  max: number,
): number {
  if (!Number.isInteger(value)) return fallback;
  return Math.max(min, Math.min(max, value));
}

/**
 * Shallow-clone the `innerBlocks` array (a new array shell, same
 * Block references). Transforms are pure, so the host can rely on
 * "I get a new outer shell I can mutate" semantics. We don't deep-
 * clone — the editor's tree-mutation pipeline already wraps the
 * splice in its own immutability layer.
 */
function cloneInnerBlocks(inner: Block[] | undefined): Block[] {
  return inner === undefined ? [] : [...inner];
}

/**
 * The complete ordered list of every first-party transform, in the
 * order they appear in the toolbar dropdown. Consumer code can
 * iterate this list to drive a per-transform UI inventory outside
 * of the registry path.
 */
export const CORE_TRANSFORMS: readonly Transform[] = [
  paragraphToHeading,
  headingToParagraph,
  paragraphToQuote,
  quoteToParagraph,
  listToParagraphs,
  paragraphsToList,
  imageToGallery,
  headingLevelUp,
  headingLevelDown,
  codeToParagraph,
  paragraphToCode,
  columnsToGroup,
  groupToColumns,
] as const;

export type { Transform, TransformContext };
