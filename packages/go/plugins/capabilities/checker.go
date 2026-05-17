package capabilities

import (
	"context"
	"sort"
	"sync"
)

// GrantSet is the set of cap IDs granted to a single plugin instance.
//
// Construction is host-driven: the install path resolves the plugin's
// manifest against a Registry, intersects with the operator's grant
// decisions, and produces the GrantSet that gets handed to the
// Checker. Plugins themselves do not modify their own grants — the set
// is captured at instantiation and held immutably for the lifetime of
// the WASM module.
//
// The zero value is an empty (deny-all) set, which is the safe default
// for a plugin that has been instantiated before its grants were
// resolved.
type GrantSet map[string]struct{}

// NewGrantSet returns a GrantSet containing exactly the given cap IDs.
// Duplicates are folded.
func NewGrantSet(ids ...string) GrantSet {
	g := make(GrantSet, len(ids))
	for _, id := range ids {
		g[id] = struct{}{}
	}
	return g
}

// Has reports whether the set contains the cap ID. O(1).
func (g GrantSet) Has(id string) bool {
	_, ok := g[id]
	return ok
}

// IDs returns the cap IDs in the set, sorted for determinism. Useful
// for audit metadata and admin-UI rendering.
func (g GrantSet) IDs() []string {
	out := make([]string, 0, len(g))
	for id := range g {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Checker enforces the cap-grant contract on every host-call entry
// point.
//
// One Checker per plugin instantiation: the WASM runtime holds it for
// the lifetime of the module and consults it on every ABI call. The
// granted GrantSet is captured by value (well, by map reference, but
// the contract is "don't mutate") at construction time and frozen
// thereafter.
//
// The reg field exists for two reasons. First, MustAllow can emit a
// distinct audit payload depending on whether the denied cap is even a
// known one — an unknown cap usually means a plugin built against a
// newer host ABI is running on an older host, which is a different
// operational concern from "the operator denied this cap at install".
// Second, holding the reg makes Allowed correctly return false for cap
// IDs that aren't even registered, regardless of what's in the grant
// set (defense in depth: a corrupted grant set listing a phantom cap
// ID should not allow anything).
type Checker struct {
	reg     *Registry
	granted GrantSet

	// emitter is the audit hook called on every denial. Nil disables
	// audit emission, which is the right default for unit tests that
	// don't want to wire up a store. Production code always installs
	// one via WithAuditEmitter.
	emitter auditEmitter

	// mu guards granted from concurrent mutation if a caller decides
	// to bolt on grant revocation later. v1 treats granted as
	// effectively immutable; the mutex is here so a future revocation
	// patch doesn't require a public-API change.
	mu sync.RWMutex
}

// CheckerOption configures a Checker at construction time.
type CheckerOption func(*Checker)

// WithAuditEmitter installs the audit hook called on every denial.
// Pass an *audit.Emitter pre-bound to the plugin slug (via
// emitter.WithPlugin(slug)); the Checker calls Emit directly without
// re-wrapping. Nil is tolerated and means "no audit emission" —
// suitable for tests but not for production wiring.
func WithAuditEmitter(e auditEmitter) CheckerOption {
	return func(c *Checker) { c.emitter = e }
}

// NewChecker builds a Checker bound to the given Registry and
// per-plugin GrantSet.
//
// reg is required: a nil registry would make every Allowed check ignore
// the registered-cap invariant, which is exactly the kind of silent
// failure the registry exists to prevent. Passing nil panics.
//
// granted is the set of cap IDs the operator approved for this plugin
// instance, intersected with what the manifest declared and what the
// registry knows about. A nil GrantSet is equivalent to an empty one
// (deny everything).
func NewChecker(reg *Registry, granted GrantSet, opts ...CheckerOption) *Checker {
	if reg == nil {
		panic("capabilities.NewChecker: registry is required")
	}
	if granted == nil {
		granted = GrantSet{}
	}
	c := &Checker{
		reg:     reg,
		granted: granted,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Allowed reports whether the plugin holds the given capability.
//
// The check is the intersection of two predicates: the cap must be
// registered (a phantom ID in the grant set means nothing), and the
// cap must be in the grant set. Both are required; either failure
// returns false.
//
// Allowed does NOT emit audit events — it's the predicate form of the
// check, used by ABI surfaces that want to silently no-op a missing
// cap rather than trap. Use MustAllow when a denial should be audited
// and surfaced as an error to the WASM caller.
//
// Safe for concurrent use.
func (c *Checker) Allowed(id string) bool {
	if !c.reg.Has(id) {
		return false
	}
	c.mu.RLock()
	_, ok := c.granted[id]
	c.mu.RUnlock()
	return ok
}

// MustAllow returns nil if the plugin holds the capability and
// ErrCapabilityDenied (wrapped with the cap ID) otherwise. On denial,
// it emits a `capability_denied` audit row through the Checker's
// emitter (if one is installed) before returning.
//
// The error shape is deliberate: callers in the WASM ABI layer can
// `errors.Is(err, ErrCapabilityDenied)` to map every denial to the
// same trap code, while a curious admin tool can extract the specific
// cap ID via the wrapped *deniedError. See capability.go's Denied()
// helper for matching specific cap denials.
//
// ctx is forwarded to the audit emitter. If ctx is cancelled, audit
// emission may fail — the denial is still returned as the function's
// primary result; audit failure is logged by the emitter, not raised
// here. The denial is the authoritative signal; audit is best-effort.
//
// Safe for concurrent use.
func (c *Checker) MustAllow(ctx context.Context, id string) error {
	if c.Allowed(id) {
		return nil
	}
	// Denial path. Record the audit row before returning so log /
	// audit ordering matches caller expectations.
	c.emitDenial(ctx, id)
	return Denied(id)
}

// Granted returns the cap IDs the Checker holds, sorted. Useful for
// audit metadata and admin-UI rendering. The returned slice is a
// fresh copy.
func (c *Checker) Granted() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.granted.IDs()
}
