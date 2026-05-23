/**
 * "Team grid" pattern.
 *
 * Three-column "meet the team" grid: each card carries a portrait
 * image, a name (`h3`), a role (paragraph), and a short bio. Wrapped in
 * a `<section>` so theme CSS can target the team region as a landmark.
 */
import type { Pattern } from '../types.ts';

export const teamGrid: Pattern = {
  id: 'core/team-grid',
  name: 'Team grid',
  category: 'features',
  description:
    'Three-column team grid with portraits, names, roles, and short bios.',
  keywords: ['team', 'people', 'about', 'staff', 'roster'],
  preview: './previews/placeholder.svg',
  blocks: [
    {
      type: 'core/group',
      attributes: { tagName: 'section', layout: 'default' },
      innerBlocks: [
        {
          type: 'core/heading',
          attributes: { content: 'Meet the team', level: 2 },
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
                    url: 'https://placehold.co/240x240',
                    alt: 'Avery Chen portrait',
                  },
                },
                {
                  type: 'core/heading',
                  attributes: { content: 'Avery Chen', level: 3 },
                },
                {
                  type: 'core/paragraph',
                  attributes: { content: 'Co-founder & CEO' },
                },
                {
                  type: 'core/paragraph',
                  attributes: {
                    content:
                      'Avery led growth at two prior publishing platforms before starting GoNext.',
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
                    url: 'https://placehold.co/240x240',
                    alt: 'Priya Shah portrait',
                  },
                },
                {
                  type: 'core/heading',
                  attributes: { content: 'Priya Shah', level: 3 },
                },
                {
                  type: 'core/paragraph',
                  attributes: { content: 'Head of Engineering' },
                },
                {
                  type: 'core/paragraph',
                  attributes: {
                    content:
                      'Priya owns the editor stack and led the move to the block-tree document model.',
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
                    url: 'https://placehold.co/240x240',
                    alt: 'Sam Okafor portrait',
                  },
                },
                {
                  type: 'core/heading',
                  attributes: { content: 'Sam Okafor', level: 3 },
                },
                {
                  type: 'core/paragraph',
                  attributes: { content: 'Head of Design' },
                },
                {
                  type: 'core/paragraph',
                  attributes: {
                    content:
                      'Sam ships the system that keeps the GoNext starter themes coherent end to end.',
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
