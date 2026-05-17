package policy

import (
	"strings"
	"testing"
)

// TestBasicPolicy_Can is the table-driven check of the Can() decision
// rule. Each row sets the principal's roles, the requested capability,
// and the expected Allowed value. The "reason" field is checked as a
// substring so the test stays robust against wording tweaks.
func TestBasicPolicy_Can(t *testing.T) {
	pol := NewBasicPolicy(DefaultRoleCapabilities())

	cases := []struct {
		name        string
		roles       []Role
		capability  Capability
		wantAllowed bool
		wantReason  string // substring
	}{
		{
			name:        "subscriber can read",
			roles:       []Role{RoleSubscriber},
			capability:  CapRead,
			wantAllowed: true,
			wantReason:  "subscriber",
		},
		{
			name:        "subscriber cannot edit posts",
			roles:       []Role{RoleSubscriber},
			capability:  CapEditPosts,
			wantAllowed: false,
			wantReason:  "no role on principal grants",
		},
		{
			name:        "contributor can edit posts",
			roles:       []Role{RoleContributor},
			capability:  CapEditPosts,
			wantAllowed: true,
		},
		{
			name:        "contributor cannot publish",
			roles:       []Role{RoleContributor},
			capability:  CapPublishPosts,
			wantAllowed: false,
		},
		{
			name:        "author can publish",
			roles:       []Role{RoleAuthor},
			capability:  CapPublishPosts,
			wantAllowed: true,
		},
		{
			name:        "author cannot edit others' posts",
			roles:       []Role{RoleAuthor},
			capability:  CapEditOthersPosts,
			wantAllowed: false,
		},
		{
			name:        "editor can edit others' posts",
			roles:       []Role{RoleEditor},
			capability:  CapEditOthersPosts,
			wantAllowed: true,
		},
		{
			name:        "editor cannot manage_options",
			roles:       []Role{RoleEditor},
			capability:  CapManageOptions,
			wantAllowed: false,
		},
		{
			name:        "admin can install plugins",
			roles:       []Role{RoleAdmin},
			capability:  CapInstallPlugins,
			wantAllowed: true,
		},
		{
			name:        "admin cannot manage_install",
			roles:       []Role{RoleAdmin},
			capability:  CapManageInstall,
			wantAllowed: false,
		},
		{
			name:        "super_admin can manage_install",
			roles:       []Role{RoleSuperAdmin},
			capability:  CapManageInstall,
			wantAllowed: true,
		},
		{
			name:        "super_admin retains everything admin has",
			roles:       []Role{RoleSuperAdmin},
			capability:  CapInstallPlugins,
			wantAllowed: true,
		},
		{
			name:        "multi-role: any role grants is sufficient",
			roles:       []Role{RoleSubscriber, RoleAdmin},
			capability:  CapInstallPlugins,
			wantAllowed: true,
		},
		{
			name:        "no roles: denied",
			roles:       nil,
			capability:  CapRead,
			wantAllowed: false,
			wantReason:  "no roles",
		},
		{
			name:        "unknown role on principal is ignored, not panicking",
			roles:       []Role{"made_up_role"},
			capability:  CapRead,
			wantAllowed: false,
		},
		{
			name:        "unknown capability is denied",
			roles:       []Role{RoleSuperAdmin},
			capability:  Capability("nonexistent_cap"),
			wantAllowed: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Principal{UserID: "user:1", Roles: tc.roles}
			d := pol.Can(p, tc.capability, nil)
			if d.Allowed != tc.wantAllowed {
				t.Errorf("Can(%v, %q) allowed = %v, want %v (reason=%q)",
					tc.roles, tc.capability, d.Allowed, tc.wantAllowed, d.Reason)
			}
			if tc.wantReason != "" && !strings.Contains(d.Reason, tc.wantReason) {
				t.Errorf("reason = %q, want substring %q", d.Reason, tc.wantReason)
			}
		})
	}
}

// TestBasicPolicy_NoRolesAlwaysDenies verifies that a principal with the
// empty Roles slice is denied for EVERY built-in capability — a strong
// invariant ("an unauthenticated/unauthorized principal can do nothing")
// that catches accidental default-allow regressions.
func TestBasicPolicy_NoRolesAlwaysDenies(t *testing.T) {
	pol := NewBasicPolicy(DefaultRoleCapabilities())
	p := Principal{UserID: "user:nobody"}

	for _, c := range []Capability{
		CapRead, CapEditPosts, CapPublishPosts, CapEditOthersPosts,
		CapManageOptions, CapInstallPlugins, CapManageInstall,
		CapUploadFiles, CapModerateComments,
	} {
		if d := pol.Can(p, c, nil); d.Allowed {
			t.Errorf("principal with no roles unexpectedly allowed %q", c)
		}
	}
}

// TestBasicPolicy_NilMap protects callers who pass nil. The policy should
// degrade to deny-all rather than panic — a nil map is a "no roles
// configured anywhere" state, which is the safer default than crashing
// on the first request.
func TestBasicPolicy_NilMap(t *testing.T) {
	pol := NewBasicPolicy(nil)
	p := Principal{UserID: "user:1", Roles: []Role{RoleSuperAdmin}}
	d := pol.Can(p, CapRead, nil)
	if d.Allowed {
		t.Fatalf("nil-map policy must deny, got %+v", d)
	}
}

// TestBasicPolicy_CustomMap verifies BasicPolicy honors a non-default
// map — important for unit tests in downstream packages that want to
// stand up a Policy with a small, hand-rolled cap set.
func TestBasicPolicy_CustomMap(t *testing.T) {
	custom := map[Role]CapabilitySet{
		"tester": NewCapabilitySet("run_tests", "view_logs"),
	}
	pol := NewBasicPolicy(custom)
	p := Principal{UserID: "user:42", Roles: []Role{"tester"}}

	if d := pol.Can(p, "run_tests", nil); !d.Allowed {
		t.Errorf("tester should run_tests, got %+v", d)
	}
	if d := pol.Can(p, "manage_options", nil); d.Allowed {
		t.Errorf("tester should NOT have manage_options, got %+v", d)
	}
}

// TestBasicPolicy_Capabilities verifies the diagnostic Capabilities()
// method returns the union of caps held across the principal's roles.
// "Author + Editor" overlaps heavily — the union should still be a
// proper set (no duplicate entries when materialized).
func TestBasicPolicy_Capabilities(t *testing.T) {
	pol := NewBasicPolicy(DefaultRoleCapabilities())
	p := Principal{UserID: "u", Roles: []Role{RoleAuthor, RoleEditor}}
	got := pol.Capabilities(p)

	// Editor strictly contains author, so the union is exactly the
	// editor set.
	want := DefaultRoleCapabilities()[RoleEditor]
	if len(got) != len(want) {
		t.Errorf("Capabilities union len = %d, want %d", len(got), len(want))
	}
	for c := range want {
		if !got.Has(c) {
			t.Errorf("Capabilities union missing %q", c)
		}
	}
}

// TestPolicyInterface_Compile is a static guarantee that BasicPolicy
// implements Policy. A change to either side that breaks the interface
// is a build error here, not a runtime surprise.
func TestPolicyInterface_Compile(t *testing.T) {
	var _ Policy = NewBasicPolicy(nil)
}
