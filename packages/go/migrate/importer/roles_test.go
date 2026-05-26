package importer

import (
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

func TestRoleMapper_Builtins(t *testing.T) {
	m := NewRoleMapper(nil)
	cases := []struct {
		in   string
		want policy.Role
	}{
		{"super_admin", policy.RoleSuperAdmin},
		{"administrator", policy.RoleAdmin},
		{"editor", policy.RoleEditor},
		{"author", policy.RoleAuthor},
		{"contributor", policy.RoleContributor},
		{"subscriber", policy.RoleSubscriber},
		// Case insensitivity.
		{"Administrator", policy.RoleAdmin},
		{"  EDITOR ", policy.RoleEditor},
	}
	for _, tc := range cases {
		got, exact := m.Map(tc.in)
		if got != tc.want {
			t.Errorf("Map(%q) = %v want %v", tc.in, got, tc.want)
		}
		if !exact {
			t.Errorf("Map(%q) should be exact match", tc.in)
		}
	}
}

func TestRoleMapper_CustomFallback(t *testing.T) {
	var warnings []string
	warn := func(format string, args ...any) {
		warnings = append(warnings, format)
	}
	m := NewRoleMapper(warn)

	// shop_manager (WooCommerce) → editor by "manager" heuristic.
	got, exact := m.Map("shop_manager")
	if got != policy.RoleEditor {
		t.Errorf("shop_manager: got %v want editor", got)
	}
	if exact {
		t.Error("shop_manager: exact should be false (heuristic)")
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings: got %d want 1", len(warnings))
	}
	if !strings.Contains(warnings[0], "heuristic") {
		t.Errorf("warning %q should mention heuristic", warnings[0])
	}

	// site_administrator → admin (not editor — admin is more specific).
	got, _ = m.Map("site_administrator")
	if got != policy.RoleAdmin {
		t.Errorf("site_administrator: got %v want admin", got)
	}

	// super_site_admin → super_admin.
	got, _ = m.Map("super_site_admin")
	if got != policy.RoleSuperAdmin {
		t.Errorf("super_site_admin: got %v want super_admin", got)
	}

	// Truly unknown → subscriber, with a warning.
	warnings = warnings[:0]
	got, exact = m.Map("flarble")
	if got != policy.RoleSubscriber {
		t.Errorf("flarble: got %v want subscriber", got)
	}
	if exact {
		t.Error("flarble: exact should be false")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "subscriber") {
		t.Errorf("flarble: warnings = %v want one mentioning subscriber", warnings)
	}
}

func TestRoleMapper_Override(t *testing.T) {
	m := NewRoleMapper(nil)
	if err := m.RegisterOverride("shop_manager", policy.RoleAdmin); err != nil {
		t.Fatalf("RegisterOverride: %v", err)
	}
	got, exact := m.Map("shop_manager")
	if got != policy.RoleAdmin {
		t.Errorf("Map(shop_manager) after override: got %v want admin", got)
	}
	if !exact {
		t.Error("override should count as exact")
	}

	// Override of a built-in.
	if err := m.RegisterOverride("subscriber", policy.RoleContributor); err != nil {
		t.Fatalf("RegisterOverride builtin: %v", err)
	}
	got, _ = m.Map("subscriber")
	if got != policy.RoleContributor {
		t.Errorf("Map(subscriber) after override: got %v want contributor", got)
	}

	// Unknown GoNext role rejected.
	if err := m.RegisterOverride("foo", policy.Role("bogus")); err == nil {
		t.Error("RegisterOverride with bogus role should error")
	}

	// Empty WP slug rejected.
	if err := m.RegisterOverride("", policy.RoleAdmin); err == nil {
		t.Error("RegisterOverride with empty slug should error")
	}
}

func TestRoleMapper_ParseOverrideFlag(t *testing.T) {
	m := NewRoleMapper(nil)
	if err := m.ParseOverrideFlag("shop_manager=admin"); err != nil {
		t.Fatalf("ParseOverrideFlag: %v", err)
	}
	got, _ := m.Map("shop_manager")
	if got != policy.RoleAdmin {
		t.Errorf("after flag: got %v want admin", got)
	}

	// Whitespace tolerated.
	if err := m.ParseOverrideFlag("  vendor  =  editor "); err != nil {
		t.Fatalf("ParseOverrideFlag with spaces: %v", err)
	}
	got, _ = m.Map("vendor")
	if got != policy.RoleEditor {
		t.Errorf("vendor: got %v want editor", got)
	}

	// Malformed forms.
	bad := []string{"", "no_equals", "=missing_lhs", "missing_rhs=", "old=not_a_real_role"}
	for _, b := range bad {
		if err := m.ParseOverrideFlag(b); err == nil {
			t.Errorf("ParseOverrideFlag(%q) should error", b)
		}
	}
}

func TestRoleMapper_NilWarnSilent(t *testing.T) {
	// Ensure a nil warn callback doesn't panic on heuristic fallback.
	m := NewRoleMapper(nil)
	got, _ := m.Map("totally_unknown")
	if got != policy.RoleSubscriber {
		t.Errorf("unknown with nil warn: got %v want subscriber", got)
	}
}
