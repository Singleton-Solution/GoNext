package policy

// Principal is whatever is acting on the system — most often the
// authenticated user, but the same shape is reused for service tokens
// and (in a future cut) plugin code. The auth middleware constructs the
// Principal at request start and stashes it on the context via
// WithPrincipal; downstream code reads it back with FromContext.
//
// UserID is a stable opaque string (e.g., "user:17"). The format is the
// caller's choice; policy makes no assumptions about it beyond using it
// as a key in audit decisions.
//
// Roles is the principal's roles. Capabilities are derived from this
// list by the Policy implementation (BasicPolicy unions DefaultRoleCapabilities
// entries); we keep the roles, not the resolved set, on the Principal so
// that a single Principal can be re-evaluated against different policies.
type Principal struct {
	UserID string
	Roles  []Role
}

// Decision is the result of a Can() call. It always carries a reason so
// callers can surface "why" to the user (the reason is human-readable
// and safe to render — no internal IDs leak through).
//
// The bare two-field shape here is the P0 surface; the DB-backed cut
// will extend Decision with Code and NeededCapability per
// docs/06-auth-permissions.md §7.2 without breaking the Allowed/Reason
// callers already depend on.
type Decision struct {
	Allowed bool
	Reason  string
}

// Policy is the only sanctioned shape for asking "may this principal
// perform this capability against (optionally) this resource?".
//
// Implementations:
//
//   - BasicPolicy: in-memory, role->cap union, no object-level checks.
//     This is the P0 default that the rest of the codebase compiles
//     against.
//
//   - The DB-backed cut (future issue) will plug into the same interface,
//     reading user_roles + role_capabilities and dispatching to per-
//     resource policy functions for meta-cap mapping.
//
// resource is intentionally `any` — primitive capability checks pass
// nil; object-level checks pass the loaded resource (a *Post, *User,
// etc.) so the underlying implementation can apply ownership/state rules.
// BasicPolicy ignores resource because it doesn't yet implement meta-cap
// mapping.
type Policy interface {
	Can(p Principal, capability Capability, resource any) Decision
}

// BasicPolicy is the default in-process Policy implementation. It owns a
// Role -> CapabilitySet map (typically DefaultRoleCapabilities()) and
// resolves Can(p, c, _) by unioning the cap sets of p's roles and
// checking whether the requested capability is in the union.
//
// BasicPolicy is intentionally read-only after construction: the map
// passed in is treated as the source of truth and not mutated. If the
// caller wants to override a role's caps for tests, they pass a tweaked
// map to NewBasicPolicy.
//
// The DB-backed Policy (future) will replace BasicPolicy in production
// while keeping this one as the in-memory default for unit tests.
type BasicPolicy struct {
	roleCaps map[Role]CapabilitySet
}

// NewBasicPolicy builds a BasicPolicy from the given role-capability
// map. The map is stored by reference; callers should not mutate it
// after handing it off. Pass DefaultRoleCapabilities() for the standard
// set, or a fresh map for tests.
func NewBasicPolicy(roleCaps map[Role]CapabilitySet) *BasicPolicy {
	if roleCaps == nil {
		roleCaps = map[Role]CapabilitySet{}
	}
	return &BasicPolicy{roleCaps: roleCaps}
}

// Can implements Policy. The decision rule is simple: union the
// capabilities of every role on the principal and check membership.
// A principal with no roles is denied (the union is empty); an unknown
// role is treated as having no capabilities (rather than panicking)
// because role lifecycle is the auth layer's problem, not the policy
// layer's. The resource argument is currently unused — BasicPolicy is
// the primitive-capability cut; the DB-backed Policy adds object-level
// rules.
func (b *BasicPolicy) Can(p Principal, capability Capability, _ any) Decision {
	if len(p.Roles) == 0 {
		return Decision{
			Allowed: false,
			Reason:  "principal has no roles",
		}
	}

	for _, r := range p.Roles {
		set, ok := b.roleCaps[r]
		if !ok {
			continue
		}
		if set.Has(capability) {
			return Decision{
				Allowed: true,
				Reason:  "role " + string(r) + " grants " + string(capability),
			}
		}
	}

	return Decision{
		Allowed: false,
		Reason:  "no role on principal grants " + string(capability),
	}
}

// Capabilities returns the union of capabilities held by the principal
// under this policy. Order is not guaranteed. The set is a fresh copy
// safe for the caller to retain or mutate.
//
// This is exposed for the auth middleware's per-request resolution and
// for diagnostics ("what can this user do?"). Hot-path code should use
// Can directly.
func (b *BasicPolicy) Capabilities(p Principal) CapabilitySet {
	out := make(CapabilitySet)
	for _, r := range p.Roles {
		set, ok := b.roleCaps[r]
		if !ok {
			continue
		}
		for c := range set {
			out[c] = struct{}{}
		}
	}
	return out
}
