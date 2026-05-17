/**
 * `core/quote` registry definition.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { QuoteAttributes } from './save.ts';

const attributes = {
  type: 'object',
  required: ['value'],
  additionalProperties: false,
  properties: {
    value: { type: 'string' },
    citation: { type: 'string', maxLength: 512 },
    style: { type: 'string', enum: ['plain', 'large'] },
  },
} as const;

export const quoteDefinition: BlockTypeDefinition<QuoteAttributes> = {
  name: 'core/quote',
  title: 'Quote',
  category: 'text',
  description: 'Give quoted text visual emphasis.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 7H5a2 2 0 0 0-2 2v4h4v4H3v-2H2v4h7V11H5V9h4zm12 0h-4a2 2 0 0 0-2 2v4h4v4h-4v-2h-1v4h7V11h-4V9h4z"/></svg>',
  attributes,
  supports: {
    align: ['left', 'center', 'right', 'wide'],
    color: { background: true, text: true },
    spacing: { margin: true, padding: true },
    reusable: true,
    lock: true,
  },
  edit: async () => {
    const mod = await import('./edit.tsx');
    return { default: mod.QuoteEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
