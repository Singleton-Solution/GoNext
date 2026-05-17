/**
 * `defaultCoreBlocks` — registers a tiny set of placeholder core blocks
 * (paragraph, heading) into a given `BlockRegistry`.
 *
 * The intent is to make the inserter immediately useful in tests and demo
 * apps. The `edit` components are deliberately minimal placeholders: they
 * render a `<div data-block="...">` so consumers can assert on them without
 * pulling in real editor surfaces. Production block authors should publish
 * their own definitions and not depend on these.
 *
 * Two design notes worth knowing about:
 *
 *  1. The `edit` import is wrapped in `async () => ({ default: ... })` so it
 *     mirrors the lazy-import contract `BlockTypeDefinition.edit` exposes —
 *     `BlockEditCanvas` always treats `edit` as a dynamic module factory.
 *  2. The attribute schemas are real JSON Schema documents so the registry's
 *     `validate()` works against trees built from these blocks. Authors who
 *     want a quick smoke test of validation can use these directly.
 */
import type {
  BlockEditProps,
  BlockRegistry,
  BlockTypeDefinition,
} from '@gonext/blocks-sdk';
import { createElement } from 'react';

/**
 * The placeholder paragraph block. Single `text` attribute (string), no
 * inner blocks, lives in the `text` category. The edit surface is a
 * `data-block="core/paragraph"` div showing the text or a placeholder hint.
 */
export const paragraphBlock: BlockTypeDefinition<{ text: string }> = {
  name: 'core/paragraph',
  title: 'Paragraph',
  category: 'text',
  description: 'Start with the building block of all narrative.',
  // A minimal "P" glyph so the inserter shows something next to the title.
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><text x="6" y="18" font-size="18" font-family="serif">P</text></svg>',
  attributes: {
    type: 'object',
    required: ['text'],
    additionalProperties: false,
    properties: { text: { type: 'string' } },
  },
  edit: async () => ({
    default: ({ attributes }: BlockEditProps<{ text: string }>) =>
      // `createElement` rather than JSX keeps this file `.ts` instead of
      // `.tsx`. Either would work; we just lean on the function form.
      createElement(
        'div',
        { 'data-block': 'core/paragraph' },
        attributes.text || 'Paragraph placeholder',
      ) as unknown,
  }),
};

/**
 * The placeholder heading block. `level` (1–6) and `text` attributes. The
 * edit surface emits an `<hN>` so visual hierarchy is obvious in snapshots.
 */
export const headingBlock: BlockTypeDefinition<{
  level: number;
  text: string;
}> = {
  name: 'core/heading',
  title: 'Heading',
  category: 'text',
  description: 'Introduce a new section.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><text x="3" y="18" font-size="16" font-family="serif">H</text></svg>',
  attributes: {
    type: 'object',
    required: ['level', 'text'],
    additionalProperties: false,
    properties: {
      level: { type: 'integer', minimum: 1, maximum: 6 },
      text: { type: 'string' },
    },
  },
  edit: async () => ({
    default: (
      { attributes }: BlockEditProps<{ level: number; text: string }>,
    ) => {
      const tag = `h${attributes.level ?? 2}`;
      return createElement(
        tag,
        { 'data-block': 'core/heading' },
        attributes.text || 'Heading placeholder',
      ) as unknown;
    },
  }),
};

/**
 * Register paragraph + heading into `registry`. Idempotent in the HMR sense
 * — passing `{ replace: true }` swaps the existing entries in place rather
 * than throwing `DuplicateBlockTypeError`. Production code should leave
 * `replace` alone so collisions surface loudly.
 */
export function defaultCoreBlocks(
  registry: BlockRegistry,
  options: { replace?: boolean } = {},
): void {
  registry.register(paragraphBlock, options);
  registry.register(headingBlock, options);
}
