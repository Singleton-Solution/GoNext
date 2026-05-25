package importer

import (
	"fmt"
	"strings"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// RoleMapper translates WordPress role slugs to GoNext Role values.
//
// WP ships with a fixed set of built-in roles (administrator, editor,
// author, contributor, subscriber) plus the multisite super_admin
// capability flag. Many sites also install custom roles via plugins
// like Members, Capability Manager Enhanced, or User Role Editor.
// We can't preserve WP's capability model verbatim because GoNext's
// is shaped differently (see packages/go/policy), so the mapping is
// the migration contract: the operator picks a GoNext role per WP
// role, and that role's capability set takes over at runtime.
//
// The Map method is safe to call concurrently after construction —
// the underlying tables are read-only. RegisterOverride mutates the
// override map and is NOT concurrency-safe; call it during setup
// before any Map calls.
//
// The zero value of RoleMapper is not useful; construct via
// NewRoleMapper.
type RoleMapper struct {
	// builtins is the fixed table of WP-built-in slugs → GoNext
	// roles. Construction-time only; never mutated.
	builtins map[string]policy.Role

	// overrides is the operator-supplied table (from --role-map
	// flags). Looked up before builtins, so an operator can override
	// even a built-in mapping if they want. Keyed by lowercased
	// WP slug.
	overrides map[string]policy.Role

	// warn is invoked when Map encounters a slug it has no exact
	// mapping for and has to fall back. nil-safe.
	warn func(format string, args ...any)
}

// NewRoleMapper returns a RoleMapper preloaded with the WordPress
// built-in role table. The warn callback (typically a log.Printf
// wrapper) is invoked when an unknown WP role is encountered; pass
// nil to silence.
func NewRoleMapper(warn func(format string, args ...any)) *RoleMapper {
	return &RoleMapper{
		builtins:  defaultBuiltinRoleMap(),
		overrides: map[string]policy.Role{},
		warn:      warn,
	}
}

// defaultBuiltinRoleMap returns the canonical WP → GoNext mapping
// for the six WP built-ins. Returned fresh each call so callers
// can use it as a starting point for a custom table.
//
// WP's super_admin is a multisite capability bit rather than a role
// in the wp_usermeta sense; we accept it as a slug for completeness.
// administrator → admin: GoNext drops the "-istrator" suffix.
func defaultBuiltinRoleMap() map[string]policy.Role {
	return map[string]policy.Role{
		"super_admin":   policy.RoleSuperAdmin,
		"administrator": policy.RoleAdmin,
		"editor":        policy.RoleEditor,
		"author":        policy.RoleAuthor,
		"contributor":   policy.RoleContributor,
		"subscriber":    policy.RoleSubscriber,
	}
}

// RegisterOverride records an operator-supplied mapping. The key is
// the WP role slug as it appears in wp_usermeta (e.g. "shop_manager"
// for WooCommerce); the value is the GoNext role to assign. An
// override for a built-in slug wins over the built-in mapping.
//
// Returns an error if the GoNext role is not one of the built-in
// slugs registered with the policy package. This is intentionally
// strict: silently accepting a typoed role name would leave migrated
// users with no usable role at runtime.
func (m *RoleMapper) RegisterOverride(wpSlug string, target policy.Role) error {
	wpSlug = strings.ToLower(strings.TrimSpace(wpSlug))
	if wpSlug == "" {
		return fmt.Errorf("role mapper: empty wp slug")
	}
	if !isBuiltinRole(target) {
		return fmt.Errorf("role mapper: %q is not a recognised GoNext role", target)
	}
	m.overrides[wpSlug] = target
	return nil
}

// ParseOverrideFlag accepts the --role-map CLI form ("old=new") and
// registers an override. Returns an error on malformed input or an
// unknown target role.
//
// The "old" half is the WP role slug; the "new" half is one of the
// GoNext role slugs (subscriber, contributor, author, editor, admin,
// super_admin). Whitespace around either half is trimmed.
//
// Multiple --role-map flags are supported by calling this once per
// flag value.
func (m *RoleMapper) ParseOverrideFlag(spec string) error {
	eq := strings.IndexByte(spec, '=')
	if eq < 0 {
		return fmt.Errorf("role mapper: bad --role-map %q (want old=new)", spec)
	}
	old := strings.TrimSpace(spec[:eq])
	newSlug := strings.TrimSpace(spec[eq+1:])
	if old == "" || newSlug == "" {
		return fmt.Errorf("role mapper: bad --role-map %q (empty half)", spec)
	}
	return m.RegisterOverride(old, policy.Role(newSlug))
}

// Map returns the GoNext role for a WP role slug.
//
// The lookup order is:
//   1. Operator overrides (registered via RegisterOverride / ParseOverrideFlag).
//   2. Built-in WP-to-GoNext table.
//   3. Heuristic fallback by name fragment (e.g. "shop_manager" → editor
//      because it contains "manager"; "shop_subscriber" → subscriber).
//   4. RoleSubscriber as the final safe default, with a warn() call.
//
// The boolean return signals whether the mapping was an exact match
// (true) or a fallback (false). Callers that want to refuse fallback
// mappings (strict mode) can branch on it.
func (m *RoleMapper) Map(wpSlug string) (policy.Role, bool) {
	original := wpSlug
	wpSlug = strings.ToLower(strings.TrimSpace(wpSlug))
	if wpSlug == "" {
		return policy.RoleSubscriber, false
	}
	if r, ok := m.overrides[wpSlug]; ok {
		return r, true
	}
	if r, ok := m.builtins[wpSlug]; ok {
		return r, true
	}
	// Heuristic. Sorted from most to least privileged so a slug
	// like "site_administrator" still lands on admin not editor.
	switch {
	case strings.Contains(wpSlug, "super") && strings.Contains(wpSlug, "admin"):
		m.warnf("unknown wp role %q mapped to super_admin by heuristic", original)
		return policy.RoleSuperAdmin, false
	case strings.Contains(wpSlug, "admin"):
		m.warnf("unknown wp role %q mapped to admin by heuristic", original)
		return policy.RoleAdmin, false
	case strings.Contains(wpSlug, "editor"), strings.Contains(wpSlug, "manager"):
		m.warnf("unknown wp role %q mapped to editor by heuristic", original)
		return policy.RoleEditor, false
	case strings.Contains(wpSlug, "author"):
		m.warnf("unknown wp role %q mapped to author by heuristic", original)
		return policy.RoleAuthor, false
	case strings.Contains(wpSlug, "contributor"):
		m.warnf("unknown wp role %q mapped to contributor by heuristic", original)
		return policy.RoleContributor, false
	}
	m.warnf("unknown wp role %q falling back to subscriber", original)
	return policy.RoleSubscriber, false
}

// warnf is the nil-safe wrapper around the warn callback.
func (m *RoleMapper) warnf(format string, args ...any) {
	if m == nil || m.warn == nil {
		return
	}
	m.warn(format, args...)
}

// isBuiltinRole reports whether r is one of the policy.Role
// constants. Used to validate operator-supplied targets.
func isBuiltinRole(r policy.Role) bool {
	for _, b := range policy.BuiltinRoles() {
		if r == b {
			return true
		}
	}
	return false
}
