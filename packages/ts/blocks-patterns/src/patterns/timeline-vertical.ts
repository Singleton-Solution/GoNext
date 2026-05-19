/**
 * "Vertical timeline" pattern.
 *
 * A roadmap / history section: heading plus a stack of dated milestone
 * entries. Each entry is a heading (the date) with a paragraph below
 * (the description). A separator block sits between groups so theme
 * CSS has a clean hook for the vertical rule that turns the column
 * into a true timeline.
 */
import type { Pattern } from '../types.ts';

export const timelineVertical: Pattern = {
  id: 'core/timeline-vertical',
  name: 'Vertical timeline',
  category: 'features',
  description:
    'A heading paired with a vertical stack of dated milestone entries.',
  keywords: ['timeline', 'roadmap', 'history', 'changelog', 'milestones'],
  preview: './previews/placeholder.svg',
  blocks: [
    {
      type: 'core/group',
      attributes: { tagName: 'section', layout: 'default' },
      innerBlocks: [
        {
          type: 'core/heading',
          attributes: { content: 'Our journey', level: 2 },
        },
        {
          type: 'core/group',
          attributes: { tagName: 'div', layout: 'default' },
          innerBlocks: [
            {
              type: 'core/heading',
              attributes: { content: 'January 2025', level: 3 },
            },
            {
              type: 'core/paragraph',
              attributes: {
                content:
                  'Project kickoff — first commits to the block-tree document model.',
              },
            },
          ],
        },
        { type: 'core/separator', attributes: { style: 'default' } },
        {
          type: 'core/group',
          attributes: { tagName: 'div', layout: 'default' },
          innerBlocks: [
            {
              type: 'core/heading',
              attributes: { content: 'June 2025', level: 3 },
            },
            {
              type: 'core/paragraph',
              attributes: {
                content:
                  'Public preview — sixteen core blocks, ten patterns, full validation.',
              },
            },
          ],
        },
        { type: 'core/separator', attributes: { style: 'default' } },
        {
          type: 'core/group',
          attributes: { tagName: 'div', layout: 'default' },
          innerBlocks: [
            {
              type: 'core/heading',
              attributes: { content: 'May 2026', level: 3 },
            },
            {
              type: 'core/paragraph',
              attributes: {
                content:
                  'Block transforms, plugin sandbox, RUM beacon, OpenAPI 3.1, public site renderer.',
              },
            },
          ],
        },
      ],
    },
  ],
};
