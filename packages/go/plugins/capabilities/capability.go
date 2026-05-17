package capabilities

import "errors"

// CapabilityDef is the host-side descriptor for one plugin capability.
//
// CapabilityDefs are constructed by the host at init time, registered into
// a Registry, and never mutated afterwards. The plugin's manifest declares
// which CapabilityDef.ID values it needs; the lifecycle install path
// resolves those names against the Registry and rejects manifests that
// reference unknown IDs. The plugin itself cannot register new defs —
// that's a host privilege, the whole point of the registry as a trust
// boundary.
//
// ID is the wire-level slug ("posts.read", "http.fetch"). Manifests carry
// IDs verbatim, the WASM ABI receives them verbatim, audit rows record
// them verbatim. Choose IDs carefully: they are an external contract.
//
// Resource and Action are a structured decomposition of the ID intended
// for admin-UI grouping and policy-like filtering ("show me every cap
// that touches posts"). They are advisory — enforcement keys off the
// opaque ID — but every built-in def fills them in for consistency.
//
// Sensitive flags caps that warrant extra scrutiny in the install UX:
// the operator should see a prominent warning before granting them. The
// flag does not change runtime enforcement; the Checker treats sensitive
// and non-sensitive caps identically. It is metadata for the human in
// the loop.
type CapabilityDef struct {
	// ID is the canonical slug, e.g. "posts.write". Used as the map key
	// in the Registry, the manifest declaration, and the wire format for
	// audit emission. Must be non-empty for Register to accept the def.
	ID string

	// Description is a short, human-readable label for admin UIs.
	// Conventionally verb-led from the plugin's perspective: "Read post
	// rows", "Send outbound transactional email".
	Description string

	// Resource is the noun the capability touches, e.g. "posts", "kv",
	// "http". Used for grouping in the install-confirmation screen.
	Resource string

	// Action is the verb, e.g. "read", "write", "send", "fetch". Paired
	// with Resource it reconstructs the ID — but enforcement never relies
	// on that reconstruction.
	Action string

	// Sensitive marks caps that the operator should review carefully
	// (outbound network, outbound email, user-data read). Purely
	// informational; runtime enforcement is identical for sensitive and
	// non-sensitive caps.
	Sensitive bool
}

// ErrCapabilityDenied is returned by Checker.MustAllow when the plugin
// does not hold the requested capability. The error wraps the cap ID so
// callers can extract it via errors.Unwrap or test specific denials via
// errors.Is on a deniedError built with the same ID.
//
// The sentinel-plus-wrapper shape is chosen so the WASM ABI layer can
// `errors.Is(err, capabilities.ErrCapabilityDenied)` to map every denial
// to the same trap code, while a curious admin tool can pull the cap ID
// off the wrapped error with Cause().
var ErrCapabilityDenied = errors.New("capabilities: denied")

// deniedError carries the cap ID alongside the ErrCapabilityDenied
// sentinel. The Is method makes errors.Is recognize both the sentinel
// itself and a deniedError built from the same ID, so a caller that
// wants to react to one specific cap can write
//
//	if errors.Is(err, capabilities.Denied("http.fetch")) { ... }
//
// without parsing the message string.
type deniedError struct {
	id string
}

// Error formats the denial as a human-readable string. The wire format
// for audit rows is the cap ID alone (set in audit.go); this string is
// purely for log lines and Go error chains.
func (e *deniedError) Error() string {
	return "capabilities: plugin lacks capability " + e.id
}

// Unwrap returns the sentinel so errors.Is(err, ErrCapabilityDenied) is
// always true for denial errors.
func (e *deniedError) Unwrap() error { return ErrCapabilityDenied }

// Is recognizes other deniedError values with the same ID. The sentinel
// fallthrough is handled by Unwrap so we don't need to check it here.
func (e *deniedError) Is(target error) bool {
	other, ok := target.(*deniedError)
	if !ok {
		return false
	}
	return e.id == other.id
}

// ID returns the cap ID this denial refers to. Useful for log
// formatting that wants to render the cap explicitly without parsing
// the message string.
func (e *deniedError) ID() string { return e.id }

// Denied builds a deniedError for the given cap ID. Exported so callers
// can write errors.Is(err, capabilities.Denied("posts.write")) to match
// one specific denial. The returned error also satisfies
// errors.Is(_, ErrCapabilityDenied) via Unwrap.
func Denied(id string) error { return &deniedError{id: id} }
