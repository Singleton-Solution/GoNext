/**
 * JSON Schema 2020-12 documents describing the block tree shape.
 *
 * These are the schemas the validator uses for the FIRST pass — structural
 * checks ("does this look like a block tree at all?"). The SECOND pass is
 * the per-block attribute schema lookup against the registry; that lives in
 * `validator.ts`.
 *
 * We export both:
 *  - `BlockJSONSchema`     — validates a single `Block`
 *  - `BlockTreeJSONSchema` — validates a `BlockTree` (root array)
 *
 * Keeping them as plain JS objects (not generated, not derived from the TS
 * types) is intentional: the types and the schema describe the same thing
 * but at different layers, and we want both to round-trip cleanly through
 * code review.
 */

/**
 * The JSON Schema 2020-12 dialect identifier we declare on every schema.
 * Ajv resolves this via the default meta-schemas; no network fetch occurs.
 */
export const SCHEMA_DIALECT = 'https://json-schema.org/draft/2020-12/schema';

/** Sentinel `$id` for the single-block schema; used to wire `$ref`s. */
export const BLOCK_SCHEMA_ID = 'https://gonext.dev/schemas/block.json';

/** Sentinel `$id` for the root array schema. */
export const BLOCK_TREE_SCHEMA_ID = 'https://gonext.dev/schemas/block-tree.json';

/**
 * Schema for a single `Block` node. We use `$ref` to express the recursive
 * `innerBlocks` array. The schema permits extra attribute properties because
 * registry-driven attribute validation is the canonical check for those.
 *
 * `clientId` is explicitly allowed — the editor passes blocks through
 * validation with it set, and the save pipeline strips it after validation
 * succeeds.
 */
export const BlockJSONSchema = {
  $schema: SCHEMA_DIALECT,
  $id: BLOCK_SCHEMA_ID,
  type: 'object',
  required: ['type', 'attributes'],
  additionalProperties: false,
  properties: {
    type: {
      type: 'string',
      // Namespaced: "<namespace>/<name>", each part is a slug.
      pattern: '^[a-z][a-z0-9-]*\\/[a-z][a-z0-9-]*$',
      minLength: 3,
      maxLength: 128,
    },
    attributes: {
      type: 'object',
      // Per-block attribute schemas are enforced separately by the registry.
      additionalProperties: true,
    },
    innerBlocks: {
      type: 'array',
      items: { $ref: BLOCK_SCHEMA_ID },
    },
    clientId: {
      type: 'string',
      minLength: 1,
      maxLength: 64,
    },
  },
} as const;

/**
 * Schema for the document body: an array of root blocks.
 */
export const BlockTreeJSONSchema = {
  $schema: SCHEMA_DIALECT,
  $id: BLOCK_TREE_SCHEMA_ID,
  type: 'array',
  items: { $ref: BLOCK_SCHEMA_ID },
} as const;
