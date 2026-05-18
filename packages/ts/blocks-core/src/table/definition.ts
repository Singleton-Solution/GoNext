/**
 * `core/table` registry definition.
 *
 * The attribute schema treats cells as plain string matrices. We don't allow
 * additional properties on the `style` sub-object so a typo lands a clear
 * validation error rather than silently no-op-ing.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { TableAttributes } from './save.ts';

const rowSchema = {
  type: 'array',
  items: { type: 'string' },
} as const;

const sectionSchema = {
  type: 'array',
  items: rowSchema,
} as const;

const attributes = {
  type: 'object',
  required: ['body'],
  additionalProperties: false,
  properties: {
    head: sectionSchema,
    body: sectionSchema,
    foot: sectionSchema,
    caption: { type: 'string' },
    style: {
      type: 'object',
      additionalProperties: false,
      properties: {
        stripes: { type: 'boolean' },
        borders: { type: 'boolean' },
      },
    },
  },
} as const;

export const tableDefinition: BlockTypeDefinition<TableAttributes> = {
  name: 'core/table',
  title: 'Table',
  category: 'text',
  description: 'Insert a table — perfect for sharing charts and data.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="5" width="18" height="14" fill="none" stroke="currentColor"/><line x1="3" y1="10" x2="21" y2="10" stroke="currentColor"/><line x1="3" y1="15" x2="21" y2="15" stroke="currentColor"/><line x1="9" y1="5" x2="9" y2="19" stroke="currentColor"/><line x1="15" y1="5" x2="15" y2="19" stroke="currentColor"/></svg>',
  attributes,
  supports: {
    align: ['wide', 'full'],
    color: { background: true, text: true },
    spacing: { margin: true, padding: true },
    reusable: true,
    lock: true,
  },
  edit: async () => {
    const mod = await import('./edit.tsx');
    return { default: mod.TableEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
