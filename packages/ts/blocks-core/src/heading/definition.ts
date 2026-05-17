/**
 * `core/heading` registry definition.
 *
 * The schema bounds `level` to 1..6 (the only valid HTML heading ranks) and
 * keeps the optional `anchor` to slug-like ascii so the generated `id`
 * attribute is URL-safe by construction.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { HeadingAttributes } from './save.ts';

const attributes = {
  type: 'object',
  required: ['content', 'level'],
  additionalProperties: false,
  properties: {
    content: { type: 'string' },
    level: { type: 'integer', minimum: 1, maximum: 6 },
    anchor: {
      type: 'string',
      pattern: '^[a-z0-9][a-z0-9-]*$',
      maxLength: 128,
    },
    align: { type: 'string', enum: ['left', 'center', 'right'] },
  },
} as const;

export const headingDefinition: BlockTypeDefinition<HeadingAttributes> = {
  name: 'core/heading',
  title: 'Heading',
  category: 'text',
  description: 'Introduce a new section.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><text x="3" y="18" font-size="16" font-family="serif">H</text></svg>',
  attributes,
  supports: {
    align: ['left', 'center', 'right', 'wide', 'full'],
    color: { background: false, text: true },
    spacing: { margin: true, padding: true },
    reusable: true,
    lock: true,
  },
  edit: async () => {
    const mod = await import('./edit.tsx');
    return { default: mod.HeadingEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
