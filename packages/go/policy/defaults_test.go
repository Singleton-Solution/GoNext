package policy

import (
	"sort"
	"testing"
)

// TestDefaultRoleCapabilities_BuiltinRoles asserts that every built-in
// role is present in the default map and has at least one capability.
// A role that defaults to the empty set is almost certainly a bug — even
// subscriber should have read.
func TestDefaultRoleCapabilities_BuiltinRoles(t *testing.T) {
	m := DefaultRoleCapabilities()
	for _, r := range BuiltinRoles() {
		set, ok := m[r]
		if !ok {
			t.Errorf("role %q missing from DefaultRoleCapabilities", r)
			continue
		}
		if len(set) == 0 {
			t.Errorf("role %q has empty capability set", r)
		}
	}
}

// TestDefaultRoleCapabilities_PerRoleSets is the table-driven check that
// each built-in role holds (or specifically lacks) the canonical sample
// capabilities for that role. This is the spec for "what does role X
// mean?" — any change to the default map that breaks one of these rows
// is a deliberate policy change and needs a fresh review.
func TestDefaultRoleCapabilities_PerRoleSets(t *testing.T) {
	m := DefaultRoleCapabilities()

	cases := []struct {
		role        Role
		mustHave    []Capability
		mustNotHave []Capability
	}{
		{
			role:     RoleSubscriber,
			mustHave: []Capability{CapRead},
			mustNotHave: []Capability{
				CapEditPosts, CapPublishPosts, CapManageOptions,
				CapInstallPlugins, CapUploadFiles,
			},
		},
		{
			role:     RoleContributor,
			mustHave: []Capability{CapRead, CapEditPosts, CapDeletePosts},
			mustNotHave: []Capability{
				CapPublishPosts, CapEditOthersPosts, CapUploadFiles,
				CapManageOptions,
			},
		},
		{
			role: RoleAuthor,
			mustHave: []Capability{
				CapRead, CapEditPosts, CapPublishPosts,
				CapEditPublishedPosts, CapDeletePublishedPosts,
				CapUploadFiles,
			},
			mustNotHave: []Capability{
				CapEditOthersPosts, CapEditOthersPages, CapManageOptions,
				CapInstallPlugins,
			},
		},
		{
			role: RoleEditor,
			mustHave: []Capability{
				CapRead, CapEditPosts, CapPublishPosts, CapEditOthersPosts,
				CapEditPrivatePosts, CapReadPrivatePosts,
				CapEditPages, CapPublishPages, CapEditOthersPages,
				CapManageCategories, CapManageTags, CapModerateComments,
				CapEditOthersMedia,
			},
			mustNotHave: []Capability{
				CapManageOptions, CapInstallPlugins, CapEditUsers,
				CapManageInstall,
			},
		},
		{
			role: RoleAdmin,
			mustHave: []Capability{
				// Everything editor has...
				CapEditOthersPosts, CapPublishPages, CapModerateComments,
				// ...plus admin-only.
				CapManageOptions, CapListUsers, CapCreateUsers,
				CapEditUsers, CapDeleteUsers, CapPromoteUsers,
				CapInstallPlugins, CapManagePlugins, CapActivatePlugins,
				CapManagePluginSettings, CapInstallThemes, CapManageThemes,
				CapSwitchThemes, CapEditThemes, CapThemeEditParts,
			},
			mustNotHave: []Capability{
				CapManageInstall, // reserved for super_admin
			},
		},
		{
			role: RoleSuperAdmin,
			mustHave: []Capability{
				// Everything admin has, plus install management.
				CapManageOptions, CapInstallPlugins, CapManageInstall,
			},
			mustNotHave: nil, // super_admin has everything in P0
		},
	}

	for _, tc := range cases {
		t.Run(string(tc.role), func(t *testing.T) {
			set, ok := m[tc.role]
			if !ok {
				t.Fatalf("role %q missing from map", tc.role)
			}
			for _, c := range tc.mustHave {
				if !set.Has(c) {
					t.Errorf("role %q should hold capability %q", tc.role, c)
				}
			}
			for _, c := range tc.mustNotHave {
				if set.Has(c) {
					t.Errorf("role %q should NOT hold capability %q", tc.role, c)
				}
			}
		})
	}
}

// TestDefaultRoleCapabilities_Hierarchy asserts the strict containment
// chain: each row in the chain holds every capability of the row below
// it. This is the "admin ⊇ editor ⊇ author ⊇ contributor ⊇ subscriber;
// super_admin ⊇ admin" invariant from docs/06-auth-permissions.md §6.1.
//
// Stated as a table over (parent, child) pairs so a regression in any
// link of the chain points at the exact pair.
func TestDefaultRoleCapabilities_Hierarchy(t *testing.T) {
	m := DefaultRoleCapabilities()

	chain := []struct {
		parent Role
		child  Role
	}{
		{RoleContributor, RoleSubscriber},
		{RoleAuthor, RoleContributor},
		{RoleEditor, RoleAuthor},
		{RoleAdmin, RoleEditor},
		{RoleSuperAdmin, RoleAdmin},
	}

	for _, link := range chain {
		t.Run(string(link.parent)+"_contains_"+string(link.child), func(t *testing.T) {
			parent, child := m[link.parent], m[link.child]
			for c := range child {
				if !parent.Has(c) {
					t.Errorf("%q is missing %q (held by %q)",
						link.parent, c, link.child)
				}
			}
			// Sanity check: parent should strictly contain child (be a
			// superset, not equal) — otherwise the hierarchy is degenerate.
			if len(parent) <= len(child) {
				t.Errorf("%q (%d caps) does not strictly contain %q (%d caps)",
					link.parent, len(parent), link.child, len(child))
			}
		})
	}
}

// TestDefaultRoleCapabilities_Independence asserts that mutating the
// result of DefaultRoleCapabilities() does not leak into a second call
// — each call returns fresh maps and sets. This protects callers (and
// tests) that tweak the defaults.
func TestDefaultRoleCapabilities_Independence(t *testing.T) {
	a := DefaultRoleCapabilities()
	a[RoleSubscriber] = NewCapabilitySet(CapManageInstall)
	a[RoleEditor].Add("custom_cap_xyz")

	b := DefaultRoleCapabilities()
	if b[RoleSubscriber].Has(CapManageInstall) {
		t.Error("mutation of map-A leaked into map-B (top-level map shared)")
	}
	if b[RoleEditor].Has("custom_cap_xyz") {
		t.Error("mutation of map-A's editor set leaked into map-B")
	}
}

// TestBuiltinRoles_Order asserts that BuiltinRoles returns the
// privilege-ascending order documented in the package — operators rely
// on this ordering in admin UIs ("show roles, weakest first").
func TestBuiltinRoles_Order(t *testing.T) {
	got := BuiltinRoles()
	want := []Role{
		RoleSubscriber,
		RoleContributor,
		RoleAuthor,
		RoleEditor,
		RoleAdmin,
		RoleSuperAdmin,
	}
	if len(got) != len(want) {
		t.Fatalf("BuiltinRoles len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("BuiltinRoles[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestCapabilitySet_AllStable is a sanity check that All() returns the
// right elements regardless of map iteration order.
func TestCapabilitySet_AllStable(t *testing.T) {
	set := NewCapabilitySet(CapRead, CapEditPosts, CapPublishPosts)
	got := set.All()
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	want := []Capability{CapEditPosts, CapPublishPosts, CapRead}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if len(got) != len(want) {
		t.Fatalf("All len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("All[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
