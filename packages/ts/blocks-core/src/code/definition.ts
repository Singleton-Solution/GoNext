/**
 * `core/code` registry definition.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { CodeAttributes } from './save.ts';

const attributes = {
  type: 'object',
  required: ['content'],
  additionalProperties: false,
  properties: {
    content: { type: 'string' },
    language: {
      type: 'string',
      // Conservative slug — matches what Prism/Shiki accept.
      pattern: '^[a-z0-9+-]{1,32}$',
    },
  },
} as const;

export const codeDefinition: BlockTypeDefinition<CodeAttributes> = {
  name: 'core/code',
  title: 'Code',
  category: 'text',
  description: 'Display code with syntax highlighting.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 6l-6 6 6 6m8-12l6 6-6 6" fill="none" stroke="currentColor" stroke-width="2"/></svg>',
  attributes,
  supports: {
    align: ['wide', 'full'],
    color: { background: true, text: true },
    spacing: { margin: true, padding: true },
    html: false,
    reusable: true,
    lock: true,
  },
  edit: async () => {
    const mod = await import('./edit.tsx');
    return { default: mod.CodeEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
