/**
 * `core/columns` registry definition.
 *
 * Container block: accepts children via `supports.innerBlocks`. The
 * `allowedChildren` list is intentionally lenient — themes that want
 * stricter column shapes can override at the app layer.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { ColumnsAttributes } from './save.ts';

const attributes = {
  type: 'object',
  required: ['columns'],
  additionalProperties: false,
  properties: {
    columns: { type: 'integer', minimum: 2, maximum: 6 },
    isStackedOnMobile: { type: 'boolean' },
    verticalAlignment: {
      type: 'string',
      enum: ['top', 'center', 'bottom'],
    },
  },
} as const;

export const columnsDefinition: BlockTypeDefinition<ColumnsAttributes> = {
  name: 'core/columns',
  title: 'Columns',
  category: 'design',
  description: 'Display content in side-by-side columns.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="4" y="4" width="6" height="16"/><rect x="14" y="4" width="6" height="16"/></svg>',
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
    return { default: mod.ColumnsEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
