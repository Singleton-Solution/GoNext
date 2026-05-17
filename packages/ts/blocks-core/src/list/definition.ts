/**
 * `core/list` registry definition.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { ListAttributes } from './save.ts';

const attributes = {
  type: 'object',
  required: ['ordered', 'values'],
  additionalProperties: false,
  properties: {
    ordered: { type: 'boolean' },
    values: {
      type: 'array',
      items: { type: 'string' },
      maxItems: 1024,
    },
    start: { type: 'integer' },
    reversed: { type: 'boolean' },
  },
} as const;

export const listDefinition: BlockTypeDefinition<ListAttributes> = {
  name: 'core/list',
  title: 'List',
  category: 'text',
  description: 'Create a bulleted or numbered list.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="4" y="6" width="2" height="2"/><rect x="4" y="11" width="2" height="2"/><rect x="4" y="16" width="2" height="2"/><rect x="9" y="6" width="11" height="2"/><rect x="9" y="11" width="11" height="2"/><rect x="9" y="16" width="11" height="2"/></svg>',
  attributes,
  supports: {
    align: ['left', 'center', 'right'],
    color: { background: false, text: true },
    spacing: { margin: true, padding: true },
    reusable: true,
    lock: true,
  },
  edit: async () => {
    const mod = await import('./edit.tsx');
    return { default: mod.ListEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
