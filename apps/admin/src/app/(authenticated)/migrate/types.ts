/**
 * Wizard state and step types.
 *
 * Held entirely client-side (no URL persistence) because a migration
 * is one-shot and never deep-linked. If the user reloads, they restart
 * — that's the expected posture: a half-configured wizard would
 * obscure whether the in-flight job was their own or someone else's.
 */

/** WizardStep enumerates the 5 wizard surfaces in display order. */
export type WizardStep =
  | 'source'
  | 'options'
  | 'preview'
  | 'run'
  | 'report';

/** Ordered list used by the stepper progress UI. */
export const WIZARD_STEPS: readonly WizardStep[] = [
  'source',
  'options',
  'preview',
  'run',
  'report',
] as const;

/**
 * SourceKind selects which input the importer reads. The wizard
 * shows the matching form per kind in step 1.
 */
export type SourceKind = 'wxr-upload' | 'rest-url' | 'acf-json';

/** Configuration captured in step 1. */
export interface SourceConfig {
  kind: SourceKind;
  /** Set when kind === 'wxr-upload'. File handle held in memory. */
  wxrFile?: File | null;
  /** Set when kind === 'rest-url'. Full origin including https://. */
  restUrl?: string;
  /** Set when kind === 'acf-json'. Server-relative path. */
  acfPath?: string;
}

/**
 * MediaMode selects how the importer handles attachments:
 *   - copy:  download every referenced file into GoNext media storage
 *   - proxy: leave files on the source server and rewrite URLs only
 *
 * Issue #187 introduced the proxy mode as a low-blast-radius default
 * for staging environments; production migrations usually want copy.
 */
export type MediaMode = 'copy' | 'proxy';

/**
 * ShortcodeMode controls what the importer does with WP shortcodes
 * embedded in content:
 *   - keep:    leave the raw [shortcode] markers; the renderer will
 *              ignore them (visible as plain text).
 *   - strip:   delete shortcode tokens, preserving any inner content.
 *   - convert: best-effort mapping to core blocks (only a few are
 *              known; unknown shortcodes fall through to 'keep').
 */
export type ShortcodeMode = 'keep' | 'strip' | 'convert';

/** Configuration captured in step 2. */
export interface OptionsConfig {
  mediaMode: MediaMode;
  shortcodeMode: ShortcodeMode;
  /**
   * Role overrides remap WP role slugs to GoNext role slugs. The
   * default mapping (administrator → admin, editor → editor, ...)
   * lives in the importer; this map only carries operator-provided
   * exceptions.
   */
  roleOverrides: Record<string, string>;
}

/**
 * DryRunResult is the data returned by step 3's preview call. The
 * shape mirrors the importer's Report struct, plus a list of
 * warnings the operator should review before committing.
 */
export interface DryRunResult {
  authors: number;
  categories: number;
  tags: number;
  posts: number;
  attachments: number;
  comments: number;
  warnings: string[];
}

/**
 * RunStatus represents a single poll of the running import job.
 * The wizard polls roughly every 2s until status==='done' or
 * status==='failed'.
 */
export interface RunStatus {
  jobId: string;
  status: 'queued' | 'running' | 'done' | 'failed';
  /** Percent complete, 0..100. Server-supplied; the UI clamps to 100. */
  percent: number;
  /** Last-known phase label, e.g. 'authors', 'posts', 'media'. */
  phase: string;
  /** Counts grow monotonically as the job advances. */
  counts: DryRunResult;
  /** Non-fatal per-record errors gathered so far. */
  errors: string[];
}

/**
 * Defaults returned by the wizard's reset() helper so the same
 * literal is available to both the initial state and to "start over"
 * after the report step.
 */
export const DEFAULT_OPTIONS: OptionsConfig = {
  mediaMode: 'copy',
  shortcodeMode: 'convert',
  roleOverrides: {},
};
