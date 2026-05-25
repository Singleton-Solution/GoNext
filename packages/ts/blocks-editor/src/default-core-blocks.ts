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
 * Lucide-glyph inline SVGs for the core block tiles. Hand-copied from
 * lucide.dev so the inserter can render proper icons without pulling
 * `lucide-react` into the SDK (it's a peer-light surface). Each path
 * is the exact `lucide` source minus the wrapper attributes — we add
 * width/height/stroke at render time via the `.gonext-block-inserter
 * __tile-icon svg` rule in editor-theme.css.
 */
const ICON_TYPE =
  '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="4 7 4 4 20 4 20 7"></polyline><line x1="9" y1="20" x2="15" y2="20"></line><line x1="12" y1="4" x2="12" y2="20"></line></svg>';

const ICON_HEADING_1 =
  '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 12h8"></path><path d="M4 18V6"></path><path d="M12 18V6"></path><path d="m17 12 3-2v8"></path></svg>';

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
  icon: ICON_TYPE,
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
  icon: ICON_HEADING_1,
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
