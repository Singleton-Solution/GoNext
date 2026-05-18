// Package schemas formalises the per-hook payload contract for the GoNext
// hook bus.
//
// Why this package exists
//
// Up to and including issue #318 the bus accepted any args... payload and
// passed it through to handlers untouched. That gave us flexibility, but it
// also meant a misbehaving plugin could fire a hook with garbage that broke
// every downstream listener — and worse, listeners had no machine-readable
// way to know what shape they were supposed to receive. Documentation
// stayed in prose, plugin authors guessed, and bugs lived for weeks.
//
// This package fixes that by introducing a JSON Schema-backed contract for
// each hook name:
//
//   - Hosts declare the expected payload shape with [SchemaRegistry.Register].
//   - The bus, when configured via [hooks.Bus.WithSchemas], validates every
//     [hooks.Bus.Do] and [hooks.Bus.ApplyFilters] payload before fan-out.
//   - Consumers that want belt-and-braces (e.g. before persisting a value
//     produced by a filter chain) call [Registry.ValidatePayload] directly.
//
// Pinned dialect
//
// All schemas go through [packages/go/jsonschemautil], which pins compile
// to draft 2020-12. A schema that declares a different $schema URL is
// rejected at [SchemaRegistry.Register] time with a wrapped
// jsonschemautil.ErrUnsupportedDialect. See packages/go/jsonschemautil for
// the policy rationale (issue #275).
//
// Built-in schemas
//
// The package embeds schemas for the WP-compat hooks documented in
// packages/go/hooks/wpcompat — the_content, save_post, user_register, etc.
// Call [BuiltinRegistry] to obtain a [Registry] pre-populated with those
// schemas. Hosts that want a tighter or looser surface can construct an
// empty [NewRegistry] and Register only what they want enforced.
//
// Modes
//
// The bus middleware ([Enforce]) supports two modes:
//
//   - Loose (default): hooks with NO registered schema pass through
//     un-validated. This is the contract for unknown plugin hooks and
//     keeps registration-by-typo from silently disabling validation for
//     everything else.
//   - Strict: any hook without a registered schema is rejected with
//     [ErrUnregisteredHook]. Use this in tests and in hardened production
//     where every hook MUST have a contract.
//
// Concurrency
//
// [SchemaRegistry] is safe for concurrent Register and Validate. A schema
// stored after Register returns is visible to all subsequent Validate calls
// across goroutines. The validator object itself is compiled once per
// Register and reused — santhosh-tekuri/jsonschema guarantees the compiled
// schema is safe for concurrent Validate, which is what makes the read
// path lock-free.
package schemas
