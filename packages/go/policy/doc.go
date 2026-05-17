// Package policy is the GoNext authorization engine: roles, capabilities,
// principals, and a single Can() entry point for every authorization
// decision in the system.
//
// This package implements the P0 skeleton of the model described in
// docs/06-auth-permissions.md §6-§7. It is intentionally data-driven and
// in-memory: the database schema for roles/capabilities (§6) and the
// per-resource policy files (§7.3) land in follow-up issues. What's here
// is the type vocabulary and the in-process default policy that the rest
// of the codebase compiles against:
//
//   - Role and Capability — named-string types, so a misplaced literal
//     "admin" stays a build error rather than a runtime miss.
//
//   - The six built-in roles (subscriber, contributor, author, editor,
//     admin, super_admin) and the core capability set covering content,
//     pages, taxonomies, media, users, site, plugins/themes, and
//     comments.
//
//   - The hierarchical default Role -> Capabilities map: admin contains
//     editor contains author contains contributor contains subscriber;
//     super_admin contains admin plus install/site-management caps.
//
//   - Principal, the actor (UserID + Roles), Decision (Allowed + Reason),
//     and Policy.Can(p, capability, resource) — the only sanctioned shape
//     for asking "is this principal allowed to do this thing?".
//
//   - BasicPolicy, the default in-process implementation: unions every
//     role's caps and checks the request.
//
//   - HTTP middleware: Require(p Policy, cap Capability) returns a
//     func(http.Handler) http.Handler that 401s when no principal is on
//     the context and 403s when the principal lacks the capability.
//     Object-level (meta-capability) checks still happen in the service
//     layer; this middleware is the cheap up-front gate.
//
//   - Context helpers: WithPrincipal / FromContext, for the upstream auth
//     middleware to stash the loaded Principal where Require can find it.
//
//   - A plugin-extensibility seam: RegisterCapability(name, description)
//     for plugins that want to declare a new capability slug. The
//     registry is in-memory and lookup-only in P0; enforcement (grants,
//     role assignment, persistence) lands with the DB schema.
//
// Usage:
//
//	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
//	mux.Handle("POST /api/posts",
//	    policy.Require(pol, policy.CapPublishPosts)(publishHandler))
//
//	// In a handler, after loading the resource:
//	p, _ := policy.FromContext(r.Context())
//	d := pol.Can(p, policy.CapEditOthersPosts, post)
//	if !d.Allowed {
//	    http.Error(w, d.Reason, http.StatusForbidden)
//	    return
//	}
//
// House rule: comparing role strings outside this package is a lint
// violation (the custom go-analyzer ships with the DB-backed cut). All
// authorization decisions go through Policy.Can or policy.Require.
//
// See docs/06-auth-permissions.md §6-§7 for the full design.
package policy
