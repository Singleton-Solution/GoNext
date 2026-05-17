package policy

// Role is a named string identifying a role slug. The named type means a
// caller cannot accidentally pass a literal "admin" string anywhere a
// Role is expected — they have to use one of the constants below (or
// a custom Role minted from a registered slug). That property is what
// the role-comparison lint rule relies on.
type Role string

// Built-in role slugs, in increasing privilege. These match the slugs
// seeded into the roles table by the migration; see
// docs/06-auth-permissions.md §6.1.
//
// super_admin is reserved for v2 multisite. In v1 it is identical to
// admin plus the install/site-management capabilities.
const (
	RoleSubscriber  Role = "subscriber"
	RoleContributor Role = "contributor"
	RoleAuthor      Role = "author"
	RoleEditor      Role = "editor"
	RoleAdmin       Role = "admin"
	RoleSuperAdmin  Role = "super_admin"
)

// BuiltinRoles returns the canonical list of built-in roles in
// increasing privilege order. Returned slice is a fresh copy so callers
// can sort/filter freely.
func BuiltinRoles() []Role {
	return []Role{
		RoleSubscriber,
		RoleContributor,
		RoleAuthor,
		RoleEditor,
		RoleAdmin,
		RoleSuperAdmin,
	}
}
