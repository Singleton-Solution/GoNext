/**
 * `core/query` registry definition.
 *
 * Container block: the inner-blocks tree is the **post card template**
 * the server walker repeats once per matched post. We set
 * `supports.innerBlocks = true` but leave `allowedChildren` open — at
 * runtime the walker provides a per-post context object (id, title,
 * excerpt, author, URL, featured image) that template blocks can
 * reference via context-binding (a future surface; for the initial
 * landing the inner tree renders the same shape per row).
 *
 * The filter attributes (authorId/category/tag/search/limit/order)
 * carry tight JSON-Schema bounds so an importer can't smuggle a billion-
 * row LIMIT or an unsigned offset through the validator.
 */
import type { BlockTypeDefinition } from '@gonext/blocks-sdk';
import type { QueryAttributes } from './save.ts';

const attributes = {
  type: 'object',
  additionalProperties: false,
  properties: {
    limit: { type: 'integer', minimum: 1, maximum: 50 },
    offset: { type: 'integer', minimum: 0 },
    authorId: { type: 'string', minLength: 1 },
    category: { type: 'string', minLength: 1 },
    tag: { type: 'string', minLength: 1 },
    search: { type: 'string' },
    order: { type: 'string', enum: ['asc', 'desc'] },
    orderBy: {
      type: 'string',
      enum: ['date', 'title', 'menu_order', 'modified'],
    },
    sticky: { type: 'boolean' },
    tagName: { type: 'string', enum: ['ul', 'div'] },
  },
} as const;

export const queryDefinition: BlockTypeDefinition<QueryAttributes> = {
  name: 'core/query',
  title: 'Query Loop',
  category: 'theme',
  description:
    'Dynamic post loop driven by author / category / tag / order filters. Inner blocks become the post-card template.',
  icon:
    '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="4" width="18" height="4" fill="none" stroke="currentColor"/><rect x="3" y="10" width="18" height="4" fill="none" stroke="currentColor"/><rect x="3" y="16" width="18" height="4" fill="none" stroke="currentColor"/></svg>',
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
    return { default: mod.QueryEdit };
  },
  save: async () => {
    const mod = await import('./save.ts');
    return { default: mod.save };
  },
};
