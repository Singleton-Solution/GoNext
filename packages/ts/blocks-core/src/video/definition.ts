/**
 * `core/video` registry definition.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { VideoAttributes } from './save.ts';

const attributes = {
  type: 'object',
  required: ['src'],
  additionalProperties: false,
  properties: {
    src: { type: 'string', minLength: 1 },
    poster: { type: 'string' },
    controls: { type: 'boolean' },
    autoplay: { type: 'boolean' },
    loop: { type: 'boolean' },
    muted: { type: 'boolean' },
    caption: { type: 'string' },
  },
} as const;

export const videoDefinition: BlockTypeDefinition<VideoAttributes> = {
  name: 'core/video',
  title: 'Video',
  category: 'media',
  description: 'Embed a video from your media library or upload a new one.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="5" width="18" height="14" rx="2" fill="none" stroke="currentColor"/><polygon points="10,8 16,12 10,16" fill="currentColor"/></svg>',
  attributes,
  supports: {
    align: ['left', 'center', 'right', 'wide', 'full'],
    spacing: { margin: true, padding: true },
    reusable: true,
    lock: true,
  },
  edit: async () => {
    const mod = await import('./edit.tsx');
    return { default: mod.VideoEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
