/**
 * `core/embed` registry definition.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { EmbedAttributes } from './save.ts';

const attributes = {
  type: 'object',
  required: ['url'],
  additionalProperties: false,
  properties: {
    url: { type: 'string', minLength: 1 },
    providerNameSlug: { type: 'string' },
    responsive: { type: 'boolean' },
    aspectRatio: { type: 'string' },
  },
} as const;

export const embedDefinition: BlockTypeDefinition<EmbedAttributes> = {
  name: 'core/embed',
  title: 'Embed',
  category: 'embed',
  description: 'Embed videos, posts and other content from supported providers.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 6h18v12H3z" fill="none" stroke="currentColor"/><polygon points="10,9 15,12 10,15" fill="currentColor"/></svg>',
  attributes,
  supports: {
    align: ['left', 'center', 'right', 'wide', 'full'],
    spacing: { margin: true, padding: true },
    reusable: true,
    lock: true,
  },
  edit: async () => {
    const mod = await import('./edit.tsx');
    return { default: mod.EmbedEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
