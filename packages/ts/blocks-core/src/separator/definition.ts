/**
 * `core/separator` registry definition.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { SeparatorAttributes } from './save.ts';

const attributes = {
  type: 'object',
  additionalProperties: false,
  properties: {
    style: { type: 'string', enum: ['default', 'wide', 'dots'] },
  },
} as const;

export const separatorDefinition: BlockTypeDefinition<SeparatorAttributes> = {
  name: 'core/separator',
  title: 'Separator',
  category: 'design',
  description: 'Create a break between ideas or sections.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 11h18v2H3z"/></svg>',
  attributes,
  supports: {
    align: ['center', 'wide', 'full'],
    color: { background: false, text: false },
    spacing: { margin: true, padding: false },
    reusable: false,
    lock: true,
  },
  edit: async () => {
    const mod = await import('./edit.tsx');
    return { default: mod.SeparatorEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
