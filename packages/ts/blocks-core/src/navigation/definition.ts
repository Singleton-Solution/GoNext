/**
 * `core/navigation` registry definition.
 *
 * Leaf block — the menu list lives in the `items` attribute (or via
 * server-resolved `menuId`). We intentionally do NOT set
 * `supports.innerBlocks` so the canvas treats this as a single self-
 * contained node. Themes wanting block-composed navs can compose a
 * `core/group` with link blocks instead.
 *
 * The recursive `items` schema is one level deep — a flat list with
 * optional children. Deeper submenus need a different shape; we'll
 * revisit when product asks for it.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { NavigationAttributes } from './save.ts';

const navigationItemSchema = {
  type: 'object',
  required: ['label', 'url'],
  additionalProperties: false,
  properties: {
    label: { type: 'string', minLength: 1 },
    url: { type: 'string' },
    rel: { type: 'string' },
    target: { type: 'string', enum: ['_self', '_blank'] },
    children: {
      type: 'array',
      items: {
        type: 'object',
        required: ['label', 'url'],
        additionalProperties: false,
        properties: {
          label: { type: 'string', minLength: 1 },
          url: { type: 'string' },
          rel: { type: 'string' },
          target: { type: 'string', enum: ['_self', '_blank'] },
        },
      },
    },
  },
} as const;

const attributes = {
  type: 'object',
  additionalProperties: false,
  properties: {
    items: { type: 'array', items: navigationItemSchema },
    menuId: { type: 'string', minLength: 1 },
    orientation: { type: 'string', enum: ['horizontal', 'vertical'] },
    ariaLabel: { type: 'string' },
    hideToggle: { type: 'boolean' },
  },
} as const;

export const navigationDefinition: BlockTypeDefinition<NavigationAttributes> = {
  name: 'core/navigation',
  title: 'Navigation',
  category: 'theme',
  description:
    'Render a header or footer menu with optional mobile toggle and nested items.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><line x1="4" y1="7" x2="20" y2="7" stroke="currentColor" stroke-width="2"/><line x1="4" y1="12" x2="20" y2="12" stroke="currentColor" stroke-width="2"/><line x1="4" y1="17" x2="20" y2="17" stroke="currentColor" stroke-width="2"/></svg>',
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
    return { default: mod.NavigationEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
