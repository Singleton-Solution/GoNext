/**
 * Plugin admin — shared types.
 *
 * Mirrors the host-side lifecycle record (packages/go/plugins/lifecycle/state.go)
 * and manifest format (packages/go/plugins/manifest/manifest.go) for the
 * shapes the admin actually reads. Keeping a TS-side projection rather
 * than importing a generated client avoids coupling the admin to an
 * OpenAPI build step that hasn't landed yet (issue #240).
 *
 * The wire contract for the read endpoints lives in
 * `docs/05-admin-api.md` (the plugins section in that doc is still TBD —
 * for now we follow the most natural REST projection of the lifecycle
 * record). Fields that the host may legitimately omit (zero timestamps,
 * empty error blocks) are typed as nullable / optional so the UI never
 * crashes on a sparse response.
 */

/**
 * State machine position. Mirrors `lifecycle.State` in the Go side.
 *
 * The admin uses these as discriminants for badge rendering and button
 * gating, never for enforcement — the host re-validates every transition
 * regardless of what the UI tried to send.
 */
export type PluginState =
  | 'installed'
  | 'active'
  | 'inactive'
  | 'pending_uninstall'
  | 'errored';

/**
 * Single declared dependency from `manifest.depends`. The version range
 * is the verbatim semver expression the manifest author wrote; we don't
 * try to render a friendly form of it.
 */
export interface PluginDependencyDecl {
  /** Slug of the other plugin we depend on. */
  name: string;
  /** SemVer range, e.g. `^1.2.0`. */
  version: string;
}

/**
 * Per-dependency status as it appears in the detail screen.
 *
 * `state` is the lifecycle state of the dependency target (so the
 * operator can see "the dep is installed but not active"); `satisfied`
 * is the resolver's verdict — if false, activation is blocked.
 */
export interface PluginDependencyStatus extends PluginDependencyDecl {
  /** Whether the dependency is currently satisfied by the live registry. */
  satisfied: boolean;
  /** Resolved state of the dependency target, or null if not installed. */
  state: PluginState | null;
  /** Resolved version of the dependency target, or null if not installed. */
  resolvedVersion: string | null;
  /** Optional human reason for an unsatisfied dep, e.g. "not installed". */
  reason?: string;
}

/**
 * Manifest summary the admin needs for list + detail views. Mirrors the
 * subset of fields the host exposes through `GET /api/v1/plugins/:name`.
 * The full raw manifest is rendered too (see PluginRecord.manifestRaw)
 * but typed accessors make the detail rendering cleaner.
 */
export interface PluginManifest {
  apiVersion: string;
  name: string;
  version: string;
  /** Optional human display name (manifest free-form). */
  displayName?: string;
  /** Optional short description. */
  description?: string;
  /** Optional author / vendor block. */
  author?: string;
  /** Optional homepage URL. */
  homepage?: string;
  /** Entry point — POSIX path to the WASM module inside the bundle. */
  entry?: string;
  /** Declared capability IDs. */
  capabilities?: string[];
  /** Declared hook ids the plugin will register. */
  hooks?: {
    actions?: string[];
    filters?: string[];
  };
  /** Background-job ids the plugin owns. */
  jobs?: string[];
  /** Compatibility ranges (host abi etc.). */
  requires?: {
    host?: string;
    abi?: number;
  };
  /** Inter-plugin dependency list. */
  depends?: PluginDependencyDecl[];
}

/**
 * Last error block, populated when the lifecycle row is in `errored`.
 */
export interface PluginErrorInfo {
  message: string;
  /** ISO8601 timestamp the error was recorded at. */
  at: string;
}

/**
 * A row from `GET /api/v1/plugins`. The list endpoint returns a compact
 * projection; the detail endpoint returns the same shape plus
 * `manifestRaw` and `dependenciesStatus`.
 */
export interface PluginRecord {
  /** Platform-unique slug — used as the URL segment and stable id. */
  name: string;
  /** Optional display name; falls back to `name` in UIs. */
  displayName?: string;
  /** SemVer string verbatim from the manifest. */
  version: string;
  /** Current lifecycle state. */
  state: PluginState;
  /** ISO8601 install time, or null if the host returns zero. */
  installedAt: string | null;
  /** ISO8601 last activation time, or null if never activated. */
  activatedAt: string | null;
  /** Capability IDs declared by the manifest. */
  capabilities: string[];
  /** Last error block, present when state is `errored`. */
  lastError: PluginErrorInfo | null;
  /** Full typed manifest summary; populated by the detail endpoint. */
  manifest?: PluginManifest;
  /** Raw manifest JSON string for the verbatim viewer. */
  manifestRaw?: string;
  /** Per-dependency satisfaction info; populated by the detail endpoint. */
  dependenciesStatus?: PluginDependencyStatus[];
}

/**
 * List response. Falls back to an empty array if the host returns
 * `null` or an unexpected envelope shape.
 */
export interface PluginListResponse {
  plugins: PluginRecord[];
}

/**
 * Result of running an install / activate / uninstall server action.
 * `ok` discriminates the union — when false, `error` carries the
 * user-facing message.
 */
export type ActionResult =
  | { ok: true; message?: string }
  | { ok: false; error: string };
