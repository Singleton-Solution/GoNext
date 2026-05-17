/**
 * `core/image` registry definition.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { ImageAttributes } from './save.ts';

const attributes = {
  type: 'object',
  required: ['url', 'alt'],
  additionalProperties: false,
  properties: {
    url: { type: 'string', minLength: 1, maxLength: 2048 },
    alt: { type: 'string', maxLength: 1024 },
    caption: { type: 'string', maxLength: 1024 },
    width: { type: 'integer', minimum: 1 },
    height: { type: 'integer', minimum: 1 },
    align: {
      type: 'string',
      enum: ['left', 'center', 'right', 'wide', 'full'],
    },
    href: { type: 'string', maxLength: 2048 },
  },
} as const;

export const imageDefinition: BlockTypeDefinition<ImageAttributes> = {
  name: 'core/image',
  title: 'Image',
  category: 'media',
  description: 'Insert an image.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="5" width="18" height="14" fill="none" stroke="currentColor"/><circle cx="9" cy="11" r="2"/><path d="M21 19l-6-6-4 4-2-2-6 4z"/></svg>',
  attributes,
  supports: {
    align: ['left', 'center', 'right', 'wide', 'full'],
    color: { background: false, text: false },
    spacing: { margin: true, padding: false },
    reusable: true,
    lock: true,
  },
  edit: async () => {
    const mod = await import('./edit.tsx');
    return { default: mod.ImageEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
