/**
 * Registry tests — register / get / list / has / unregister / clear and the
 * duplicate-detection contract.
 */
import { beforeEach, describe, expect, it } from 'vitest';
import {
  BlockRegistry,
  DuplicateBlockTypeError,
} from './registry.ts';
import type { BlockTypeDefinition } from './types.ts';

const paragraph: BlockTypeDefinition = {
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
};

const heading: BlockTypeDefinition = {
  name: 'core/heading',
  title: 'Heading',
  category: 'text',
  attributes: {
    type: 'object',
    required: ['level', 'text'],
    additionalProperties: false,
    properties: {
      level: { type: 'integer', minimum: 1, maximum: 6 },
      text: { type: 'string' },
    },
  },
  edit: async () => ({ default: () => null }),
};

describe('BlockRegistry', () => {
  let registry: BlockRegistry;

  beforeEach(() => {
    registry = new BlockRegistry();
  });

  it('starts empty', () => {
    expect(registry.list()).toEqual([]);
    expect(registry.get('core/paragraph')).toBeUndefined();
    expect(registry.has('core/paragraph')).toBe(false);
  });

  it('registers and retrieves a block type', () => {
    registry.register(paragraph);
    expect(registry.get('core/paragraph')).toBe(paragraph);
    expect(registry.has('core/paragraph')).toBe(true);
    expect(registry.list()).toEqual([paragraph]);
  });

  it('lists in registration order', () => {
    registry.register(paragraph);
    registry.register(heading);
    expect(registry.list().map((d) => d.name)).toEqual([
      'core/paragraph',
      'core/heading',
    ]);
  });

  it('throws DuplicateBlockTypeError on a collision', () => {
    registry.register(paragraph);
    expect(() => registry.register(paragraph)).toThrowError(
      DuplicateBlockTypeError,
    );
    try {
      registry.register(paragraph);
    } catch (err) {
      expect(err).toBeInstanceOf(DuplicateBlockTypeError);
      if (err instanceof DuplicateBlockTypeError) {
        expect(err.blockType).toBe('core/paragraph');
        expect(err.name).toBe('DuplicateBlockTypeError');
      }
    }
  });

  it('allows replacement when replace=true', () => {
    registry.register(paragraph);
    const replacement: BlockTypeDefinition = {
      ...paragraph,
      title: 'Paragraph (HMR)',
    };
    registry.register(replacement, { replace: true });
    expect(registry.get('core/paragraph')?.title).toBe('Paragraph (HMR)');
  });

  it('unregister returns false when there was nothing to remove', () => {
    expect(registry.unregister('core/never-registered')).toBe(false);
  });

  it('unregister returns true and removes the entry', () => {
    registry.register(paragraph);
    expect(registry.unregister('core/paragraph')).toBe(true);
    expect(registry.has('core/paragraph')).toBe(false);
  });

  it('clear removes every registration', () => {
    registry.register(paragraph);
    registry.register(heading);
    registry.clear();
    expect(registry.list()).toEqual([]);
  });

  describe('validate()', () => {
    it('passes for a tree of registered types with valid attributes', () => {
      registry.register(paragraph);
      registry.register(heading);
      const out = registry.validate([
        { type: 'core/heading', attributes: { level: 1, text: 'Hi' } },
        { type: 'core/paragraph', attributes: { text: 'A paragraph.' } },
      ]);
      expect(out.valid).toBe(true);
      expect(out.errors).toEqual([]);
    });

    it('flags unknown block types', () => {
      registry.register(paragraph);
      const out = registry.validate([
        { type: 'unknown/widget', attributes: {} },
      ]);
      expect(out.valid).toBe(false);
      expect(out.errors[0]?.code).toBe('unknown-type');
    });

    it('flags attribute errors with JSON-pointer paths', () => {
      registry.register(heading);
      const out = registry.validate([
        { type: 'core/heading', attributes: { level: 99, text: 'bad' } },
      ]);
      expect(out.valid).toBe(false);
      expect(out.errors[0]).toMatchObject({
        code: 'attributes',
        blockType: 'core/heading',
      });
      expect(out.errors[0]?.path.startsWith('/0/attributes')).toBe(true);
    });

    it('re-registration invalidates the cached attribute validator', () => {
      registry.register(paragraph);

      // Initial: { text } is required, anything else rejected.
      expect(
        registry.validate([
          { type: 'core/paragraph', attributes: { text: 'a', extra: 1 } },
        ]).valid,
      ).toBe(false);

      // Replace with a loose schema; the previously cached compiled
      // validator must NOT be reused.
      registry.register(
        {
          ...paragraph,
          attributes: {
            type: 'object',
            required: ['text'],
            additionalProperties: true,
            properties: { text: { type: 'string' } },
          },
        },
        { replace: true },
      );

      expect(
        registry.validate([
          { type: 'core/paragraph', attributes: { text: 'a', extra: 1 } },
        ]).valid,
      ).toBe(true);
    });
  });
});
