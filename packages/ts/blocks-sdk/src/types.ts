/**
 * Canonical TypeScript types for the GoNext block tree.
 *
 * See `docs/04-block-editor.md` §1 + §2 for the design rationale. These types
 * are the single source of truth for what lives in `posts.content_blocks`
 * (JSONB) and what plugins register via `BlockRegistry.register()`.
 *
 * The schema document for this shape is in `./schema.ts`; the validator that
 * enforces it lives in `./validator.ts`.
 */

/**
 * A loose record of block attributes. Each registered block type narrows
 * this through its own JSON Schema (see `AttributesSchema`).
 */
export type BlockAttributes = Record<string, unknown>;

/**
 * Per-instance lock flags. Surfaced via `attributes.lock` (note: NOT
 * `supports.lock` — `supports.lock: true` is the registry-side
 * capability flag that says "this block CAN be locked"; the runtime
 * state of "this instance IS locked" lives on the block's attributes
 * so it round-trips with the persisted tree).
 *
 * Authors flip these in the inspector; the canvas respects them by
 * disabling drag handles and delete buttons. The `BlockLockIndicator`
 * toolbar chip is shown whenever at least one flag is set.
 *
 * Both flags default to `false` when absent — a block without a `lock`
 * attribute is fully mobile and removable. See `locks.ts` in
 * `@gonext/blocks-editor` for the read/write helpers.
 */
export interface BlockLockState {
  /** When true, the canvas refuses to move (drag / reorder) the block. */
  move?: boolean;
  /** When true, the canvas refuses to remove (delete) the block. */
  remove?: boolean;
}

/**
 * The persisted shape of a single block node.
 *
 * `type` is namespaced (e.g. `core/paragraph`, `wp-pricing/pricing-table`).
 * `clientId` is editor-only; the save pipeline strips it before persisting.
 */
export interface Block<A extends BlockAttributes = BlockAttributes> {
  /** Registry key, e.g. "core/paragraph". The renderer dispatches on this. */
  type: string;
  /** Validated against the registered type's `attributes` schema. */
  attributes: A;
  /** Recursive children. Omitted for leaf-only blocks. */
  innerBlocks?: Block[];
  /** Editor-only stable id. MUST be stripped before persisting. */
  clientId?: string;
}

/**
 * The full document is just an array of root blocks. No wrapping envelope —
 * see `docs/04-block-editor.md` §1.1.
 */
export type BlockTree = Block[];

/**
 * The set of inserter categories core blocks live in. Plugin blocks may use
 * arbitrary strings, but the editor groups unknown categories under "Other".
 */
export type BlockCategory =
  | 'text'
  | 'media'
  | 'design'
  | 'widgets'
  | 'theme'
  | 'embed'
  | 'custom'
  | (string & {});

/**
 * The "capability matrix" a block can opt into. The editor uses these flags
 * to decide which inspector controls to render and how to constrain the
 * block in the canvas (locking, alignment, allowed children, etc.).
 */
export interface BlockSupports {
  /** Block can contain other blocks. */
  innerBlocks?: boolean;
  /** When `innerBlocks`, an optional whitelist of permitted child types. */
  allowedChildren?: string[];
  /** Inspector colour controls. */
  color?: { background?: boolean; text?: boolean };
  /** Inspector spacing controls. */
  spacing?: { margin?: boolean; padding?: boolean };
  /** Alignment options exposed in the block toolbar. */
  align?: ('left' | 'center' | 'right' | 'wide' | 'full')[];
  /** Raw HTML editing (an escape hatch — false by default). */
  html?: boolean;
  /** Reusable / synced-pattern eligible. */
  reusable?: boolean;
  /** Lockable in the editor UI. */
  lock?: boolean;
}

/**
 * A JSON Schema 2020-12 document describing a block's attributes. We keep
 * this typed loosely on purpose: tightening it forces every consumer into
 * complex generic gymnastics, and Ajv accepts any object that conforms to
 * the spec.
 *
 * The phantom `_attrs` field is purely for type inference — it never exists
 * at runtime.
 */
export type AttributesSchema<A extends BlockAttributes = BlockAttributes> =
  & Record<string, unknown>
  & { _attrs?: A };

/**
 * Editor-side React component types. We model them as opaque values so the
 * SDK can be consumed in Node-only contexts (server renderer, validation
 * scripts) without pulling in React's type definitions.
 */
export type EditComponent<A extends BlockAttributes = BlockAttributes> =
  (props: BlockEditProps<A>) => unknown;

export type SaveComponent<A extends BlockAttributes = BlockAttributes> =
  (props: BlockSaveProps<A>) => unknown;

export interface BlockEditProps<A extends BlockAttributes = BlockAttributes> {
  attributes: A;
  setAttributes: (patch: Partial<A>) => void;
  isSelected: boolean;
  clientId: string;
  context: Record<string, unknown>;
}

export interface BlockSaveProps<A extends BlockAttributes = BlockAttributes> {
  attributes: A;
}

