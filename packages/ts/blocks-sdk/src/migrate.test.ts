/**
 * Migration tests — cover the four documented contracts:
 *  - No deprecated: a no-op
 *  - Single-step deprecation: old → new
 *  - Deprecation chain (multiple steps): older → ... → current
 *  - Eligibility override via `isEligible`
 *  - Idempotency: re-running migration on a current block is a no-op
 *  - Recursive tree migration preserves structure and migrates nested
 *    blocks
 */
import { describe, expect, it } from 'vitest';
import { migrateBlock, migrateBlockTree } from './migrate.ts';
import type {
  Block,
  BlockDeprecation,
  BlockTypeDefinition,
} from './types.ts';

// Current shape: { period: "month" | "year", currency: string }.
// v1 shape:     { cycle: "monthly" | "yearly", currency: string }
// v0 shape:     { recurring: boolean, currency: string }  // boolean cycle
const v0Schema = {
  type: 'object',
  required: ['recurring', 'currency'],
  additionalProperties: false,
  properties: {
    recurring: { type: 'boolean' },
    currency: { type: 'string' },
  },
};

const v1Schema = {
  type: 'object',
  required: ['cycle', 'currency'],
  additionalProperties: false,
  properties: {
    cycle: { type: 'string', enum: ['monthly', 'yearly'] },
    currency: { type: 'string' },
  },
};

const pricingDef: BlockTypeDefinition = {
  name: 'wp-pricing/pricing-table',
  title: 'Pricing Table',
  category: 'widgets',
  attributes: {
    type: 'object',
    required: ['period', 'currency'],
    additionalProperties: false,
    properties: {
      period: { type: 'string', enum: ['month', 'year'] },
      currency: { type: 'string' },
    },
  },
  edit: async () => ({ default: () => null }),
  deprecated: [
    // v1 → current: rename `cycle` → `period`, value mapping.
    {
      attributes: v1Schema,
      migrate: (old) => ({
        attributes: {
          period: old.cycle === 'yearly' ? 'year' : 'month',
          currency: old.currency,
        },
      }),
    },
    // v0 → v1: translate boolean recurring into `cycle`.
    {
      attributes: v0Schema,
      migrate: (old) => ({
        attributes: {
          cycle: old.recurring === true ? 'monthly' : 'yearly',
          currency: old.currency,
        },
      }),
    },
  ],
};

describe('migrateBlock — no-op cases', () => {
  it('returns input unchanged when deprecated is undefined', () => {
    const def: BlockTypeDefinition = {
      ...pricingDef,
      deprecated: undefined,
    };
    const block: Block = {
      type: 'wp-pricing/pricing-table',
      attributes: { cycle: 'monthly', currency: 'USD' },
    };
    expect(migrateBlock(block, def)).toBe(block);
  });

  it('returns input unchanged when deprecated is empty', () => {
    const def: BlockTypeDefinition = { ...pricingDef, deprecated: [] };
    const block: Block = {
      type: 'wp-pricing/pricing-table',
      attributes: { period: 'month', currency: 'USD' },
    };
    expect(migrateBlock(block, def)).toBe(block);
  });

  it('returns input unchanged when no step is eligible', () => {
    const block: Block = {
      type: 'wp-pricing/pricing-table',
      attributes: { period: 'year', currency: 'EUR' },
    };
    expect(migrateBlock(block, pricingDef)).toBe(block);
  });
});

describe('migrateBlock — single step', () => {
  it('translates v1 to current', () => {
    const block: Block = {
      type: 'wp-pricing/pricing-table',
      attributes: { cycle: 'yearly', currency: 'USD' },
    };
    const migrated = migrateBlock(block, pricingDef);
    expect(migrated.attributes).toEqual({ period: 'year', currency: 'USD' });
    expect(migrated.type).toBe('wp-pricing/pricing-table');
  });

  it('preserves clientId across migration', () => {
    const block: Block = {
      type: 'wp-pricing/pricing-table',
      attributes: { cycle: 'monthly', currency: 'GBP' },
      clientId: 'abc-123',
    };
    const migrated = migrateBlock(block, pricingDef);
    expect(migrated.clientId).toBe('abc-123');
  });
});

