/**
 * "Latest posts" grid pattern.
 *
 * A heading-introduced three-column grid of post cards. Each card is an
 * image / heading / paragraph triplet — the exact shape themed-up post
 * cards take across the GoNext starter templates. A future query-block
 * iteration will swap the static fixtures for a live query, but the
 * pattern's outer shape stays identical.
 */
import type { Pattern } from '../types.ts';

export const postGrid: Pattern = {
  id: 'core/post-grid',
  name: 'Latest posts grid',
  category: 'posts',
  description:
    'A heading paired with a three-column grid of post cards (image, title, excerpt).',
  keywords: ['posts', 'blog', 'cards', 'grid', 'archive'],
  preview: './previews/placeholder.svg',
  blocks: [
    {
      type: 'core/group',
      attributes: { tagName: 'section', layout: 'default' },
      innerBlocks: [
        {
          type: 'core/heading',
          attributes: { content: 'From the blog', level: 2 },
        },
        {
          type: 'core/columns',
          attributes: { columns: 3, isStackedOnMobile: true },
          innerBlocks: [
            {
              type: 'core/group',
              attributes: { tagName: 'article', layout: 'default' },
              innerBlocks: [
                {
                  type: 'core/image',
                  attributes: {
                    url: 'https://placehold.co/600x400',
                    alt: 'Post cover image',
                  },
                },
                {
                  type: 'core/heading',
                  attributes: { content: 'Shipping the editor', level: 3 },
                },
                {
                  type: 'core/paragraph',
                  attributes: {
                    content:
                      'A look at how we built the block editor in the open over six months.',
                  },
                },
              ],
            },
            {
              type: 'core/group',
              attributes: { tagName: 'article', layout: 'default' },
              innerBlocks: [
                {
                  type: 'core/image',
                  attributes: {
                    url: 'https://placehold.co/600x400',
                    alt: 'Post cover image',
                  },
                },
                {
                  type: 'core/heading',
                  attributes: { content: 'Theme tokens v2', level: 3 },
                },
                {
                  type: 'core/paragraph',
                  attributes: {
                    content:
                      'The next iteration of the design-token contract, with migration notes for plugin authors.',
                  },
                },
              ],
            },
            {
              type: 'core/group',
              attributes: { tagName: 'article', layout: 'default' },
              innerBlocks: [
                {
                  type: 'core/image',
                  attributes: {
                    url: 'https://placehold.co/600x400',
                    alt: 'Post cover image',
                  },
                },
                {
                  type: 'core/heading',
                  attributes: { content: 'Patterns are here', level: 3 },
                },
                {
                  type: 'core/paragraph',
                  attributes: {
                    content:
                      'Operators can now drop curated layouts in with a click — meet the new Patterns tab.',
                  },
                },
              ],
            },
          ],
        },
      ],
    },
  ],
};
