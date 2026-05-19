/**
 * "Pricing — three tiers" pattern.
 *
 * Three side-by-side pricing tiers (Starter / Pro / Enterprise). Each
 * tier is a column hosting a heading, a price paragraph, a feature list,
 * and a CTA button. The structure mirrors the canonical SaaS pricing
 * page; theme CSS can highlight the middle column via the standard
 * `is-style-*` modifier on the inner group.
 */
import type { Pattern } from '../types.ts';

export const pricingThreeTier: Pattern = {
  id: 'core/pricing-three-tier',
  name: 'Three-tier pricing',
  category: 'pricing',
  description:
    'Side-by-side pricing tiers with feature lists and a CTA per plan.',
  keywords: ['pricing', 'plans', 'tiers', 'subscription'],
  preview: './previews/placeholder.svg',
  blocks: [
    {
      type: 'core/columns',
      attributes: {
        columns: 3,
        isStackedOnMobile: true,
      },
      innerBlocks: [
        {
          type: 'core/group',
          attributes: { tagName: 'div', layout: 'default' },
          innerBlocks: [
            {
              type: 'core/heading',
              attributes: { content: 'Starter', level: 3, align: 'center' },
            },
            {
              type: 'core/paragraph',
              attributes: { content: '$0 / month', align: 'center' },
            },
            {
              type: 'core/list',
              attributes: {
                ordered: false,
                values: ['1 site', 'Community support', 'Core blocks'],
              },
            },
            {
              type: 'core/button',
              attributes: {
                text: 'Start free',
                url: '#starter',
                style: 'outline',
                align: 'center',
              },
            },
          ],
        },
        {
          type: 'core/group',
          attributes: { tagName: 'div', layout: 'default' },
          innerBlocks: [
            {
              type: 'core/heading',
              attributes: { content: 'Pro', level: 3, align: 'center' },
            },
            {
              type: 'core/paragraph',
              attributes: { content: '$19 / month', align: 'center' },
            },
            {
              type: 'core/list',
              attributes: {
                ordered: false,
                values: [
                  '5 sites',
                  'Email support',
                  'Premium plugins',
                  'Custom themes',
                ],
              },
            },
            {
              type: 'core/button',
              attributes: {
                text: 'Upgrade',
                url: '#pro',
                style: 'fill',
                align: 'center',
              },
            },
          ],
        },
        {
          type: 'core/group',
          attributes: { tagName: 'div', layout: 'default' },
          innerBlocks: [
            {
              type: 'core/heading',
              attributes: {
                content: 'Enterprise',
                level: 3,
                align: 'center',
              },
            },
            {
              type: 'core/paragraph',
              attributes: { content: 'Contact us', align: 'center' },
            },
            {
              type: 'core/list',
              attributes: {
                ordered: false,
                values: [
                  'Unlimited sites',
                  '24/7 support',
                  'SLA + audit logs',
                  'SSO + SCIM',
                ],
              },
            },
            {
              type: 'core/button',
              attributes: {
                text: 'Contact sales',
                url: '#enterprise',
                style: 'outline',
                align: 'center',
              },
            },
          ],
        },
      ],
    },
  ],
};
