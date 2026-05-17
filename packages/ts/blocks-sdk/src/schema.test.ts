/**
 * Sanity checks on the exported JSON Schema documents. These are NOT
 * exhaustive — `validator.test.ts` does the heavy lifting. We just verify
 * the shape, ids, and references are wired correctly.
 */
import { describe, expect, it } from 'vitest';
import {
  BLOCK_SCHEMA_ID,
  BLOCK_TREE_SCHEMA_ID,
  BlockJSONSchema,
  BlockTreeJSONSchema,
  SCHEMA_DIALECT,
} from './schema.ts';

describe('BlockJSONSchema', () => {
  it('declares the JSON Schema 2020-12 dialect', () => {
    expect(BlockJSONSchema.$schema).toBe(SCHEMA_DIALECT);
  });

  it('has the documented $id', () => {
    expect(BlockJSONSchema.$id).toBe(BLOCK_SCHEMA_ID);
  });

  it('requires `type` and `attributes`', () => {
    expect(BlockJSONSchema.required).toEqual(['type', 'attributes']);
  });

  it('forbids unknown top-level properties', () => {
    expect(BlockJSONSchema.additionalProperties).toBe(false);
  });

  it('recurses into innerBlocks via $ref', () => {
    const inner = BlockJSONSchema.properties.innerBlocks;
    expect(inner.type).toBe('array');
    expect(inner.items).toEqual({ $ref: BLOCK_SCHEMA_ID });
  });

  it('constrains block names with a namespaced pattern', () => {
    const pattern = BlockJSONSchema.properties.type.pattern;
    expect(new RegExp(pattern).test('core/paragraph')).toBe(true);
    expect(new RegExp(pattern).test('wp-seo/breadcrumbs')).toBe(true);
    expect(new RegExp(pattern).test('no-namespace')).toBe(false);
    expect(new RegExp(pattern).test('UPPER/case')).toBe(false);
  });
});

describe('BlockTreeJSONSchema', () => {
  it('declares the JSON Schema 2020-12 dialect', () => {
    expect(BlockTreeJSONSchema.$schema).toBe(SCHEMA_DIALECT);
  });

  it('has the documented $id', () => {
    expect(BlockTreeJSONSchema.$id).toBe(BLOCK_TREE_SCHEMA_ID);
  });

  it('is an array of blocks', () => {
    expect(BlockTreeJSONSchema.type).toBe('array');
    expect(BlockTreeJSONSchema.items).toEqual({ $ref: BLOCK_SCHEMA_ID });
  });
});
