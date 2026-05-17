package settings

import (
	"encoding/json"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// SettingType is the high-level type of a setting's value. It is a hint
// for the admin UI's widget picker — the canonical truth about a
// value's shape lives in the JSON Schema. The two agree by convention,
// not by enforcement; the schema is what Write validates against.
//
// Why a separate Type field at all? Two reasons:
//
//  1. The admin UI needs to pick a widget (text input, number stepper,
//     checkbox, dropdown, multi-select, JSON editor) before it has parsed
//     the schema deeply. The Type is the cheap up-front signal.
//
//  2. The CLI (`gonext option get foo`) needs to know whether to print
//     the value as a string, a number, or pretty-printed JSON. Again, a
//     short string is easier than walking the schema.
type SettingType string

// Built-in SettingType values. Plugins should pick the closest match
// rather than inventing new types — admin widgets are keyed off this set.
const (
	// SettingTypeString is a plain text value. JSON Schema: {"type":
	// "string"}. Use enum/format inside the schema for further
	// constraints (e.g. format:"email", maxLength:80).
	SettingTypeString SettingType = "string"

	// SettingTypeInt is an integer. JSON Schema: {"type": "integer"}.
	// Use minimum/maximum/multipleOf for further constraints.
	SettingTypeInt SettingType = "int"

	// SettingTypeBool is a boolean checkbox-style setting. JSON Schema:
	// {"type": "boolean"}.
	SettingTypeBool SettingType = "bool"

	// SettingTypeEnum is a string with a closed set of values. JSON
	// Schema: {"type": "string", "enum": [...]}. The admin renders this
	// as a dropdown.
	SettingTypeEnum SettingType = "enum"

	// SettingTypeArray is a list of values (most often strings). JSON
	// Schema: {"type": "array", "items": {...}}.
	SettingTypeArray SettingType = "array"

	// SettingTypeObject is a structured value. JSON Schema: {"type":
	// "object", "properties": {...}}. The admin renders this as a
	// grouped form section or a JSON editor depending on the schema's
	// complexity.
	SettingTypeObject SettingType = "object"
)

// Valid reports whether t is one of the recognized SettingType values.
// Plugins that want to add a new type should propose it upstream rather
// than passing an unknown string — the admin UI will not know how to
// render an unfamiliar widget.
func (t SettingType) Valid() bool {
	switch t {
	case SettingTypeString, SettingTypeInt, SettingTypeBool,
		SettingTypeEnum, SettingTypeArray, SettingTypeObject:
		return true
	}
	return false
}

// Setting is the canonical declaration of one configurable knob. It
// pairs metadata (description, group, capability) with the structural
// contract (Type + Schema) and the runtime hook (Validator).
//
// One Setting per key. Registering two Setting values with the same Key
// is a programming error and surfaces from Registry.Register as
// ErrDuplicateKey.
//
// The struct is value-typed because it's read-mostly: the registry hands
// out copies (Setting is small — a handful of strings, a JSON blob, two
// function pointers), and that means the consumer can't accidentally
// mutate the registry by writing into a returned Setting.
type Setting struct {
	// Key is the canonical dotted identifier — e.g. "core.site.name",
	// "core.permalinks.format", "myplugin.feature.enabled". It is the
	// primary key both in this registry and in the options table.
	//
	// Convention: lowercase, dot-separated, namespace-prefixed. Core
	// settings live under "core."; plugin settings under the plugin
	// slug. The prefix is policy, not mechanism — nothing in this
	// package enforces it, but plugin reviewers will reject PRs that
	// register "title" instead of "myplugin.title".
	Key string

	// Description is the admin-UI help text. Keep it short; one line is
	// the target. Markdown is NOT supported in v1 — the admin UI renders
	// this as plain text.
	Description string

	// Type is the high-level type hint. See SettingType godoc for why
	// this exists alongside Schema.
	Type SettingType

	// Schema is the JSON Schema 2020-12 document that Write validates
	// against. It is the canonical truth about value shape.
	//
	// Encoded as json.RawMessage so callers can either:
	//   - Embed a string literal:    Schema: json.RawMessage(`{"type":"string"}`)
	//   - Marshal a Go map:           b, _ := json.Marshal(m); Schema: b
	//   - Read a schema file:         b, _ := os.ReadFile("…"); Schema: b
	//
	// A nil/empty Schema is allowed at the Setting struct level but
	// will cause Registry.Register to return ErrInvalidSchema — every
	// setting must have a schema, even if it's just `{"type":"string"}`.
	Schema json.RawMessage

	// Default is the value used when Store.Read is called for a key
	// that's not yet in the options table. It is NOT validated against
	// Schema at registration time (validating any-value against a JSON
	// Schema requires the compiled schema, and we want the registry to
	// be cheap to populate); registries that want belt-and-braces can
	// call Setting.ValidateDefault() after registering.
	Default any

	// Autoload mirrors the options.autoload column from migration 000008.
	// If true, the value is loaded into memory on every request (via
	// Store.LoadAutoload at boot, then kept in the L1 cache); if false,
	// the value is fetched on demand.
	//
	// Core settings that are read on hot paths (site.name, locale,
	// permalinks.format) should be autoload=true. Bulk plugin settings,
	// log filters, and anything read by a CLI command but not by HTTP
	// handlers should be autoload=false to keep the autoload set
	// bounded — that's the WP anti-pattern this design exists to fix
	// (see docs/01-core-cms.md §10.11).
	Autoload bool

	// Group is the admin-UI section ("general", "reading", "writing",
	// "permalinks", …). The admin renders one form page per Group.
	// Empty is treated as the catch-all group "uncategorized" — Registry
	// will accept it but admin UI reviewers will reject PRs that ship
	// it.
	Group string

	// RequiresCapability is the policy.Capability a user must hold to
	// PUT this setting. Most core settings are gated by
	// policy.CapManageOptions; plugin-specific settings may have their
	// own caps. The HTTP layer reads this at request time and short-
	// circuits with 403 before validation runs.
	RequiresCapability policy.Capability

	// Validator is an optional callback run AFTER JSON Schema validation
	// passes. Use it for cross-field or runtime checks the schema
	// can't express: "this URL must be reachable", "this slug must be
	// unique against the database", "this directory must be writable".
	//
	// Return nil to accept, any non-nil error to reject. The error
	// message bubbles up to the API client unmodified — keep it
	// admin-facing ("URL must be HTTPS in production"), not internal
	// ("net/url: parse error 17").
	Validator func(value any) error
}
