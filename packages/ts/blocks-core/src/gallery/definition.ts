/**
 * `core/gallery` registry definition.
 *
 * The image item schema permits an empty `alt` (decorative images) but
 * still requires the field so authoring tooling can prompt for it
 * everywhere.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { GalleryAttributes } from './save.ts';

const galleryImageSchema = {
  type: 'object',
  required: ['url', 'alt'],
  additionalProperties: false,
  properties: {
    url: { type: 'string', minLength: 1 },
    alt: { type: 'string' },
    caption: { type: 'string' },
    width: { type: 'integer', minimum: 1 },
    height: { type: 'integer', minimum: 1 },
  },
} as const;

const attributes = {
  type: 'object',
  required: ['images'],
  additionalProperties: false,
  properties: {
    images: { type: 'array', items: galleryImageSchema },
    columns: { type: 'integer', minimum: 1, maximum: 8 },
    imageCrop: { type: 'boolean' },
  },
} as const;

export const galleryDefinition: BlockTypeDefinition<GalleryAttributes> = {
  name: 'core/gallery',
  title: 'Gallery',
  category: 'media',
  description: 'Display multiple images in a rich gallery.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="3" width="8" height="8" fill="none" stroke="currentColor"/><rect x="13" y="3" width="8" height="8" fill="none" stroke="currentColor"/><rect x="3" y="13" width="8" height="8" fill="none" stroke="currentColor"/><rect x="13" y="13" width="8" height="8" fill="none" stroke="currentColor"/></svg>',
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
    return { default: mod.GalleryEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
