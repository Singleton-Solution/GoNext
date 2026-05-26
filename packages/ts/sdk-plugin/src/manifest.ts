/**
 * Typed manifest schema for GoNext plugins.
 *
 * Mirrors `packages/go/plugins/manifest/schema.json` (gonext.io/v1). The
 * Go-side validator is the canonical gate at install time; this module
 * is the author-side ergonomic counterpart so a TypeScript plugin can
 * declare its manifest with autocomplete and catch the common mistakes
 * (bad slug, malformed semver, dotted-token regex misses) BEFORE the
 * bundle even leaves the dev machine.
 *
 * The {@link buildManifest} entry point both validates and renders the
 * manifest to its canonical JSON form. The `gonext-sdk-build` CLI calls
 * it before zipping the bundle so the on-disk `manifest.json` is
 * guaranteed to satisfy the Go validator's schema.
 */

/** The literal apiVersion the v1 schema requires. */
export const MANIFEST_API_VERSION = 'gonext.io/v1' as const;

/** Plugin-slug regex — matches `manifest/schema.json` `name.pattern`. */
const SLUG_RE = /^[a-z][a-z0-9-]{2,40}$/;

/** SemVer 2.0.0 — same regex used by the Go schema. */
const SEMVER_RE =
  /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$/;

/** Dotted token used for capabilities and jobs. */
const CAP_RE = /^[a-z][a-z0-9]*(?:\.[a-z][a-z0-9]*)*$/;

/** Hook name — same dotted token but underscores are allowed. */
const HOOK_RE = /^[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)*$/;

/** Entry path is a POSIX path ending in `.wasm`. */
const ENTRY_RE = /^[A-Za-z0-9_.\-/]+\.wasm$/;

/** Job id — same as capability but hyphens allowed in each segment. */
const JOB_RE = /^[a-z][a-z0-9-]*(?:\.[a-z][a-z0-9-]*)*$/;

/** Semver range — accepts npm-style operators. */
const SEMVER_RANGE_RE = /^[~^>=<*0-9A-Za-z.\-+ ,|]+$/;

/** ed25519 signature — 128 lowercase hex chars. */
const SIGNATURE_RE = /^[0-9a-f]{128}$/;

/** Hooks block — at least one of `actions` / `filters` should be populated. */
export interface HooksManifest {
  actions?: string[];
  filters?: string[];
}

/** Required compatibility constraints. Today only `host` is defined. */
export interface RequiresManifest {
  host: string;
}

/** Inter-plugin dependency. */
export interface DependencyManifest {
  name: string;
  version: string;
}

/** Persistent-storage budgets. Only the KV namespace is described today. */
export interface StorageManifest {
  kv?: {
    max_bytes?: number;
    max_keys?: number;
  };
}

/**
 * Author-facing manifest input. Mirrors the Go schema 1:1, with all
 * optional fields surfacing as TypeScript optionals.
 *
 * `apiVersion` is omitted because {@link buildManifest} fills it in.
 */
export interface ManifestInput {
  name: string;
  version: string;
  entry: string;
  capabilities?: string[];
  hooks?: HooksManifest;
  jobs?: string[];
  requires?: RequiresManifest;
  depends?: DependencyManifest[];
  signature?: string;
  storage?: StorageManifest;
}

/**
 * Canonical manifest as serialized to `manifest.json`. Differs from
 * {@link ManifestInput} in two ways: `apiVersion` is always present, and
 * unset optional fields are omitted (matching the schema's
 * `additionalProperties: false` posture).
 */
export interface Manifest extends ManifestInput {
  apiVersion: typeof MANIFEST_API_VERSION;
}

/** Validation problem surfaced by {@link buildManifest}. */
export interface ManifestIssue {
  path: string;
  message: string;
}

/**
 * Error thrown by {@link buildManifest} when one or more fields fail
 * validation. Carries the full list so the CLI can render every problem
 * in one round trip.
 */