/**
 * A single migration step. The deprecation pipeline (see `migrateBlock`)
 * walks the array newest → oldest and applies the first entry that says
 * `isEligible()` (or, when omitted, the first entry whose own attribute
 * schema validates against the input).
 */
export interface BlockDeprecation<
  Old extends BlockAttributes = BlockAttributes,
  New extends BlockAttributes = BlockAttributes,
> {
  /**
   * The schema version this entry migrates **away from** — i.e. the
   * version of the OLD attribute shape. Optional but recommended:
   * when present, the inspector's deprecation banner can say "this
   * block is at v1; v3 is current" instead of a generic "needs
   * migration" hint. Versions are monotonic integers; semantic
   * versioning is overkill for a per-block schema.
   */
  version?: number;
  /** JSON Schema describing the OLD attribute shape this step matches. */
  attributes: AttributesSchema<Old>;
  /**
   * Translate old attributes into the new shape. May also rewrite the
   * inner-block subtree (e.g. promote children, drop a wrapper).
   */
  migrate: (
    oldAttrs: Old,
    oldInnerBlocks: Block[],
  ) => { attributes: New; innerBlocks?: Block[] };
  /**
   * Optional faster-than-validation eligibility check. Returns true when
   * the migration should be applied to the given block.
   */
  isEligible?: (attrs: unknown, innerBlocks: Block[]) => boolean;
}

/**
 * What a plugin (or core) registers to declare a block. See
 * `docs/04-block-editor.md` §2.1 for the full rationale.
 *
 * `edit` and `save` are lazy imports so the registry can be populated on the
 * server (validation, render) without forcing the editor bundle to load.
 */
export interface BlockTypeDefinition<
  A extends BlockAttributes = BlockAttributes,
> {
  /** Namespaced name, e.g. "core/paragraph". */
  name: string;
  /** Inserter label. */
  title: string;
  /** Inserter category — see `BlockCategory`. */
  category: BlockCategory;
  /** Optional one-line description for the inserter. */
  description?: string;
  /**
   * Icon identifier. Either a registry id (e.g. `lucide:dollar-sign`) or
   * an inline SVG string. The editor resolves this lazily.
   */
  icon?: string;
  /**
   * Current schema version. When omitted, the migration pipeline
   * treats the block as version 1. Bumping this value tells the
   * editor "this block has new attribute semantics — anything older
   * needs to be walked through `deprecated[]`". The inspector
   * surfaces a "this block is deprecated" warning when a loaded
   * block's effective version is below `version` and no further
   * deprecation step matches.
   */
  version?: number;
  /** JSON Schema for the block's attributes. */
  attributes: AttributesSchema<A>;
  /** Capability matrix. */
  supports?: BlockSupports;
  /** If set, restricts the block's allowed parents (block names). */
  parent?: string[];
  /** If set, restricts the block's allowed ancestors anywhere in the chain. */
  ancestor?: string[];
  /** Lazy import of the editor component. */
  edit: () => Promise<{ default: EditComponent<A> }>;
  /** Lazy import of the save component (static blocks). */
  save?: () => Promise<{ default: SaveComponent<A> }>;
  /** Dynamic-block render directive: "<plugin-slug>/<handler-name>". */
  render?: { handler: string };
  /**
   * Block context — keys this block exposes to its descendants.
   *
   * The editor and the Go server-side walker each maintain a context
   * map keyed by these names. By default, each key resolves to the
   * value at the matching attribute on the block instance (e.g. a
   * Query block that lists `["postId"]` exposes whichever `postId`
   * attribute the current iteration carries).
   *
   * Descendants opt into reading these values via `usesContext`. The
   * editor's `useBlockContext(key)` hook (in `@gonext/blocks-editor`)
   * reads from the same map.
   */
  providesContext?: string[];
  /**
   * Block context — keys this block reads from its ancestors.
   *
   * The editor canvas filters the ancestor context map down to these
   * keys before passing it to the Edit component on the `context`
   * prop. The Go walker does the same when invoking the registered
   * server-side renderer. Listing a key here that no ancestor
   * provides is non-fatal: the value is `undefined`.
   */
  usesContext?: string[];
  /** Schema migrations, ordered newest → oldest. */
  deprecated?: BlockDeprecation<BlockAttributes, A>[];
}

/**
 * A single, structured validation failure. The `path` is a JSON-pointer-like
 * string indicating where in the input the failure occurred:
 *
 *   "/0"                       — the first root block is malformed
 *   "/0/innerBlocks/2/type"    — the type of a nested grandchild is wrong
 *   "/0/attributes/level"      — an attribute mismatched its schema
 */
export interface ValidationError {
  /** Pointer into the tree where the error was found. */
  path: string;
  /** Machine code: "schema", "unknown-type", "attributes". */
  code: 'schema' | 'unknown-type' | 'attributes';
  /** Human-readable message — safe to surface to authors. */
  message: string;
  /**
   * The offending block's `type`, when known. Helpful in editor toasts that
   * say "Block 'core/columns' failed to validate: …".
   */
  blockType?: string;
}

export interface ValidationResult {
  valid: boolean;
  errors: ValidationError[];
}
