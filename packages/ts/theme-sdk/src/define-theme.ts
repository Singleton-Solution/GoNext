/**
 * `defineTheme()` — the identity helper theme authors call to build a
 * type-safe `theme.json` value.
 *
 * The function does *no* runtime work: it returns its input verbatim,
 * cast to `GoNextTheme`. The whole point is the type position — the
 * generic parameter is constrained so that:
 *
 *  - missing required fields fail at compile time, and
 *  - **unknown top-level keys fail at compile time** (the
 *    `Exact<…>` mapped type below makes any extra property an
 *    explicit `never`, which is a type error the compiler refuses to
 *    silently widen).
 *
 * This is the same pattern the editor's `defineBlockType()` uses, and
 * it's the convention §15.2 of `docs/03-theme-system.md` calls out:
 *
 *   import { defineTheme } from '@gonext/theme-sdk';
 *   export default defineTheme({
 *     version: 1,
 *     settings: { … },
 *     styles:   { … },
 *   });
 *
 * Authors can write `theme.json` as a `.ts` file and let the compiler
 * surface mistakes before a `theme install` ever runs. The build step
 * is then a one-liner that emits the value as JSON.
 */

import type { ThemeJson } from './theme-json.ts';

/**
 * The validated theme value `defineTheme()` returns. We re-export
 * `ThemeJson` under a friendlier name so the author-facing surface
 * reads "this is a GoNext theme manifest" rather than "this is a
 * JSON document" — they're the same type, but the alias makes the
 * import line self-documenting.
 */
export type GoNextTheme = ThemeJson;

/**
 * Mapped type that "exactly" matches `Shape`: every key of `T` must
 * be a key of `Shape`. Extra keys are reported as `never`, which
 * turns the property into an unsatisfiable constraint and forces a
 * compile-time error at the call site.
 *
 * This is the standard trick TypeScript users reach for to emulate
 * exact object types (which the language otherwise lacks). The cost
 * is a small amount of type-level noise; the win is that
 * `defineTheme({...known fields, oopsTypo: true})` won't compile —
 * exactly the behavior the issue spec asks for.
 *
 * The generic is intentionally inferred from the *argument*, not
 * defaulted to `ThemeJson`, so the compiler narrows to the precise
 * literal type the author wrote.
 */
export type Exact<T, Shape> = T & {
  [K in Exclude<keyof T, keyof Shape>]: never;
};

/**
 * Identity helper for authoring a `theme.json` value in TypeScript.
 *
 * No-op at runtime; in the type position it forces the argument to
 * "exactly" satisfy `GoNextTheme` (a typo in a top-level key is a
 * compile error). The returned value is the same object the caller
 * passed in — no copy, no normalization, no schema validation
 * (validation belongs to the Go side and runs at install).
 */
export function defineTheme<T extends GoNextTheme>(
  theme: T & Exact<T, GoNextTheme>,
): GoNextTheme {
  return theme;
}
