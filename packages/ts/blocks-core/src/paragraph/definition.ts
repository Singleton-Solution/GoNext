/**
 * `core/paragraph` registry definition.
 *
 * Single source of truth for the paragraph block's name, schema, supports
 * matrix, and lazy edit/save imports. Consumers register this via
 * `registerCoreBlocks(registry)` in `../index.ts`.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { ParagraphAttributes } from './save.ts';

/**
 * JSON Schema for the paragraph block's attributes.
 *
 * Mirrors `ParagraphAttributes` exactly. We keep it as a `const` literal so
 * the registry's validator can hand it straight to Ajv without a runtime
 * conversion step.
 */
const attributes = {
  type: 'object',
  required: ['content'],
  additionalProperties: false,
  properties: {
    content: { type: 'string' },
    align: { type: 'string', enum: ['left', 'center', 'right'] },
    dropCap: { type: 'boolean' },
  },
} as const;

export const paragraphDefinition: BlockTypeDefinition<ParagraphAttributes> = {
  name: 'core/paragraph',
  title: 'Paragraph',
  category: 'text',
  description: 'Start with the building block of all narrative.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><text x="6" y="18" font-size="18" font-family="serif">P</text></svg>',
  attributes,
  supports: {
    align: ['left', 'center', 'right', 'wide', 'full'],
    color: { background: true, text: true },
    spacing: { margin: true, padding: true },
    reusable: true,
    lock: true,
  },
  edit: async () => {
    const mod = await import('./edit.tsx');
    return { default: mod.ParagraphEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
