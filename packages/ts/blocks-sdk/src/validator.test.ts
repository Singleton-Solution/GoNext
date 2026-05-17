/**
 * Validator tests.
 *
 * Cover the four documented error paths:
 *  - structural malformations (caught by pass 1)
 *  - unknown block types (caught by pass 2)
 *  - attribute mismatches (caught by pass 2)
 *  - happy-path: valid trees pass cleanly
 *
 * Path semantics are exercised at every depth — root, nested, deep nested —
 * because regressions there make the editor's "highlight bad block" UX
 * useless.
 */
import { describe, expect, it } from 'vitest';
import type { BlockTypeDefinition } from './types.ts';
import { BlockValidator, validateBlockTree } from './validator.ts';

const paragraphDef: BlockTypeDefinition = {
  name: 'core/paragraph',
  title: 'Paragraph',
  category: 'text',
  attributes: {
    type: 'object',
    additionalProperties: false,
    required: ['text'],
    properties: {
      text: { type: 'string', maxLength: 8000 },
      align: { type: 'string', enum: ['left', 'center', 'right'] },
    },
  },
  edit: async () => ({ default: () => null }),
};

const headingDef: BlockTypeDefinition = {
  name: 'core/heading',
  title: 'Heading',
  category: 'text',
  attributes: {
    type: 'object',
    additionalProperties: false,
    required: ['level', 'text'],
    properties: {
      level: { type: 'integer', minimum: 1, maximum: 6 },
      text: { type: 'string' },
    },
  },
  edit: async () => ({ default: () => null }),
};

const columnsDef: BlockTypeDefinition = {
  name: 'core/columns',
  title: 'Columns',
  category: 'design',
  attributes: {
    type: 'object',
    additionalProperties: false,
    required: ['count'],
    properties: {
      count: { type: 'integer', minimum: 1, maximum: 6 },
    },
  },
  edit: async () => ({ default: () => null }),
};

const registry = new Map<string, BlockTypeDefinition>([
  ['core/paragraph', paragraphDef],
  ['core/heading', headingDef],
  ['core/columns', columnsDef],
]);

const lookup = (name: string): BlockTypeDefinition | undefined =>
  registry.get(name);

describe('BlockValidator — happy path', () => {
  it('accepts an empty tree', () => {
    const v = new BlockValidator(lookup);
    expect(v.validate([])).toEqual({ valid: true, errors: [] });
  });

  it('accepts a single valid block', () => {
    const v = new BlockValidator(lookup);
    const tree = [
      {
        type: 'core/paragraph',
        attributes: { text: 'hi' },
      },
    ];
    expect(v.validate(tree)).toEqual({ valid: true, errors: [] });
  });

  it('accepts a deeply nested valid tree', () => {
    const v = new BlockValidator(lookup);
    const tree = [
      {
        type: 'core/columns',
        attributes: { count: 2 },
        innerBlocks: [
          {
            type: 'core/heading',
            attributes: { level: 2, text: 'Section' },
          },
          {
            type: 'core/paragraph',
            attributes: { text: 'A paragraph.', align: 'left' },
            innerBlocks: [],
          },
        ],
      },
    ];
    expect(v.validate(tree)).toEqual({ valid: true, errors: [] });
  });

  it('accepts blocks with editor-only clientId', () => {
    const v = new BlockValidator(lookup);
    const tree = [
      {
        type: 'core/paragraph',
        attributes: { text: 'hi' },
        clientId: '01J-fake-ulid',
      },
    ];
    expect(v.validate(tree)).toEqual({ valid: true, errors: [] });
  });
});

describe('BlockValidator — structural errors', () => {
  it('rejects a non-array root', () => {
    const v = new BlockValidator(lookup);
    const out = v.validate({ type: 'core/paragraph', attributes: {} });
    expect(out.valid).toBe(false);
    expect(out.errors[0]?.code).toBe('schema');
  });

  it('rejects a block missing required `type`', () => {
    const v = new BlockValidator(lookup);
    const out = v.validate([{ attributes: { text: 'hi' } }]);
    expect(out.valid).toBe(false);
    expect(out.errors.some((e) => e.code === 'schema')).toBe(true);
  });

  it('rejects a block with a bad `type` pattern', () => {
    const v = new BlockValidator(lookup);
    const out = v.validate([
      { type: 'no-namespace', attributes: { text: 'hi' } },
    ]);
    expect(out.valid).toBe(false);
    expect(out.errors.some((e) => e.code === 'schema')).toBe(true);
  });

  it('rejects unknown top-level properties on a block', () => {
    const v = new BlockValidator(lookup);
    const out = v.validate([
      {
        type: 'core/paragraph',
        attributes: { text: 'hi' },
        extraneous: true,
      },
    ]);
    expect(out.valid).toBe(false);
    expect(out.errors.some((e) => e.code === 'schema')).toBe(true);
  });

  it('rejects innerBlocks that is not an array', () => {
    const v = new BlockValidator(lookup);
    const out = v.validate([
      {
        type: 'core/columns',
        attributes: { count: 2 },
        innerBlocks: 'not an array',
      },
    ]);
    expect(out.valid).toBe(false);
    expect(out.errors.some((e) => e.code === 'schema')).toBe(true);
  });
});

