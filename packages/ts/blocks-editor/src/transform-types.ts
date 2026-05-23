/**
 * Structural types the editor uses to interoperate with
 * `@gonext/blocks-transforms` **without** depending on that package.
 *
 * Mirrors the same pattern as `pattern-types.ts`: transforms are
 * authored against `@gonext/blocks-sdk` block types, and the editor's
 * toolbar is the consumer of them. Pulling in the transforms package
 * from the editor would create a layering surprise (the host wires
 * transforms in at app-init time, just like the block registry); the
 * structural shape here mirrors `@gonext/blocks-transforms` exactly
 * so plumbing a real TransformRegistry into the toolbar Just Works.
 */
import type { Block } from '@gonext/blocks-sdk';

/**
 * Result of a transform, mirroring `@gonext/blocks-transforms`. A
 * single replacement block or a sibling array.
 */
export type TransformResult = Block | Block[];

/**
 * Mirror of `@gonext/blocks-transforms` `Transform`. See that package
 * for the authoritative field-by-field rationale.
 */
export interface Transform {
  id: string;
  from: string;
  to: string;
  label: string;
  description?: string;
  convert: (
    block: Block,
    context?: Record<string, unknown>,
  ) => TransformResult;
  isMatch?: (block: Block) => boolean;
}

/**
 * Mirror of `@gonext/blocks-transforms` `TransformRegistry`. We type
 * only the surface the toolbar uses (`from(name, block)`); other
 * methods may exist on concrete implementations but are not required.
 */
export interface TransformRegistry {
  from(blockName: string, block?: Block): Transform[];
}