export class ManifestError extends Error {
  override readonly name = 'ManifestError';
  readonly issues: readonly ManifestIssue[];
  constructor(issues: readonly ManifestIssue[]) {
    super(
      issues.length === 1 && issues[0]
        ? `manifest: ${issues[0].path}: ${issues[0].message}`
        : `manifest: ${issues.length} validation errors`,
    );
    this.issues = issues;
  }
}

/**
 * Validate `input` and return the canonical manifest. Throws
 * {@link ManifestError} on any failure.
 *
 * Validation rules mirror `packages/go/plugins/manifest/schema.json` —
 * the host re-validates with the canonical schema at install time, so
 * this function is a courteous early gate, not a security boundary.
 */
export function buildManifest(input: ManifestInput): Manifest {
  const issues: ManifestIssue[] = [];

  if (typeof input.name !== 'string' || !SLUG_RE.test(input.name)) {
    issues.push({
      path: '/name',
      message:
        'must be 3..41 chars: lowercase ASCII start, [a-z0-9-] thereafter',
    });
  }
  if (typeof input.version !== 'string' || !SEMVER_RE.test(input.version)) {
    issues.push({
      path: '/version',
      message: 'must be a SemVer 2.0.0 string (e.g. 1.2.3 or 1.2.3-beta.1)',
    });
  }
  if (typeof input.entry !== 'string' || !ENTRY_RE.test(input.entry)) {
    issues.push({
      path: '/entry',
      message: 'must be a POSIX path ending in .wasm (e.g. plugin.wasm)',
    });
  } else if (/(^|\/)\.\.(\/|$)/.test(input.entry)) {
    issues.push({ path: '/entry', message: 'parent-traversal not allowed' });
  }

  if (input.capabilities) {
    if (!Array.isArray(input.capabilities)) {
      issues.push({
        path: '/capabilities',
        message: 'must be an array of dotted-token strings',
      });
    } else {
      input.capabilities.forEach((c, i) => {
        if (typeof c !== 'string' || !CAP_RE.test(c)) {
          issues.push({
            path: `/capabilities/${i}`,
            message: 'must match ^[a-z][a-z0-9]*(\\.[a-z][a-z0-9]*)*$',
          });
        }
      });
      if (
        new Set(input.capabilities).size !== input.capabilities.length
      ) {
        issues.push({
          path: '/capabilities',
          message: 'entries must be unique',
        });
      }
    }
  }

  if (input.hooks) {
    validateHookList(input.hooks.actions, '/hooks/actions', issues);
    validateHookList(input.hooks.filters, '/hooks/filters', issues);
  }

  if (input.jobs) {
    if (!Array.isArray(input.jobs)) {
      issues.push({ path: '/jobs', message: 'must be an array of strings' });
    } else {
      input.jobs.forEach((j, i) => {
        if (typeof j !== 'string' || !JOB_RE.test(j)) {
          issues.push({
            path: `/jobs/${i}`,
            message: 'must match ^[a-z][a-z0-9-]*(\\.[a-z][a-z0-9-]*)*$',
          });
        }
      });
      if (new Set(input.jobs).size !== input.jobs.length) {
        issues.push({ path: '/jobs', message: 'entries must be unique' });
      }
    }
  }

  if (input.requires) {
    if (typeof input.requires.host !== 'string') {
      issues.push({
        path: '/requires/host',
        message: 'must be a semver range string',
      });
    } else if (!SEMVER_RANGE_RE.test(input.requires.host)) {
      issues.push({
        path: '/requires/host',
        message: 'contains characters outside the semver-range vocabulary',
      });
    }
  }

  if (input.depends) {
    if (!Array.isArray(input.depends)) {
      issues.push({
        path: '/depends',
        message: 'must be an array of {name, version} entries',
      });
    } else {
      input.depends.forEach((d, i) => {
        if (!d || typeof d !== 'object') {
          issues.push({
            path: `/depends/${i}`,
            message: 'each entry must be an object',
          });
          return;
        }
        if (typeof d.name !== 'string' || !SLUG_RE.test(d.name)) {
          issues.push({
            path: `/depends/${i}/name`,
            message: 'must be a plugin slug (same regex as /name)',
          });
        }
        if (
          typeof d.version !== 'string' ||
          !SEMVER_RANGE_RE.test(d.version)
        ) {
          issues.push({
            path: `/depends/${i}/version`,
            message: 'must be a semver range string',
          });
        }
      });
    }
  }

  if (input.signature !== undefined) {
    if (
      typeof input.signature !== 'string' ||
      !SIGNATURE_RE.test(input.signature)
    ) {
      issues.push({
        path: '/signature',
        message: 'must be 128 lowercase hex chars (ed25519)',
      });
    }
  }

  if (input.storage?.kv) {
    const { max_bytes, max_keys } = input.storage.kv;
    if (max_bytes !== undefined) {
      if (!Number.isInteger(max_bytes) || max_bytes < 0) {
        issues.push({
          path: '/storage/kv/max_bytes',
          message: 'must be a non-negative integer',
        });
      }
    }
    if (max_keys !== undefined) {
      if (!Number.isInteger(max_keys) || max_keys < 0) {
        issues.push({
          path: '/storage/kv/max_keys',
          message: 'must be a non-negative integer',
        });
      }
    }
  }

  if (issues.length > 0) {
    throw new ManifestError(issues);
  }

  return canonicalize(input);
}

