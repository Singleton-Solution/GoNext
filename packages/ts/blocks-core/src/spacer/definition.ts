/**
 * `core/spacer` registry definition.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { SpacerAttributes } from './save.ts';

const attributes = {
  type: 'object',
  required: ['height'],
  additionalProperties: false,
  properties: {
    height: { type: 'integer', minimum: 1, maximum: 2000 },
  },
} as const;

export const spacerDefinition: BlockTypeDefinition<SpacerAttributes> = {
  name: 'core/spacer',
  title: 'Spacer',
  category: 'design',
  description: 'Add white space between blocks.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M11 4h2v6h6v2h-6v6h-2v-6H5v-2h6z"/></svg>',
  attributes,
  supports: {
    align: ['wide', 'full'],
    color: { background: false, text: false },
    spacing: { margin: false, padding: false },
    reusable: false,
    lock: true,
  },
  edit: async () => {
    const mod = await import('./edit.tsx');
    return { default: mod.SpacerEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
