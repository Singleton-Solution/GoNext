/**
 * Smoke test for the public surface — ensures every documented export
 * exists and is the expected kind of value (class, function, schema,
 * constant).
 */
import { describe, expect, it } from 'vitest';
import * as sdk from './index.ts';

describe('public surface', () => {
  it('exports types and runtime symbols together', () => {
    // Runtime exports we care about for plugin authors.
    const expectedRuntime = [
      'BLOCK_SCHEMA_ID',
      'BLOCK_TREE_SCHEMA_ID',
      'BlockJSONSchema',
      'BlockTreeJSONSchema',
      'SCHEMA_DIALECT',
      'BlockValidator',
      'validateBlockTree',
      'BlockRegistry',
      'DuplicateBlockTypeError',
      'migrateBlock',
      'migrateBlockTree',
    ];
    for (const name of expectedRuntime) {
      expect(sdk).toHaveProperty(name);
      expect((sdk as Record<string, unknown>)[name]).toBeDefined();
    }
  });

  it('BlockRegistry can be instantiated and used end-to-end', () => {
    const reg = new sdk.BlockRegistry();
    reg.register({
      name: 'core/paragraph',
      title: 'Paragraph',
      category: 'text',
      attributes: {
        type: 'object',
        required: ['text'],
        additionalProperties: false,
        properties: { text: { type: 'string' } },
      },
      edit: async () => ({ default: () => null }),
    });
    expect(
      reg.validate([{ type: 'core/paragraph', attributes: { text: 'hi' } }])
        .valid,
    ).toBe(true);
  });

  it('exposes JSON Schema docs that look schema-shaped', () => {
    expect(typeof sdk.BlockJSONSchema).toBe('object');
    expect(sdk.BlockJSONSchema.$id).toBe(sdk.BLOCK_SCHEMA_ID);
    expect(sdk.BlockTreeJSONSchema.$id).toBe(sdk.BLOCK_TREE_SCHEMA_ID);
  });
});
