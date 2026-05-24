/**
 * Plugin admin — capability registry mirror.
 *
 * This file is the TypeScript projection of the built-in capability set
 * registered in `packages/go/plugins/capabilities/registry.go`. The host
 * is authoritative — the lifecycle install path validates every
 * capability id in a manifest against the live Go registry — but the
 * admin needs a local lookup so the capability-review screen can render
 * sensible plain-English descriptions before the install request is
 * fired.
 *
 * Keeping the two in sync is a manual chore today; once the host
 * exposes `GET /api/v1/plugins/_capabilities` (TBD) this module flips to
 * a runtime fetch with a static fallback. Until then, treat any
 * divergence as a release-time check: if a manifest references an id
 * not in this map, we render a fallback row warning that the operator
 * should review the manifest manually rather than relying on the
 * canned description.
 *
 * Source of truth for the descriptions is `builtinCapabilityDefs` in
 * `packages/go/plugins/capabilities/registry.go`; the wording is kept
 * identical so an operator looking at the install screen sees the same
 * text as appears in any host-side audit log.
 */

/** Severity tier for the install-time review. */
export type CapabilityRisk = 'low' | 'sensitive';

/**
 * Local mirror of a `CapabilityDef`. The `human` field is what the
 * review screen displays — written in operator voice ("Read all
 * posts") rather than plugin voice ("Read post rows").
 */
export interface CapabilityInfo {
  /** Wire-level id, e.g. "posts.read". */
  id: string;
  /** Resource grouping for sectioned display. */
  resource: string;
  /** Action verb. */
  action: string;
  /** Verbatim description from the host registry. */
  description: string;
  /**
   * Operator-voice phrasing for the review screen. The host description
   * is plugin-centric; this is platform-centric so the consent prompt
   * reads naturally ("This plugin will be able to...").
   */
  human: string;
  /** Sensitive caps get the warning treatment. */
  risk: CapabilityRisk;
}

/**
 * The built-in cap set. Kept in lockstep with
 * `packages/go/plugins/capabilities/registry.go::builtinCapabilityDefs`.
 *
 * Ordering matters: the review screen renders capabilities in the order
 * the manifest declared them, but groups them by `resource` for the
 * summary header. Adding a cap here is a no-op if no manifest declares
 * it; removing one orphans any plugin already installed with it
 * (validation will fail on next reinstall).
 */
const BUILTIN_CAPABILITIES: ReadonlyArray<CapabilityInfo> = [
  {
    id: 'posts.read',
    resource: 'posts',
    action: 'read',
    description: 'Read post rows.',
    human: 'Read all posts on this site.',
    risk: 'low',
  },
  {
    id: 'posts.write',
    resource: 'posts',
    action: 'write',
    description: 'Create, update, and delete post rows.',
    human: 'Create, update, and delete posts on this site.',
    risk: 'low',
  },
  {
    id: 'users.read',
    resource: 'users',
    action: 'read',
    description: 'Read user rows (non-PII projection).',
    human: 'Read non-PII user records (handle, role, status).',
    risk: 'low',
  },
  {
    id: 'email.send',
    resource: 'email',
    action: 'send',
    description: 'Send outbound transactional email.',
    human: 'Send transactional email on behalf of this site.',
    risk: 'sensitive',
  },
  {
    id: 'http.fetch',
    resource: 'http',
    action: 'fetch',
    description: 'Make outbound HTTP requests.',
    human:
      'Make outbound HTTP requests to any URL — including third-party services.',
    risk: 'sensitive',
  },
  {
    id: 'kv.read',
    resource: 'kv',
    action: 'read',
    description: 'Read from the plugin key-value namespace.',
    human: 'Read from the plugin’s own key-value namespace.',
    risk: 'low',
  },
  {
    id: 'kv.write',
    resource: 'kv',
    action: 'write',
    description: 'Write to the plugin key-value namespace.',
    human: 'Write to the plugin’s own key-value namespace.',
    risk: 'low',
  },
  {
    id: 'hooks.subscribe',
    resource: 'hooks',
    action: 'subscribe',
    description: 'Register a hook listener.',
    human:
      'Subscribe to platform events (so the plugin can react to actions like "post published").',
    risk: 'low',
  },
  {
    id: 'jobs.enqueue',
    resource: 'jobs',
    action: 'enqueue',
    description: 'Enqueue a background job.',
    human: 'Enqueue background jobs to run asynchronously on the host.',
    risk: 'low',
  },
];

const BY_ID: ReadonlyMap<string, CapabilityInfo> = new Map(
  BUILTIN_CAPABILITIES.map((c) => [c.id, c]),
);

/**
 * Look up a capability by id. Returns `null` when unknown so the caller
 * can render a fallback row that flags the manifest as out-of-band.
 */
export function lookupCapability(id: string): CapabilityInfo | null {
  return BY_ID.get(id) ?? null;
}

/**
 * Synthesise a CapabilityInfo for an id that isn't in the local
 * registry. The review screen renders this as a yellow row labelled
 * "Unrecognised — manual review required". The host registry will
 * reject the install anyway; this just keeps the UI honest in the
 * interim.
 */
export function unknownCapability(id: string): CapabilityInfo {
  const [resource = 'other', action = ''] = id.split('.', 2);
  return {
    id,
    resource,
    action,
    description: 'Unknown capability — not in the host registry.',
    human:
      'This capability is not recognised by the host. The install will be rejected; review the manifest carefully.',
    risk: 'sensitive',
  };
}

/**
 * Resolve a list of capability ids in declaration order. Unknown ids
 * surface via `unknownCapability` so the caller doesn't need to do its
 * own discriminated rendering.
 */
export function resolveCapabilities(ids: readonly string[]): CapabilityInfo[] {
  return ids.map((id) => lookupCapability(id) ?? unknownCapability(id));
}

/** Convenience: return every known capability sorted by id. */
export function allKnownCapabilities(): CapabilityInfo[] {
  return [...BUILTIN_CAPABILITIES].sort((a, b) => a.id.localeCompare(b.id));
}