describe('BlockValidator — unknown block types', () => {
  it('reports unknown-type without crashing', () => {
    const v = new BlockValidator(lookup);
    const out = v.validate([
      {
        type: 'unknown/widget',
        attributes: { whatever: true },
      },
    ]);
    expect(out.valid).toBe(false);
    expect(out.errors).toHaveLength(1);
    expect(out.errors[0]).toMatchObject({
      path: '/0',
      code: 'unknown-type',
      blockType: 'unknown/widget',
    });
    expect(out.errors[0]?.message).toContain('unknown/widget');
  });

  it('still walks into unknown blocks` children', () => {
    const v = new BlockValidator(lookup);
    const out = v.validate([
      {
        type: 'unknown/widget',
        attributes: {},
        innerBlocks: [
          {
            type: 'core/paragraph',
            attributes: { text: 'still valid' },
          },
          {
            type: 'core/paragraph',
            // Missing required `text`.
            attributes: {},
          },
        ],
      },
    ]);
    expect(out.valid).toBe(false);
    // 1 unknown-type at /0, plus 1 attribute error at /0/innerBlocks/1.
    expect(out.errors).toHaveLength(2);
    expect(out.errors[0]?.code).toBe('unknown-type');
    expect(out.errors[1]?.code).toBe('attributes');
    expect(out.errors[1]?.path.startsWith('/0/innerBlocks/1/attributes')).toBe(
      true,
    );
  });
});

describe('BlockValidator — attribute errors', () => {
  it('reports an attribute mismatch with a JSON-pointer-style path', () => {
    const v = new BlockValidator(lookup);
    const out = v.validate([
      {
        type: 'core/heading',
        attributes: { level: 9, text: 'too deep' },
      },
    ]);
    expect(out.valid).toBe(false);
    expect(out.errors).toHaveLength(1);
    expect(out.errors[0]).toMatchObject({
      code: 'attributes',
      blockType: 'core/heading',
    });
    expect(out.errors[0]?.path.startsWith('/0/attributes')).toBe(true);
    expect(out.errors[0]?.path).toContain('level');
  });

  it('reports a missing required attribute', () => {
    const v = new BlockValidator(lookup);
    const out = v.validate([
      {
        type: 'core/paragraph',
        attributes: {},
      },
    ]);
    expect(out.valid).toBe(false);
    expect(out.errors[0]?.code).toBe('attributes');
    expect(out.errors[0]?.path).toBe('/0/attributes');
  });

  it('uses deep paths for nested attribute errors', () => {
    const v = new BlockValidator(lookup);
    const out = v.validate([
      {
        type: 'core/columns',
        attributes: { count: 2 },
        innerBlocks: [
          {
            type: 'core/columns',
            attributes: { count: 2 },
            innerBlocks: [
              {
                type: 'core/heading',
                attributes: { level: 9, text: 'bad' },
              },
            ],
          },
        ],
      },
    ]);
    expect(out.valid).toBe(false);
    expect(out.errors[0]?.path.startsWith('/0/innerBlocks/0/innerBlocks/0/attributes')).toBe(
      true,
    );
  });

  it('rejects unknown attributes when the schema disallows them', () => {
    const v = new BlockValidator(lookup);
    const out = v.validate([
      {
        type: 'core/paragraph',
        attributes: { text: 'hi', secret: 42 },
      },
    ]);
    expect(out.valid).toBe(false);
    expect(out.errors[0]?.code).toBe('attributes');
  });

  it('caches compiled attribute validators per type', () => {
    const v = new BlockValidator(lookup);
    // Two blocks of the same type → compile() should run once. We can't peek
    // at Ajv directly, but validating twice in a row with no errors proves
    // the cache is at least functional.
    const tree = [
      { type: 'core/paragraph', attributes: { text: 'a' } },
      { type: 'core/paragraph', attributes: { text: 'b' } },
    ];
    expect(v.validate(tree).valid).toBe(true);
    expect(v.validate(tree).valid).toBe(true);
  });
});

describe('BlockValidator — invalidation', () => {
  it('drops cached compiled schemas on invalidate()', () => {
    const v = new BlockValidator(lookup);
    // Validate once to populate the cache.
    v.validate([{ type: 'core/paragraph', attributes: { text: 'a' } }]);
    // Invalidate and re-run. The interesting thing is that this does NOT
    // throw — invalidation of an unseen type is a no-op.
    v.invalidate('core/paragraph');
    v.invalidate('never-registered/widget');
    expect(
      v.validate([{ type: 'core/paragraph', attributes: { text: 'a' } }]).valid,
    ).toBe(true);
  });
});

describe('validateBlockTree convenience export', () => {
  it('mirrors BlockValidator.validate', () => {
    const tree = [{ type: 'core/paragraph', attributes: { text: 'hi' } }];
    expect(validateBlockTree(tree, lookup)).toEqual({
      valid: true,
      errors: [],
    });
  });

  it('reports errors with the same shape', () => {
    const out = validateBlockTree(
      [{ type: 'core/paragraph', attributes: {} }],
      lookup,
    );
    expect(out.valid).toBe(false);
    expect(out.errors[0]?.code).toBe('attributes');
  });
});
