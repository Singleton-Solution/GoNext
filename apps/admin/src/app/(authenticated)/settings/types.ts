/**
 * Shared types for the admin settings UI.
 *
 * Mirrors the settings registry surface shipped in #325. Each setting in the
 * registry has a typed value; the admin form maps that type onto a concrete
 * input control. Keep this list narrow: when a new type lands in the registry,
 * extend `SettingType` here and `renderField()` in `SettingsForm`.
 */
export type SettingType = 'text' | 'url' | 'number' | 'boolean' | 'select';

export interface SettingOption {
  /** Submitted to the API. */
  value: string;
  /** Shown in the `<option>`. */
  label: string;
}

/**
 * Declarative description of one editable setting. `SettingsForm` consumes a
 * `Setting[]` array and renders a labelled input per entry, validates on submit
 * based on `type` + `required`, and emits a single `Record<string, unknown>`
 * patch payload.
 */
export interface Setting {
  /** Dotted key, e.g. `core.site.name`. The key the API expects in the PATCH. */
  key: string;
  /** Human-readable label rendered above the input. */
  label: string;
  /** Optional help text rendered below the input. */
  help?: string;
  /** Input type — drives both the control and the local validation. */
  type: SettingType;
  /** Required for `select` controls; ignored otherwise. */
  options?: readonly SettingOption[];
  /** Whether the field must be non-empty before submit is allowed. */
  required?: boolean;
  /** Optional placeholder forwarded to the input. */
  placeholder?: string;
  /**
   * When true, the rendered input uses the mono font stack
   * (Geist Mono). Used by the Permalinks custom-structure field
   * so URL templates read like code.
   */
  mono?: boolean;
}

/**
 * Response shape of `GET /api/v1/settings?group=<group>`. Values are unknown
 * because the registry stores heterogeneous types; the form coerces them at
 * the input boundary.
 */
export type SettingsValues = Record<string, unknown>;

/**
 * Optional grouping for the schema-driven form. Each section renders as
 * its own paper-2 card with a title + optional description, holding the
 * subset of fields whose keys are listed. Pages that do NOT declare
 * sections fall back to a single unsectioned card.
 *
 * Living-Systems brand uses these sections as the primary visual rhythm
 * for forms — see docs/design/HANDOFF.md ("Component patterns to honor").
 */
export interface SettingsSection {
  /** Short, sentence-case title rendered above the fields. */
  title: string;
  /** Optional supporting copy under the title. */
  description?: string;
  /** Keys of `Setting.key` entries that belong to this section. */
  keys: readonly string[];
}

/**
 * Group identifier passed to the API. Matches the registry buckets defined in
 * #325: `core` for site identity, `core.reading` for the reading feature, etc.
 * The admin keeps these literal so route handlers stay grep-able.
 */
export type SettingsGroup =
  | 'core.site'
  | 'core.reading'
  | 'core.writing'
  | 'core.permalinks';
