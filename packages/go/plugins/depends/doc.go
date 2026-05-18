// Package depends resolves inter-plugin dependencies declared in a
// manifest's `depends` array and gates the lifecycle Activate transition.
//
// The package is the second half of the activation contract: the
// manifest validator (plugins/manifest) accepts a `depends` array of
// {name, version-range} pairs at parse time; this package then checks
// that, at the moment an operator clicks "Activate", every entry
// resolves to an installed, currently-Active plugin whose declared
// version sits inside the requested range. If any check fails, the
// Activate path returns a typed error and the row stays where it was —
// no Errored parking, because the plugin itself didn't misbehave; the
// runtime environment just isn't ready for it yet.
//
// # What is and is not gated
//
//   - Install is NOT gated. An operator can install a plugin whose
//     dependencies haven't been installed yet. The row lands in
//     Installed and waits. This matters because operators commonly
//     stage a bundle of plugins before activating any of them.
//
//   - Activate IS gated. Calling lifecycle.Manager.Activate routes
//     through Gate.AllowActivate, which runs the resolver against the
//     current Storage view and refuses the transition if anything is
//     missing, inactive, or version-incompatible.
//
//   - Deactivate is NOT gated by this package. A dependency being
//     removed first will be caught the next time someone tries to
//     re-activate. Future work (issue #251 follow-up) will add a
//     dependent-detection pass on Deactivate to warn operators.
//
// # Resolver inputs
//
// The resolver is decoupled from lifecycle.Storage so the same logic
// can be reused by a CLI dry-run that reads from a registry index, by
// integration tests that synthesize state, and by the production
// lifecycle Manager. Callers wire a Registry function that maps a slug
// to a *PluginRecord (or "not found"); the resolver does the rest.
//
// # Errors
//
// The four typed errors — ErrMissingDependency, ErrInactiveDependency,
// ErrVersionMismatch, ErrCircularDependency — are exported so the admin
// UI and CLI can show structured failure UI instead of grepping error
// strings. Each error carries the offending slugs as a structured slice
// so the renderer can list them.
//
// # Semver
//
// Range parsing uses a deliberately small subset of npm-style operators:
//
//	^1.2.0       — caret: same major, version >= 1.2.0 < 2.0.0
//	~1.2.0       — tilde: same minor, version >= 1.2.0 < 1.3.0
//	>=1.2.0      — minimum
//	<2.0.0       — maximum (exclusive)
//	>=1.0.0 <2.0.0 — composite (space-separated AND)
//	1.2.0        — exact match
//	*            — any version
//
// The underlying comparison goes through golang.org/x/mod/semver, which
// requires the "v" prefix internally; this package transparently adds
// it.  We intentionally avoid pulling in a third-party semver
// library — the operator set above covers every range the platform
// allows in the manifest schema, and the smaller surface area makes the
// resolver auditable.
package depends
