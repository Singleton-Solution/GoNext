/**
 * Hook-payload validator.
 *
 * Mirror of packages/go/hooks/schemas: a registry of hook name -> JSON
 * Schema with Validate / ValidateStrict entry points. Plugin guests
 * running in a TS environment can hook into the registry on the
 * "post" path (validate before sending a value back to the host) and
 * the "pre" path (validate before fanning a value out to other
 * subscribers).
 *
 * The runtime contract matches the Go side byte-for-byte:
 *
 *   - Schemas are pinned to JSON Schema 2020-12 (see
 *     packages/go/jsonschemautil for the policy rationale).
 *   - Unknown hooks in loose mode pass through; in strict mode they
 *     are rejected with a HooksUnregisteredError.
 *   - Validation failures throw a HooksValidationError carrying the
 *     hook name and the underlying Ajv error tree.
 *
 * The validator never throws on a marshalling failure (everything in
 * TS is already a structured value); the failure modes are limited to
 * "missing schema in strict mode" and "value didn't match schema".
 */
import Ajv, { type ErrorObject, type ValidateFunction } from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';
import { BUILTIN_SCHEMAS, type BuiltinHookName } from './schemas/index.ts';

/**
 * Pinned JSON Schema dialect URI. Must match `Draft2020URI` in
 * packages/go/jsonschemautil and `SCHEMA_DIALECT` in
 * packages/ts/blocks-sdk so a misconfigured schema fails consistently
 * across the stack.
 */
export const SCHEMA_DIALECT =
  'https://json-schema.org/draft/2020-12/schema';

/** Mode controlling unknown-hook handling. */
export type EnforcementMode = 'loose' | 'strict';

/**
 * Error raised when a payload fails its registered schema.
 *
 * Carries the hook name and the raw Ajv error tree so callers wanting
 * per-path detail can render it without re-running the validator.
 */
export class HooksValidationError extends Error {
  public override readonly name = 'HooksValidationError';
  public readonly hookName: string;
  public readonly errors: ReadonlyArray<ErrorObject>;
  constructor(hookName: string, errors: ReadonlyArray<ErrorObject>) {
    const first = errors[0];
    const summary =
      first !== undefined
        ? `${first.instancePath || '/'}: ${first.message ?? 'validation failed'}`
        : 'validation failed';
    super(
      `hooks-schemas: payload for "${hookName}" does not match its schema (${summary})`,
    );
    this.hookName = hookName;
    this.errors = errors;
  }
}

/**
 * Error raised in strict mode when a hook has no registered schema.
 *
 * Mirrors the Go-side `ErrUnregisteredHook` sentinel — callers can
 * `instanceof` against this class to distinguish "no contract declared"
 * from "payload didn't match the contract".
 */
export class HooksUnregisteredError extends Error {
  public override readonly name = 'HooksUnregisteredError';
  public readonly hookName: string;
  constructor(hookName: string) {
    super(`hooks-schemas: no schema registered for hook "${hookName}"`);
    this.hookName = hookName;
  }
}

/**
 * Error raised by `register` when the supplied schema declares a
 * dialect other than the pinned draft 2020-12. Catching this at
 * registration time means the operator sees the offending file's name
 * rather than a generic Ajv "unknown keyword" later.
 */
export class HooksUnsupportedDialectError extends Error {
  public override readonly name = 'HooksUnsupportedDialectError';
  public readonly declared: string;
  public readonly hookName: string;
  constructor(declared: string, hookName: string) {
    super(
      `hooks-schemas: schema for "${hookName}" declared $schema=${JSON.stringify(declared)}, ` +
        `but only ${SCHEMA_DIALECT} is accepted.`,
    );
    this.declared = declared;
    this.hookName = hookName;
  }
}

/**
 * SchemaRegistry holds the per-hook compiled validators. A single
 * Ajv instance backs the registry so cross-schema `$ref`s could
 * resolve, though the built-in schemas are self-contained.
 *
 * Construction is cheap — tests can freely build a per-test registry
 * via `new SchemaRegistry()`. Use `createBuiltinRegistry()` to obtain
 * one pre-populated with the WP-compat hook contracts.
 */
export class SchemaRegistry {
  private readonly ajv: Ajv;
  private readonly validators = new Map<string, ValidateFunction>();

  constructor() {
    this.ajv = new Ajv({
      allErrors: true,
      strict: false,
      addUsedSchema: false,
    });
    addFormats(this.ajv);
  }

  /**
   * Compile and store a schema for hookName. The schema must declare
   * the pinned 2020-12 dialect (or omit `$schema`, which Ajv2020
   * treats as 2020-12 by default).
   */
  register(hookName: string, schema: unknown): void {
    if (hookName.length === 0) {
      throw new Error('hooks-schemas: register: empty hook name');
    }
    if (schema === null || typeof schema !== 'object' || Array.isArray(schema)) {
      throw new Error(
        `hooks-schemas: register("${hookName}"): schema must be an object`,
      );
    }
    const $schema = (schema as { $schema?: unknown }).$schema;
    if ($schema !== undefined) {
      if (typeof $schema !== 'string' || $schema.trim() !== SCHEMA_DIALECT) {
        throw new HooksUnsupportedDialectError(String($schema), hookName);
      }
    }
    if (this.validators.has(hookName)) {
      throw new Error(
        `hooks-schemas: register: hook "${hookName}" already registered`,
      );
    }
    const fn = this.ajv.compile(schema as Record<string, unknown>);
    this.validators.set(hookName, fn);
  }

  /** Whether a schema is registered for hookName. */
  has(hookName: string): boolean {
    return this.validators.has(hookName);
  }

  /** Sorted list of registered hook names. Snapshot — caller may mutate. */
  names(): string[] {
    return Array.from(this.validators.keys()).sort();
  }

  /**
   * Validate payload against the schema for hookName. In loose mode
   * (default) an unregistered hook returns; in strict mode it throws
   * a `HooksUnregisteredError`.
   *
   * Throws `HooksValidationError` if the payload fails the schema.
   * Returns nothing on success — the validated payload is the same
   * value the caller passed in.
   */
  validate(hookName: string, payload: unknown, mode: EnforcementMode = 'loose'): void {
    const fn = this.validators.get(hookName);
    if (fn === undefined) {
      if (mode === 'strict') {
        throw new HooksUnregisteredError(hookName);
      }
      return;
    }
    const ok = fn(payload);
    if (!ok) {
      throw new HooksValidationError(hookName, fn.errors ?? []);
    }
  }
}

/**
 * Construct a registry pre-populated with every built-in WP-compat
 * schema. Use this in plugin guest code as the baseline; register
 * extra plugin-specific schemas on top via `registry.register()`.
 */
export function createBuiltinRegistry(): SchemaRegistry {
  const reg = new SchemaRegistry();
  for (const [name, schema] of Object.entries(BUILTIN_SCHEMAS)) {
    reg.register(name, schema);
  }
  return reg;
}

/** Convenience: the canonical list of built-in hook names. */
export function builtinHookNames(): readonly BuiltinHookName[] {
  return Object.keys(BUILTIN_SCHEMAS) as BuiltinHookName[];
}
