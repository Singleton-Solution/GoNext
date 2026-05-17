// Package jsonschemautil pins every JSON Schema validator in the
// GoNext codebase to the same dialect (draft 2020-12) so plugin, theme,
// and block authors get consistent semantics regardless of which
// validator entry point they hit.
//
// Why this package exists
//
// GoNext compiles JSON Schema documents in several places:
//
//   - packages/go/settings: schemas declared by core and by plugins via
//     the settings registry.
//   - packages/go/theme: the $schema URL declared in theme.json
//     manifests.
//   - packages/go/plugins/manifest (PR #34): the plugin manifest
//     itself, plus the block-attribute sub-schemas declared inside it.
//
// Each of those used to call into santhosh-tekuri/jsonschema with its
// own configuration and silently accept whatever draft the schema
// declared. That meant a draft-07 schema and a 2020-12 schema with the
// same shape would behave differently on edge cases (unevaluated*,
// prefixItems, $dynamicRef), and authors couldn't rely on the keywords
// docs/02-plugin-system.md §7.7 promises them.
//
// This package centralizes the decision:
//
//   - Every validator goes through Compile (or NewCompiler), which sets
//     Draft = jsonschema.Draft2020 unconditionally.
//   - Top-level documents that declare a different $schema are rejected
//     at registration time with a clear, author-facing error.
//   - Documents that omit $schema are accepted — the compiler treats
//     them as 2020-12 per the pinned default. ValidateDialect makes the
//     "must match the pinned draft" rule explicit so each call site can
//     decide whether to reject or normalize.
//
// Tests in every consumer package call into Compile/ValidateDialect so
// a future maintainer who wants to relax the dialect rule has to touch
// the same place that documents it.
package jsonschemautil

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// Draft2020URI is the canonical JSON Schema 2020-12 dialect identifier.
// Every top-level schema GoNext compiles must declare this string in
// its "$schema" field (or omit the field, in which case the compiler
// applies it as the default). The constant is exported so call sites
// can use it instead of re-typing the URL — a single typo somewhere
// would otherwise produce two-draft mismatches that are hard to spot
// in review.
//
// The "draft" wording is a misnomer carried over from earlier
// specs — 2020-12 is the stable release, not a draft. We keep the URL
// the spec ships with.
const Draft2020URI = "https://json-schema.org/draft/2020-12/schema"

// ErrUnsupportedDialect is returned by Compile and ValidateDialect when
// the schema declares a $schema URL that isn't Draft2020URI. The error
// message includes the offending URL so the install/registration flow
// can surface a useful "your plugin declared draft-07; rewrite under
// 2020-12" message to the author.
//
// Callers wrap this with a package-local "invalid schema" sentinel
// (e.g. settings.ErrInvalidSchema) so HTTP handlers can still produce a
// uniform 400 response without parsing the dialect-specific message.
var ErrUnsupportedDialect = errors.New("jsonschemautil: unsupported JSON Schema dialect (only draft 2020-12 is accepted)")

// NewCompiler returns a *jsonschema.Compiler pre-configured for the
// pinned dialect. Use this anywhere you'd otherwise call
// jsonschema.NewCompiler() directly — it keeps the Draft assignment
// out of every call site so a future bump (e.g. to a future 2025 draft)
// is a one-line change here, not a treasure hunt across the repo.
//
// The returned compiler is not safe for concurrent compilation — that
// is the underlying library's contract. Construct one compiler per
// AddResource/Compile pair.
func NewCompiler() *jsonschema.Compiler {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	return c
}

// ValidateDialect inspects the top-level "$schema" field of a raw JSON
// Schema document. If the field is present and not Draft2020URI, it
// returns an ErrUnsupportedDialect-wrapped error naming the offending
// URL; if absent, it returns nil (the compiler will apply the pinned
// default).
//
// Use this before calling Compile when you want a separate error path
// for "wrong draft" vs "malformed schema" — for example, a manifest
// installer that wants to abort the install with a dedicated message
// before running the (slower) full compile.
//
// Malformed JSON returns a wrapped json.Unmarshal error — the caller
// should treat that as "invalid schema" of the same severity as an
// unsupported dialect.
func ValidateDialect(raw []byte) error {
	// Empty input is a programming error at the call site, not a
	// dialect issue. We surface a distinct error so the caller doesn't
	// produce a "wrong dialect" message for an actually-missing schema.
	if len(raw) == 0 {
		return errors.New("jsonschemautil: empty schema document")
	}

	// Pull just the $schema field. Decoding the whole document into a
	// generic map would work, but a one-field peek is cheaper and keeps
	// the error path tight if the document is huge.
	var head struct {
		Schema string `json:"$schema"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return fmt.Errorf("jsonschemautil: parse $schema: %w", err)
	}

	// Absent $schema is fine — NewCompiler() pins the default. The
	// alternative ("require explicit $schema") rejects every existing
	// fixture in the repo for no real benefit; the compiler is the
	// authority on dialect, and it's already pinned.
	if head.Schema == "" {
		return nil
	}

	if !IsDraft2020(head.Schema) {
		return fmt.Errorf("%w: declared %q", ErrUnsupportedDialect, head.Schema)
	}
	return nil
}

// IsDraft2020 reports whether s equals Draft2020URI. Trailing slashes
// and a leading "http://" (instead of https) sometimes slip in from
// authors who hand-edit the URL — we accept the canonical form only.
// A normalizing match would mask the typo silently and produce
// confusing downstream errors when the compiler's strict resolver
// disagreed with our laxer check.
func IsDraft2020(s string) bool {
	return strings.TrimSpace(s) == Draft2020URI
}

// Compile validates the dialect of raw, then compiles it under the
// pinned draft. resourceID is the stable URI the schema is registered
// under for $ref resolution — it is NOT dereferenced as a URL. Pass
// something like "https://gonext.local/settings/<key>.json" so two
// schemas with the same shape but different keys don't collide.
//
// Errors come back wrapped:
//
//   - dialect mismatch: ErrUnsupportedDialect (callers can errors.Is
//     against it to produce a dedicated "wrong draft" error in HTTP
//     responses).
//   - parse / compile failure: a plain error with a descriptive prefix.
//
// The returned *jsonschema.Schema is reusable and safe for concurrent
// Validate calls — that is the underlying library's documented
// contract.
func Compile(resourceID string, raw []byte) (*jsonschema.Schema, error) {
	if err := ValidateDialect(raw); err != nil {
		return nil, err
	}
	c := NewCompiler()
	if err := c.AddResource(resourceID, strings.NewReader(string(raw))); err != nil {
		return nil, fmt.Errorf("jsonschemautil: add resource: %w", err)
	}
	schema, err := c.Compile(resourceID)
	if err != nil {
		return nil, fmt.Errorf("jsonschemautil: compile: %w", err)
	}
	return schema, nil
}
