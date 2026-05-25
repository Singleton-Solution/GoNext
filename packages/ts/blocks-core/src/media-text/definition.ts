/**
 * `core/media-text` registry definition.
 *
 * Container block: accepts arbitrary children in the text column via
 * `supports.innerBlocks`. The media side is a single attribute-driven
 * figure rather than a child block — this mirrors WordPress's Media &
 * Text and keeps the round-trip stable across importers.
 *
 * The width / position / alignment knobs all carry JSON-Schema bounds so
 * the validator catches malformed plugin-emitted attrs at import time.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { MediaTextAttributes } from './save.ts';

const attributes = {
  type: 'object',
  required: ['mediaUrl', 'mediaAlt'],
  additionalProperties: false,
  properties: {
    mediaUrl: { type: 'string' },
    mediaAlt: { type: 'string' },
    mediaPosition: { type: 'string', enum: ['left', 'right'] },
    mediaWidth: { type: 'integer', minimum: 10, maximum: 90 },
    verticalAlignment: {
      type: 'string',
      enum: ['top', 'center', 'bottom'],
    },
    imageFill: { type: 'boolean' },
    mediaCaption: { type: 'string' },
  },
} as const;

export const mediaTextDefinition: BlockTypeDefinition<MediaTextAttributes> = {
  name: 'core/media-text',
  title: 'Media & Text',
  category: 'media',
  description:
    'Set media side-by-side with text — image on the left or right, vertically aligned, optional fill.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="5" width="8" height="14" fill="none" stroke="currentColor"/><line x1="13" y1="7" x2="21" y2="7" stroke="currentColor"/><line x1="13" y1="12" x2="21" y2="12" stroke="currentColor"/><line x1="13" y1="17" x2="19" y2="17" stroke="currentColor"/></svg>',
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
    return { default: mod.MediaTextEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
