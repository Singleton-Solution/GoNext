/**
 * `core/file` registry definition.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { FileAttributes } from './save.ts';

const attributes = {
  type: 'object',
  required: ['href', 'fileName'],
  additionalProperties: false,
  properties: {
    href: { type: 'string', minLength: 1 },
    fileName: { type: 'string', minLength: 1 },
    downloadButton: { type: 'boolean' },
    textLinkHref: { type: 'boolean' },
  },
} as const;

export const fileDefinition: BlockTypeDefinition<FileAttributes> = {
  name: 'core/file',
  title: 'File',
  category: 'media',
  description: 'Add a link to a downloadable file.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 2h8l4 4v16H6z" fill="none" stroke="currentColor"/><path d="M14 2v4h4" fill="none" stroke="currentColor"/></svg>',
  attributes,
  supports: {
    align: ['left', 'center', 'right'],
    spacing: { margin: true, padding: true },
    reusable: true,
    lock: true,
  },
  edit: async () => {
    const mod = await import('./edit.tsx');
    return { default: mod.FileEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
