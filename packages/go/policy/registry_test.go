package policy

import (
	"strings"
	"sync"
	"testing"
)

// TestRegistry_BuiltinSeed asserts every built-in capability constant is
// registered with a non-empty description on package init. Operators
// rely on the description in admin UIs; an empty one is a doc bug.
func TestRegistry_BuiltinSeed(t *testing.T) {
	cases := []Capability{
		CapRead, CapEditPosts, CapEditOthersPosts, CapPublishPosts,
		CapManageOptions, CapManageInstall, CapInstallPlugins,
		CapManagePlugins, CapManageThemes, CapModerateComments,
		CapUploadFiles, CapListUsers,
	}
	for _, c := range cases {
		desc, ok := LookupCapability(c)
		if !ok {
			t.Errorf("built-in capability %q not registered", c)
			continue
		}
		if strings.TrimSpace(desc) == "" {
			t.Errorf("built-in capability %q has empty description", c)
		}
	}
}

// TestRegisterCapability_Roundtrip is the happy-path: registering a
// plugin capability makes it visible to LookupCapability and to
// RegisteredCapabilities. The test resets the global registry so it
// doesn't leak state into other tests.
func TestRegisterCapability_Roundtrip(t *testing.T) {
	t.Cleanup(resetRegistryForTest)
	resetRegistryForTest()

	RegisterCapability("manage_forms", "Manage form submissions.")
	desc, ok := LookupCapability("manage_forms")
	if !ok {
		t.Fatal("manage_forms not found after Register")
	}
	if desc != "Manage form submissions." {
		t.Errorf("description = %q, want %q", desc, "Manage form submissions.")
	}

	all := RegisteredCapabilities()
	found := false
	for _, c := range all {
		if c == "manage_forms" {
			found = true
			break
		}
	}
	if !found {
		t.Error("manage_forms missing from RegisteredCapabilities")
	}
}

// TestRegisterCapability_DoubleRegisterPanics asserts the "do not
// double-register" rule: a duplicate name (whether from a buggy plugin
// or a typo in core) is a panic, not a silent overwrite.
func TestRegisterCapability_DoubleRegisterPanics(t *testing.T) {
	t.Cleanup(resetRegistryForTest)
	resetRegistryForTest()
	RegisterCapability("custom_thing", "Initial description")

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on double-register, got none")
		} else if msg, _ := r.(string); !strings.Contains(msg, "already registered") {
			t.Errorf("panic message = %q, want substring %q", msg, "already registered")
		}
	}()
	RegisterCapability("custom_thing", "Second registration")
}

// TestRegisterCapability_EmptyNamePanics rejects empty names — the
// empty string is the zero value, never a valid slug.
func TestRegisterCapability_EmptyNamePanics(t *testing.T) {
	t.Cleanup(resetRegistryForTest)
	resetRegistryForTest()

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on empty name, got none")
		}
	}()
	RegisterCapability("", "no name")
}

// TestRegisteredCapabilities_Sorted asserts the returned slice is sorted
// lexicographically — admin UIs render in this order, so an unsorted
// slice would surface as flapping UI.
func TestRegisteredCapabilities_Sorted(t *testing.T) {
	got := RegisteredCapabilities()
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Errorf("RegisteredCapabilities not sorted at index %d: %q > %q",
				i, got[i-1], got[i])
		}
	}
}

// TestRegister_Concurrent ensures the registry is safe under concurrent
// registration — plugin init code may run in parallel, and a corrupted
// map is one of those nightmare bugs that only shows up under race.
func TestRegister_Concurrent(t *testing.T) {
	t.Cleanup(resetRegistryForTest)
	resetRegistryForTest()

	const n = 64
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			RegisterCapability(
				"cap_"+itoa(i),
				"Capability number "+itoa(i),
			)
		}()
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if _, ok := LookupCapability(Capability("cap_" + itoa(i))); !ok {
			t.Errorf("cap_%d missing after concurrent register", i)
		}
	}
}

// itoa is a tiny helper to avoid pulling strconv into the test purely
// for label construction.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
