/**
 * @gonext/sdk-plugin — public entry point.
 *
 * Plugin authors write code like this:
 *
 *     import { pluginInit, registerAction, registerFilter, host } from '@gonext/sdk-plugin';
 *
 *     registerAction('save_post', async (args) => {
 *       host.log.info('post saved: ' + JSON.stringify(args));
 *       host.kv.set('last-save', String(host.nowMs()));
 *     });
 *
 *     registerFilter('the_content', async (value, _args) => {
 *       return `<div data-plugin="hello">${value}</div>`;
 *     });
 *
 *     pluginInit();
 *
 * `pluginInit()` wires the dispatcher onto `globalThis` under the
 * well-known `gn_handle_hook` name. The Javy host adapter calls that
 * function for every hook invocation, passing the marshaled JSON
 * envelope. The dispatcher routes to the registered handler, marshals
 * the result back out, and translates any thrown error into the right
 * `ResultStatus` sentinel.
 *
 * The codec lives in `codec.ts`; the typed host wrappers live in
 * `host.ts`; the manifest builder lives in `manifest.ts`. This module
 * is intentionally tiny — its only job is the dispatcher plus the
 * registration API.
 */

import {
  CodecError,
  ResultStatus,
  type ResultStatusCode,
  marshalFilterResult,
  unmarshalActionPayload,
  unmarshalFilterPayload,
} from './codec.ts';
import { HostError } from './host.ts';

export {
  CodecError,
  PayloadKind,
  ResultStatus,
  marshalActionPayload,
  marshalFilterPayload,
  marshalFilterResult,
  unmarshalActionPayload,
  unmarshalFilterPayload,
} from './codec.ts';
export type {
  ActionPayload,
  FilterPayload,
  FilterResult,
  PayloadKindLiteral,
  ResultStatusCode,
} from './codec.ts';

export {
  HostError,
  LogLevel,
  audit,
  cache,
  cron,
  db,
  host,
  http,
  i18n,
  kv,
  log,
  media,
  nowMs,
  observe,
  secrets,
  users,
} from './host.ts';
export type {
  HostBindings,
  HostDataResult,
  HttpFetchRequest,
  HttpFetchResponse,
  LogLevelCode,
} from './host.ts';

export {
  MANIFEST_API_VERSION,
  ManifestError,
  buildManifest,
  manifestToJSON,
} from './manifest.ts';
export type {
  DependencyManifest,
  HooksManifest,
  Manifest,
  ManifestInput,
  ManifestIssue,
  RequiresManifest,
  StorageManifest,
} from './manifest.ts';

/**
 * Action handler. Receives the args list the host bus passed to
 * `hooks.Bus.Do`. Return value is ignored — actions are fire-and-forget.
 * Returning a Promise is fine; the dispatcher awaits it.
 */
export type ActionHandler = (
  args: unknown[],
) => void | Promise<void>;

/**
 * Filter handler. Receives the value to transform plus any extras the
 * host passed. The return value (or resolved value of the returned
 * Promise) is JSON-encoded and sent back to the host.
 */
export type FilterHandler = (
  value: unknown,
  args: unknown[],
) => unknown | Promise<unknown>;

/** Internal registry of handlers keyed by hook name. */
const actionHandlers = new Map<string, ActionHandler>();
const filterHandlers = new Map<string, FilterHandler>();

/**
 * Register an action handler. Calling twice for the same hook
 * replaces the previous handler — same semantics as the Go SDK's
 * `AddAction` so authors switching languages aren't surprised.
 */
export function registerAction(name: string, handler: ActionHandler): void {
  if (!name) throw new Error('registerAction: hook name is required');
  actionHandlers.set(name, handler);
}

/** Register a filter handler. Replaces on conflict; see {@link registerAction}. */
export function registerFilter(name: string, handler: FilterHandler): void {
  if (!name) throw new Error('registerFilter: hook name is required');
  filterHandlers.set(name, handler);
}

/**
 * Dispatcher used by the Javy adapter. The adapter exposes the
 * raw `(name, payloadJSON)` invocation; this function does the
 * registry lookup, JSON parsing, and result marshalling.
 *
 * Exported so tests can call it directly without going through the
 * Javy bridge.
 *
 * Return value carries the JSON-encoded result body on success
 * (`status === 0`) or a `ResultStatus` sentinel with no body on
 * failure. The Javy adapter packs `(ptr, len)` from this shape on
 * the way back to the host.
 */
export interface DispatchResult {
  status: ResultStatusCode | number;
  body?: string;
}

export async function dispatch(
  name: string,
  payloadJSON: string,
): Promise<DispatchResult> {
  if (!name) {
    return { status: ResultStatus.BadPayload };
  }
  // Filters first — most hot-path calls are filter dispatch.
  const filter = filterHandlers.get(name);
  if (filter) {
    try {
      const { value, args } = unmarshalFilterPayload(payloadJSON);
      const transformed = await Promise.resolve(filter(value, args));
      return { status: ResultStatus.OK, body: marshalFilterResult(transformed) };
    } catch (err) {
      return { status: classifyError(err) };
    }
  }
  const action = actionHandlers.get(name);
  if (action) {
    try {
      const { args } = unmarshalActionPayload(payloadJSON);
      await Promise.resolve(action(args));
      return { status: ResultStatus.OK };
    } catch (err) {
      return { status: classifyError(err) };
    }
  }
  return { status: ResultStatus.UnknownHook };
}

/**
 * Wire the dispatcher onto `globalThis` so the Javy host adapter can
 * find it.
 *
 * The adapter exposes one function — `globalThis.gn_handle_hook` —
 * with the signature `(name, payloadJSON) => Promise<DispatchResult>`.
 * The compiled wasm body that Javy emits handles the pointer / length
 * dance; the JS layer just sees strings.
 *
 * Plugin authors call this once at module-top-level after registering
 * their handlers. The function is idempotent — calling it twice
 * simply rewires the same dispatcher, useful in dev when hot-reload
 * re-evaluates the bundle.
 */
export function pluginInit(): void {
  const slot = globalThis as Record<string, unknown>;
  slot['gn_handle_hook'] = dispatch;
}

function classifyError(err: unknown): ResultStatusCode | number {
  if (err instanceof CodecError) return err.status;
  if (err instanceof HostError) {
    // Host failures inside a handler propagate as a generic guest error;
    // the host has already audited the underlying ABI denial / quota /
    // etc., so we don't echo the data sentinel back through the hook
    // surface.
    return ResultStatus.Error;
  }
  return ResultStatus.Error;
}

/**
 * Test-only helper: clear every registered handler. Used by the test
 * suite to keep cases independent without re-importing the module.
 * Plugin authors should never need this.
 */
export function _resetForTests(): void {
  actionHandlers.clear();
  filterHandlers.clear();
}