/**
 * Render a validated manifest to its canonical JSON string. Field
 * order matches the Go schema's declared order so signed bundles can
 * round-trip without re-canonicalisation surprises.
 *
 * Indent is two spaces — same as the existing `examples/plugins/seo`
 * manifest committed to the repo.
 */
export function manifestToJSON(manifest: Manifest): string {
  return JSON.stringify(manifest, null, 2) + '\n';
}

function validateHookList(
  list: string[] | undefined,
  path: string,
  issues: ManifestIssue[],
): void {
  if (list === undefined) return;
  if (!Array.isArray(list)) {
    issues.push({ path, message: 'must be an array of dotted hook names' });
    return;
  }
  list.forEach((name, i) => {
    if (typeof name !== 'string' || !HOOK_RE.test(name)) {
      issues.push({
        path: `${path}/${i}`,
        message: 'must match ^[a-z][a-z0-9_]*(\\.[a-z][a-z0-9_]*)*$',
      });
    }
  });
  if (new Set(list).size !== list.length) {
    issues.push({ path, message: 'entries must be unique' });
  }
}

function canonicalize(input: ManifestInput): Manifest {
  const out: Manifest = {
    apiVersion: MANIFEST_API_VERSION,
    name: input.name,
    version: input.version,
    entry: input.entry,
  };
  if (input.capabilities && input.capabilities.length > 0) {
    out.capabilities = [...input.capabilities];
  }
  if (input.hooks) {
    const hooks: HooksManifest = {};
    if (input.hooks.actions && input.hooks.actions.length > 0) {
      hooks.actions = [...input.hooks.actions];
    }
    if (input.hooks.filters && input.hooks.filters.length > 0) {
      hooks.filters = [...input.hooks.filters];
    }
    if (hooks.actions || hooks.filters) {
      out.hooks = hooks;
    }
  }
  if (input.jobs && input.jobs.length > 0) {
    out.jobs = [...input.jobs];
  }
  if (input.requires) {
    out.requires = { host: input.requires.host };
  }
  if (input.depends && input.depends.length > 0) {
    out.depends = input.depends.map((d) => ({
      name: d.name,
      version: d.version,
    }));
  }
  if (input.signature) {
    out.signature = input.signature;
  }
  if (input.storage?.kv) {
    const kv: StorageManifest['kv'] = {};
    if (input.storage.kv.max_bytes !== undefined) {
      kv.max_bytes = input.storage.kv.max_bytes;
    }
    if (input.storage.kv.max_keys !== undefined) {
      kv.max_keys = input.storage.kv.max_keys;
    }
    if (kv.max_bytes !== undefined || kv.max_keys !== undefined) {
      out.storage = { kv };
    }
  }
  return out;
}
