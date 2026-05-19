/**
 * "Bullet list with icons" pattern.
 *
 * A "what you get" feature checklist — a heading paired with a list of
 * benefit lines. The core list block does not yet ship a per-item icon
 * affordance, so each item leads with a glyph in the string itself; the
 * pattern updates in place once the list block grows an icon attribute.
 */
import type { Pattern } from '../types.ts';

export const bulletListWithIcons: Pattern = {
  id: 'core/bullet-list-with-icons',
  name: 'Bullet list with icons',
  category: 'features',
  description:
    'A heading paired with a benefits list — themes attach checkmark icons via CSS.',
  keywords: ['list', 'checklist', 'features', 'benefits', 'bullets'],
  preview: './previews/placeholder.svg',
  blocks: [
    {
      type: 'core/group',
      attributes: { tagName: 'section', layout: 'default' },
      innerBlocks: [
        {
          type: 'core/heading',
          attributes: { content: 'What you get', level: 2 },
        },
        {
          type: 'core/list',
          attributes: {
            ordered: false,
            values: [
              'Open-source under Apache-2.0 — no surprise upgrades.',
              'A plugin sandbox you can trust on a multi-tenant install.',
              'Themes as plain TypeScript packages with first-class types.',
              'Real RUM, real i18n, real accessibility — out of the box.',
              'CLI + REST + GraphQL surfaces that stay in sync.',
            ],
          },
        },
      ],
    },
  ],
};
