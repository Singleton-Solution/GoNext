/**
 * JSON envelope codec for the GoNext plugin ABI.
 *
 * Mirrors `packages/go/plugins/abi/hooks/marshal.go`. Every payload that
 * crosses the host<->guest boundary is JSON, so the codec is essentially
 * a typed view onto `JSON.stringify` / `JSON.parse` with a few invariants:
 *
 *   - Action payloads always carry an empty array for `args` (never null),
 *     so the guest decoder has a single shape to handle.
 *   - Filter payloads always carry an explicit `value` (JSON-null when
 *     none was supplied) and an empty `args` array when no extras were
 *     passed.
 *   - Filter results carry one field — `value` — also JSON-null when the
 *     handler chose not to transform.
 *
 * Result statuses mirror the negative-int32 sentinels the host returns
 * in the low 32 bits of the packed i64. The Javy guest never returns
 * these directly from JS; the runtime entry point at `src/host.ts`
 * translates a typed throw into the right packed return.
 *
 * Why a dedicated module: the codec is the only piece of the SDK that
 * needs to round-trip cleanly in a Node test environment (Vitest).
 * Keeping it free of `globalThis.gn_*` references means the unit tests
 * for envelope handling don't have to stand up the Javy host shims.
 */

/** Sentinel statuses returned by the host. */
export const ResultStatus = {
  OK: 0,
  Error: -1,
  OutOfMemory: -2,
  BadPayload: -3,
  UnknownHook: -4,
  Trap: -5,
} as const;

/** Type-level enumeration of the result-status sentinels. */
export type ResultStatusCode =
  (typeof ResultStatus)[keyof typeof ResultStatus];

/** Tag for the two payload kinds the hook bus dispatches. */
export const PayloadKind = {
  Action: 'action',
  Filter: 'filter',
} as const;

/** Type-level enumeration of payload kinds. */
export type PayloadKindLiteral =
  (typeof PayloadKind)[keyof typeof PayloadKind];

/**
 * Wire shape of an action payload. Arrays of `unknown` because the bus
 * is variadic over `any` — the guest is responsible for narrowing on a
 * per-hook basis.
 */
export interface ActionPayload {
  kind: typeof PayloadKind.Action;
  args: unknown[];
}

/**
 * Wire shape of a filter payload. `value` is the transformable thing
 * threaded through the chain; `args` carries the per-call extras.
 */
export interface FilterPayload {
  kind: typeof PayloadKind.Filter;
  value: unknown;
  args: unknown[];
}

/** Wire shape of a filter handler's return value. */
export interface FilterResult {
  value: unknown;
}

/**
 * Marshal an action payload to its JSON wire form.
 *
 * A null/undefined `args` becomes `[]` so the encoded envelope is
 * always `{"kind":"action","args":[]}` rather than carrying `"args":
 * null`. The guest decoder relies on the field being an array.
 */
export function marshalActionPayload(args?: readonly unknown[] | null): string {
  const payload: ActionPayload = {
    kind: PayloadKind.Action,
    args: args ? Array.from(args) : [],
  };
  return JSON.stringify(payload);
}

/**
 * Marshal a filter payload to its JSON wire form.
 *
 * Like `marshalActionPayload`, missing extras become an empty array.
 * Missing `value` becomes JSON-null so the encoded form always has the
 * field present.
 */
export function marshalFilterPayload(
  value: unknown,
  args?: readonly unknown[] | null,
): string {
  const payload: FilterPayload = {
    kind: PayloadKind.Filter,
    value: value === undefined ? null : value,
    args: args ? Array.from(args) : [],
  };
  return JSON.stringify(payload);
}

/**
 * Marshal a filter result to its JSON wire form.
 *
 * `value === undefined` is treated as JSON-null so the field is always
 * present on the wire.
 */
export function marshalFilterResult(value: unknown): string {
  const payload: FilterResult = {
    value: value === undefined ? null : value,
  };
  return JSON.stringify(payload);
}

/**
 * Parse an incoming action payload's JSON wire bytes.
 *
 * Throws a {@link CodecError} on malformed JSON, missing fields, or a
 * wrong `kind` tag. The thrown error's `status` is
 * `ResultStatus.BadPayload` so the runtime entry point can echo it back
 * to the host as a negative-length return.
 */
export function unmarshalActionPayload(raw: string): ActionPayload {
  const parsed = parseJSON(raw);
  if (!isObject(parsed)) {
    throw new CodecError(
      'action payload is not a JSON object',
      ResultStatus.BadPayload,
    );
  }
  if (parsed['kind'] !== PayloadKind.Action) {
    throw new CodecError(
      `action payload has wrong kind ${JSON.stringify(parsed['kind'])}`,
      ResultStatus.BadPayload,
    );
  }
  const args = parsed['args'];
  if (!Array.isArray(args)) {
    throw new CodecError(
      'action payload args is not an array',
      ResultStatus.BadPayload,
    );
  }
  return { kind: PayloadKind.Action, args };
}

/**
 * Parse an incoming filter payload's JSON wire bytes.
 *
 * Same error contract as {@link unmarshalActionPayload}. `value` is
 * accepted as any JSON-serialisable type, including null.
 */
export function unmarshalFilterPayload(raw: string): FilterPayload {
  const parsed = parseJSON(raw);
  if (!isObject(parsed)) {
    throw new CodecError(
      'filter payload is not a JSON object',
      ResultStatus.BadPayload,
    );
  }
  if (parsed['kind'] !== PayloadKind.Filter) {
    throw new CodecError(
      `filter payload has wrong kind ${JSON.stringify(parsed['kind'])}`,
      ResultStatus.BadPayload,
    );
  }
  if (!('value' in parsed)) {
    throw new CodecError(
      'filter payload missing value field',
      ResultStatus.BadPayload,
    );
  }
  const args = parsed['args'];
  if (!Array.isArray(args)) {
    throw new CodecError(
      'filter payload args is not an array',
      ResultStatus.BadPayload,
    );
  }
  return {
    kind: PayloadKind.Filter,
    value: parsed['value'],
    args,
  };
}

/**
 * Parse a JSON wire blob without throwing the raw `SyntaxError`. We
 * normalise to a {@link CodecError} so the runtime entry point can do
 * one `instanceof` check.
 */
function parseJSON(raw: string): unknown {
  try {
    return JSON.parse(raw);
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    throw new CodecError(`malformed JSON: ${msg}`, ResultStatus.BadPayload);
  }
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

/**
 * Typed failure carried by the codec layer.
 *
 * The runtime entry point catches this and packs `status` into the
 * low 32 bits of the i64 return. Plugin authors generally don't
 * throw `CodecError` directly — they let the codec do it for them and
 * surface their own errors via {@link PluginError} from `host.ts`.
 */
export class CodecError extends Error {
  override readonly name = 'CodecError';
  readonly status: ResultStatusCode;
  constructor(message: string, status: ResultStatusCode = ResultStatus.BadPayload) {
    super(message);
    this.status = status;
  }
}
