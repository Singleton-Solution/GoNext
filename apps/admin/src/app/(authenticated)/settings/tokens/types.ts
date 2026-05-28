/**
 * On-wire shapes for /me/tokens. These mirror the Go IssuedTokenView /
 * TokenView structs (apps/api/internal/auth/pat/handler.go). Keep them
 * in lock-step — a mismatched field is a runtime decode failure, not a
 * type error.
 */

export interface TokenView {
  id: string;
  name: string;
  prefix: string;
  scopes: readonly string[];
  created_at: string;
  last_used_at?: string | null;
  expires_at?: string | null;
}

export interface IssuedTokenView extends TokenView {
  /**
   * The full plaintext token. Returned EXACTLY ONCE, in the issue
   * response. The TokenReveal component is responsible for surfacing
   * this prominently and gating its dismissal behind a confirmation.
   */
  plaintext: string;
  /**
   * The intersection of the requested scopes with the user's effective
   * capability set. The token can do no more than this set, regardless
   * of what the operator picked.
   */
  effective_scopes: readonly string[];
  /**
   * Sentinel flag: true means the plaintext is non-recoverable. The UI
   * SHOULD render an unmissable warning when this is true. The handler
   * always sets it to true today; the flag exists so a future "preview"
   * surface can issue tokens with save_now=false without changing the
   * client.
   */
  save_now: boolean;
}

/**
 * Expiration presets supported by the API. Match the Go presetExpiry
 * switch in handler.go; adding a new preset requires both ends.
 */
export type ExpiresPreset = 'never' | '30d' | '90d' | '1y';

export interface IssueRequest {
  name: string;
  scopes: string[];
  expires_in: ExpiresPreset;
}

/**
 * The canonical scope options shown in the new-token UI. The list is
 * intentionally a curated subset of the capability registry — every
 * shipped scope must have an explanation an operator can read in five
 * seconds. Adding a new capability to the registry does NOT automatically
 * surface it here; that's a deliberate design choice (the multi-select
 * exists to make scope decisions explicit, not exhaustive).
 */
export interface ScopeOption {
  slug: string;
  label: string;
  description: string;
}

export const SCOPE_OPTIONS: readonly ScopeOption[] = [
  {
    slug: 'read',
    label: 'Read public content',
    description: 'Read published posts, pages, and media. Safe for read-only CI.',
  },
  {
    slug: 'edit_posts',
    label: 'Edit posts',
    description: 'Create, update, and delete the operator’s own posts.',
  },
  {
    slug: 'edit_published_posts',
    label: 'Edit published posts',
    description: 'Modify already-published posts. Pair with edit_posts.',
  },
  {
    slug: 'publish_posts',
    label: 'Publish posts',
    description: 'Move a draft to published. Common in scheduled-release pipelines.',
  },
  {
    slug: 'upload_files',
    label: 'Upload media',
    description: 'Upload images and other attachments to the media library.',
  },
  {
    slug: 'list_users',
    label: 'List users',
    description: 'Read the user directory (no edits).',
  },
  {
    slug: 'manage_options',
    label: 'Manage site options',
    description: 'High-trust: change site-wide settings. Restrict to admin pipelines.',
  },
  {
    slug: 'system_read',
    label: 'Read system status',
    description: 'Read-only access to /admin/status (DB/Redis/queue health, build info).',
  },
];
