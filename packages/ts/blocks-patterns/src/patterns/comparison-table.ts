/**
 * "Comparison table" pattern.
 *
 * Wraps a `core/table` with thead + body for a classic "us vs them"
 * comparison. The table block treats rows as plain string matrices, so
 * the pattern carries placeholder copy that authors edit in place.
 */
import type { Pattern } from '../types.ts';

export const comparisonTable: Pattern = {
  id: 'core/comparison-table',
  name: 'Comparison table',
  category: 'features',
  description:
    'A heading paired with a feature-by-feature comparison table.',
  keywords: ['compare', 'table', 'matrix', 'features', 'pricing'],
  preview: './previews/placeholder.svg',
  blocks: [
    {
      type: 'core/group',
      attributes: { tagName: 'section', layout: 'default' },
      innerBlocks: [
        {
          type: 'core/heading',
          attributes: { content: 'How we compare', level: 2 },
        },
        {
          type: 'core/table',
          attributes: {
            head: [['Feature', 'GoNext', 'Legacy CMS']],
            body: [
              ['Block-based editor', 'Yes', 'Partial'],
              ['Plugin sandbox', 'Yes', 'No'],
              ['Open licence', 'Apache-2.0', 'Mixed'],
              ['Built-in i18n', 'Yes', 'Add-on'],
            ],
            caption: 'Feature parity vs. a representative legacy CMS.',
            style: { stripes: true, borders: true },
          },
        },
      ],
    },
  ],
};
