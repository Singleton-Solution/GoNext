/**
 * Tests for the block deprecation pipeline editor wrapper (issue #198).
 */
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import {
  BlockRegistry,
  type Block,
  type BlockTypeDefinition,
} from '@gonext/blocks-sdk';
import {
  auditDeprecations,
  runDeprecations,
  warnDeprecatedBlocks,
} from './deprecation.ts';

const v0Schema = {
  type: 'object',
  required: ['recurring'],
  additionalProperties: false,
  properties: { recurring: { type: 'boolean' } },
};

const v1Schema = {
  type: 'object',
  required: ['cycle'],
  additionalProperties: false,
  properties: { cycle: { type: 'string' } },
};

const pricingDef: BlockTypeDefinition = {
  name: 'wp-pricing/pricing-table',
  title: 'Pricing',
  category: 'widgets',
  version: 3,
  attributes: {
    type: 'object',
    required: ['period'],
    additionalProperties: false,
    properties: { period: { type: 'string', enum: ['month', 'year'] } },
  },
  edit: async () => ({ default: () => null }),
  deprecated: [
    {
      version: 2,
      attributes: v1Schema,
      migrate: (old: any) => ({
        attributes: { period: old.cycle === 'yearly' ? 'year' : 'month' },
      }),
    },
    {
      version: 1,
      attributes: v0Schema,
      migrate: (old: any) => ({
        attributes: { cycle: old.recurring === true ? 'monthly' : 'yearly' },
      }),
    },
  ],
};

function buildRegistry(): BlockRegistry {
  const r = new BlockRegistry();
  r.register(pricingDef);
  return r;
}

describe('runDeprecations', () => {
  it('migrates blocks through their deprecation chain', () => {
    const registry = buildRegistry();
    const tree: Block[] = [
      {
        type: 'wp-pricing/pricing-table',
        attributes: { recurring: true },
      },
    ];
    const out = runDeprecations(tree, registry);
    expect(out[0]!.attributes).toEqual({ period: 'month' });
  });

  it('passes current blocks through unchanged', () => {
    const registry = buildRegistry();
    const tree: Block[] = [
      {
        type: 'wp-pricing/pricing-table',
        attributes: { period: 'month' },
      },
    ];
    expect(runDeprecations(tree, registry)).toEqual(tree);
  });

  it('passes unknown block types through unchanged', () => {
    const registry = buildRegistry();
    const tree: Block[] = [
      { type: 'unknown/foo', attributes: { x: 1 } },
    ];
    expect(runDeprecations(tree, registry)).toEqual(tree);
  });
});

describe('auditDeprecations', () => {
  it('reports a deprecated block with from/to versions', () => {
    const registry = buildRegistry();
    const tree: Block[] = [
      { type: 'wp-pricing/pricing-table', attributes: { cycle: 'yearly' } },
    ];
    const findings = auditDeprecations(tree, registry);
    expect(findings).toHaveLength(1);
    expect(findings[0]!.fromVersion).toBe(2);
    expect(findings[0]!.toVersion).toBe(3);
    expect(findings[0]!.path).toEqual([0]);
  });

  it('does not mutate the tree', () => {
    const registry = buildRegistry();
    const tree: Block[] = [
      { type: 'wp-pricing/pricing-table', attributes: { recurring: true } },
    ];
    const original = JSON.parse(JSON.stringify(tree));
    auditDeprecations(tree, registry);
    expect(tree).toEqual(original);
  });

  it('walks into innerBlocks', () => {
    const registry = buildRegistry();
    const tree: Block[] = [
      {
        type: 'core/columns',
        attributes: {},
        innerBlocks: [
          {
            type: 'wp-pricing/pricing-table',
            attributes: { recurring: true },
          },
        ],
      },
    ];
    const findings = auditDeprecations(tree, registry);
    expect(findings).toHaveLength(1);
    expect(findings[0]!.path).toEqual([0, 0]);
  });

  it('returns empty for a current tree', () => {
    const registry = buildRegistry();
    const tree: Block[] = [
      { type: 'wp-pricing/pricing-table', attributes: { period: 'month' } },
    ];
    expect(auditDeprecations(tree, registry)).toEqual([]);
  });
});

describe('warnDeprecatedBlocks', () => {
  // Use `any` for the spy type so we don't lose type-soundness in
  // the rest of the file just to satisfy vi.spyOn's parametric signature.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  let warnSpy: any;
  const originalEnv = process.env.NODE_ENV;

  beforeEach(() => {
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
  });

  afterEach(() => {
    warnSpy.mockRestore();
    process.env.NODE_ENV = originalEnv;
  });

  it('logs in development', () => {
    process.env.NODE_ENV = 'development';
    const registry = buildRegistry();
    warnDeprecatedBlocks(
      [{ type: 'wp-pricing/pricing-table', attributes: { recurring: true } }],
      registry,
    );
    expect(warnSpy).toHaveBeenCalled();
  });

  it('is silent in production', () => {
    process.env.NODE_ENV = 'production';
    const registry = buildRegistry();
    warnDeprecatedBlocks(
      [{ type: 'wp-pricing/pricing-table', attributes: { recurring: true } }],
      registry,
    );
    expect(warnSpy).not.toHaveBeenCalled();
  });

  it('is silent when there are no findings', () => {
    process.env.NODE_ENV = 'development';
    const registry = buildRegistry();
    warnDeprecatedBlocks(
      [{ type: 'wp-pricing/pricing-table', attributes: { period: 'month' } }],
      registry,
    );
    expect(warnSpy).not.toHaveBeenCalled();
  });
});
