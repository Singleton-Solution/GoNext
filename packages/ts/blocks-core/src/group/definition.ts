/**
 * `core/group` registry definition.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { GroupAttributes } from './save.ts';

const attributes = {
  type: 'object',
  additionalProperties: false,
  properties: {
    tagName: {
      type: 'string',
      enum: ['div', 'section', 'header', 'footer', 'main', 'article', 'aside'],
    },
    layout: { type: 'string', enum: ['default', 'flex', 'grid'] },
  },
} as const;

export const groupDefinition: BlockTypeDefinition<GroupAttributes> = {
  name: 'core/group',
  title: 'Group',
  category: 'design',
  description: 'Combine blocks into a single visual container.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="3" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2"/></svg>',
  attributes,
  supports: {
    innerBlocks: true,
    align: ['wide', 'full'],
    color: { background: true, text: true },
    spacing: { margin: true, padding: true },
    reusable: true,
    lock: true,
  },
  edit: async () => {
    const mod = await import('./edit.tsx');
    return { default: mod.GroupEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
