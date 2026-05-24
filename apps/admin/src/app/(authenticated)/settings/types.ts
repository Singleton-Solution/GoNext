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
}

/**
 * Response shape of `GET /api/v1/settings?group=<group>`. Values are unknown
 * because the registry stores heterogeneous types; the form coerces them at
 * the input boundary.
 */
export type SettingsValues = Record<string, unknown>;

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