describe('migrateBlock — chain', () => {
  it('walks v0 → v1 → current in a single call', () => {
    const block: Block = {
      type: 'wp-pricing/pricing-table',
      attributes: { recurring: true, currency: 'USD' },
    };
    const migrated = migrateBlock(block, pricingDef);
    // recurring:true → cycle:'monthly' → period:'month'
    expect(migrated.attributes).toEqual({ period: 'month', currency: 'USD' });
  });

  it('handles a chain that requires the inner-blocks rewrite path', () => {
    const child: Block = {
      type: 'core/text',
      attributes: { value: 'inner' },
    };
    const def: BlockTypeDefinition = {
      name: 'core/container',
      title: 'Container',
      category: 'design',
      attributes: {
        type: 'object',
        required: ['layout'],
        additionalProperties: false,
        properties: {
          layout: { type: 'string', enum: ['flex', 'grid'] },
        },
      },
      edit: async () => ({ default: () => null }),
      deprecated: [
        {
          attributes: {
            type: 'object',
            required: ['orientation'],
            additionalProperties: false,
            properties: {
              orientation: { type: 'string', enum: ['horizontal', 'vertical'] },
            },
          },
          migrate: (old, oldInner) => ({
            attributes: {
              layout: old.orientation === 'horizontal' ? 'flex' : 'grid',
            },
            // Simulate a structural rewrite: drop the first inner block.
            innerBlocks: oldInner.slice(1),
          }),
        },
      ],
    };
    const block: Block = {
      type: 'core/container',
      attributes: { orientation: 'horizontal' },
      innerBlocks: [child, { ...child, attributes: { value: 'second' } }],
    };
    const migrated = migrateBlock(block, def);
    expect(migrated.attributes).toEqual({ layout: 'flex' });
    expect(migrated.innerBlocks).toHaveLength(1);
    expect(migrated.innerBlocks?.[0]?.attributes).toEqual({ value: 'second' });
  });

  it('caps runaway chains at MAX_MIGRATION_STEPS', () => {
    // Construct a deprecation step that produces output that re-matches the
    // same schema. Without the cap, `migrateBlock` would loop forever.
    const looper: BlockDeprecation = {
      attributes: {
        type: 'object',
        required: ['n'],
        additionalProperties: false,
        properties: { n: { type: 'integer' } },
      },
      migrate: (old) => ({
        attributes: { n: ((old.n as number) ?? 0) + 1 },
      }),
    };
    const def: BlockTypeDefinition = {
      name: 'test/looper',
      title: 'Looper',
      category: 'custom',
      attributes: {
        type: 'object',
        required: ['done'],
        additionalProperties: false,
        properties: { done: { type: 'boolean' } },
      },
      edit: async () => ({ default: () => null }),
      deprecated: [looper],
    };
    const block: Block = { type: 'test/looper', attributes: { n: 0 } };
    const migrated = migrateBlock(block, def);
    // We don't care about the exact final value, only that we DIDN'T hang.
    expect(typeof (migrated.attributes as { n: number }).n).toBe('number');
  });
});

describe('migrateBlock — isEligible', () => {
  it('uses isEligible when provided, bypassing schema match', () => {
    let calls = 0;
    const def: BlockTypeDefinition = {
      name: 'test/eligible',
      title: 'Eligible',
      category: 'custom',
      attributes: {
        type: 'object',
        required: ['v'],
        additionalProperties: false,
        properties: { v: { type: 'integer' } },
      },
      edit: async () => ({ default: () => null }),
      deprecated: [
        {
          attributes: {
            // Intentionally bogus schema; isEligible should win.
            type: 'object',
            properties: {},
          },
          isEligible: (attrs) => {
            calls++;
            return (attrs as { legacy?: boolean }).legacy === true;
          },
          migrate: (old) => ({
            attributes: { v: (old.value as number) ?? 1 },
          }),
        },
      ],
    };
    const block: Block = {
      type: 'test/eligible',
      attributes: { legacy: true, value: 42 },
    };
    const migrated = migrateBlock(block, def);
    expect(migrated.attributes).toEqual({ v: 42 });
    expect(calls).toBeGreaterThan(0);
  });

  it('skips steps whose isEligible returns false', () => {
    const def: BlockTypeDefinition = {
      name: 'test/skip',
      title: 'Skip',
      category: 'custom',
      attributes: {
        type: 'object',
        required: ['v'],
        additionalProperties: false,
        properties: { v: { type: 'integer' } },
      },
      edit: async () => ({ default: () => null }),
      deprecated: [
        {
          attributes: { type: 'object' },
          isEligible: () => false,
          migrate: () => ({ attributes: { v: -1 } }),
        },
      ],
    };
    const block: Block = { type: 'test/skip', attributes: { v: 1 } };
    expect(migrateBlock(block, def)).toBe(block);
  });
});

describe('migrateBlockTree', () => {
  it('recurses into inner blocks', () => {
    const lookup = (name: string): BlockTypeDefinition | undefined =>
      name === pricingDef.name ? pricingDef : undefined;

    const tree: Block[] = [
      {
        type: 'core/columns',
        attributes: { count: 2 },
        innerBlocks: [
          {
            type: 'wp-pricing/pricing-table',
            attributes: { cycle: 'monthly', currency: 'USD' },
          },
        ],
      },
    ];
    const migrated = migrateBlockTree(tree, lookup);
    expect(migrated[0]?.innerBlocks?.[0]?.attributes).toEqual({
      period: 'month',
      currency: 'USD',
    });
  });

  it('passes unknown block types through unchanged', () => {
    const lookup = (): BlockTypeDefinition | undefined => undefined;
    const tree: Block[] = [
      {
        type: 'unknown/widget',
        attributes: { v: 1 },
      },
    ];
    const migrated = migrateBlockTree(tree, lookup);
    expect(migrated[0]).toEqual(tree[0]);
  });

  it('leaves a tree without inner blocks alone aside from migration', () => {
    const lookup = (name: string): BlockTypeDefinition | undefined =>
      name === pricingDef.name ? pricingDef : undefined;
    const tree: Block[] = [
      {
        type: 'wp-pricing/pricing-table',
        attributes: { period: 'year', currency: 'EUR' },
      },
    ];
    const migrated = migrateBlockTree(tree, lookup);
    expect(migrated[0]?.attributes).toEqual({ period: 'year', currency: 'EUR' });
  });
});
