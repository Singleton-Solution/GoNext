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
  isPinnedDialect,
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

// Cross-stack contract: the canonical URI must match the Go-side
// constant in `packages/go/jsonschemautil.Draft2020URI`. A drift here
// would mean a block author's attribute schema validates differently
// in the editor vs the server renderer. The string is duplicated here
// (not imported from anywhere) on purpose so changing it requires
// touching both sides.
describe('SCHEMA_DIALECT', () => {
  it('matches the canonical JSON Schema 2020-12 URL', () => {
    expect(SCHEMA_DIALECT).toBe('https://json-schema.org/draft/2020-12/schema');
  });
});

describe('isPinnedDialect', () => {
  it('accepts the canonical URL', () => {
    expect(isPinnedDialect(SCHEMA_DIALECT)).toBe(true);
  });

  it('trims surrounding whitespace', () => {
    expect(isPinnedDialect(`  ${SCHEMA_DIALECT}\n`)).toBe(true);
  });

  it('rejects historic drafts', () => {
    expect(isPinnedDialect('http://json-schema.org/draft-07/schema#')).toBe(
      false,
    );
    expect(isPinnedDialect('http://json-schema.org/draft-06/schema#')).toBe(
      false,
    );
    expect(isPinnedDialect('https://json-schema.org/draft/2019-09/schema')).toBe(
      false,
    );
  });

  it('rejects non-strings without throwing', () => {
    expect(isPinnedDialect(undefined)).toBe(false);
    expect(isPinnedDialect(null)).toBe(false);
    expect(isPinnedDialect(42)).toBe(false);
    expect(isPinnedDialect({})).toBe(false);
  });
});
