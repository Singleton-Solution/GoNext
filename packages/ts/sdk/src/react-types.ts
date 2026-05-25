/**
 * Minimal `ComponentType` declaration.
 *
 * The SDK exposes block specs that carry React components, but
 * adding `@types/react` as a dependency would (a) bloat the SDK's
 * type surface, (b) couple us to a specific React major, and (c)
 * make the SDK harder to consume from non-React block runtimes
 * (Preact, future Solid integration). Declaring just the
 * `ComponentType<P>` callable shape gives plugin authors the type
 * affordance without the runtime / peer-dep coupling.
 *
 * The shape mirrors React's: a function from `props` to
 * `ReactElement | null`, plus optional `displayName`. We use
 * `unknown` for the return type so a JSX-emitting function and a
 * pre-rendered element factory both satisfy the contract.
 */

/**
 * Open-typed React element. The block registry treats whatever the
 * component returns as opaque — it's the renderer (editor or theme)
 * that types it tightly.
 */
export type ReactElementLike = unknown;

/**
 * Subset of `React.ComponentType<P>` that the SDK needs. Matches
 * the real `@types/react` declaration structurally so a plugin can
 * pass a real React component and get the right inference.
 */
export interface ComponentType<P> {
  (props: P): ReactElementLike;
  displayName?: string;
}
