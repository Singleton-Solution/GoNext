/**
 * Convenience entry point that registers every first-party transform
 * onto a passed `TransformRegistry`. Mirrors the
 * `registerCoreBlocks(...)` / `registerCorePatterns(...)` shape so
 * apps that already wired the latter can pick up transforms with a
 * parallel one-line call.
 *
 * Pass `{ replace: true }` only for HMR-style reloads — production
 * code should leave it off so a duplicate registration throws loudly
 * via `DuplicateTransformError`.
 */
import { CORE_TRANSFORMS } from './builtins.ts';
import { TransformRegistry } from './registry.ts';

export function registerBuiltinTransforms(
  registry: TransformRegistry,
  options: { replace?: boolean } = {},
): void {
  for (const transform of CORE_TRANSFORMS) {
    registry.register(transform, options);
  }
}
