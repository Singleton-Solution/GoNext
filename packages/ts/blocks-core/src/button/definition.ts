/**
 * `core/button` registry definition.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { ButtonAttributes } from './save.ts';

const attributes = {
  type: 'object',
  required: ['text'],
  additionalProperties: false,
  properties: {
    text: { type: 'string' },
    url: { type: 'string' },
    linkTarget: { type: 'string', enum: ['_self', '_blank'] },
    style: { type: 'string', enum: ['fill', 'outline'] },
    borderRadius: { type: 'integer', minimum: 0, maximum: 100 },
    align: { type: 'string', enum: ['left', 'center', 'right'] },
  },
} as const;

export const buttonDefinition: BlockTypeDefinition<ButtonAttributes> = {
  name: 'core/button',
  title: 'Button',
  category: 'design',
  description: 'Prompt visitors to take action with a button-style link.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="4" y="8" width="16" height="8" rx="4" fill="none" stroke="currentColor"/></svg>',
  attributes,
  supports: {
    align: ['left', 'center', 'right'],
    color: { background: true, text: true },
    spacing: { margin: true, padding: true },
    reusable: true,
    lock: true,
  },
  edit: async () => {
    const mod = await import('./edit.tsx');
    return { default: mod.ButtonEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
