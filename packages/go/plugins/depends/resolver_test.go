package depends

import (
	"testing"
)

// fakeRegistry returns a Registry function backed by a static map.
// Tests that need a different registry view per call can rebuild this
// trivially.
func fakeRegistry(records ...PluginRecord) Registry {
	m := make(map[string]PluginRecord, len(records))
	for _, r := range records {
		m[r.Slug] = r
	}
	return func(name string) (*PluginRecord, bool) {
		r, ok := m[name]
		if !ok {
			return nil, false
		}
		// Return a copy so callers can't mutate the registry by
		// mutating the returned record. Mirrors lifecycle.Storage.Get.
		out := r
		return &out, true
	}
}

func TestResolver_EmptyDeps(t *testing.T) {
	t.Parallel()
	r := &Resolver{Registry: fakeRegistry()}
	report, err := r.Check(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.OK() {
		t.Errorf("empty deps should yield clean report; got %+v", report)
	}
}

func TestResolver_AllSatisfied(t *testing.T) {
	t.Parallel()
	reg := fakeRegistry(
		PluginRecord{Slug: "gn-core", Version: "1.2.0", Active: true},
		PluginRecord{Slug: "gn-i18n", Version: "2.0.5", Active: true},
	)
	r := &Resolver{Registry: reg}
	report, err := r.Check([]Dependency{
		{Name: "gn-core", VersionRange: "^1.0.0"},
		{Name: "gn-i18n", VersionRange: ">=2.0.0 <3.0.0"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.OK() {
		t.Errorf("want OK, got %+v", report)
	}
}

func TestResolver_Missing(t *testing.T) {
	t.Parallel()
	reg := fakeRegistry(PluginRecord{Slug: "gn-core", Version: "1.0.0", Active: true})
	r := &Resolver{Registry: reg}
	report, err := r.Check([]Dependency{
		{Name: "gn-core", VersionRange: "^1.0.0"},
		{Name: "gn-ghost", VersionRange: "^1.0.0"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Missing) != 1 || report.Missing[0] != "gn-ghost" {
		t.Errorf("Missing: got %v", report.Missing)
	}
}

func TestResolver_Inactive(t *testing.T) {
	t.Parallel()
	reg := fakeRegistry(PluginRecord{Slug: "gn-core", Version: "1.0.0", Active: false})
	r := &Resolver{Registry: reg}
	report, err := r.Check([]Dependency{
		{Name: "gn-core", VersionRange: "^1.0.0"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Inactive) != 1 || report.Inactive[0] != "gn-core" {
		t.Errorf("Inactive: got %v", report.Inactive)
	}
}

func TestResolver_Incompatible(t *testing.T) {
	t.Parallel()
	reg := fakeRegistry(PluginRecord{Slug: "gn-core", Version: "2.0.0", Active: true})
	r := &Resolver{Registry: reg}
	report, err := r.Check([]Dependency{
		{Name: "gn-core", VersionRange: "^1.0.0"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Incompatible) != 1 {
		t.Fatalf("Incompatible: got %v", report.Incompatible)
	}
	m := report.Incompatible[0]
	if m.Name != "gn-core" || m.Got != "2.0.0" || m.Want != "^1.0.0" {
		t.Errorf("mismatch detail: %+v", m)
	}
}

// TestResolver_Mixed exercises a manifest that simultaneously hits all
// three buckets — proving the resolver categorises every entry rather
// than short-circuiting on the first failure.
func TestResolver_Mixed(t *testing.T) {
	t.Parallel()
	reg := fakeRegistry(
		PluginRecord{Slug: "gn-active", Version: "1.0.0", Active: true},
		PluginRecord{Slug: "gn-inactive", Version: "1.0.0", Active: false},
		PluginRecord{Slug: "gn-old", Version: "0.9.0", Active: true},
	)
	r := &Resolver{Registry: reg}
	report, err := r.Check([]Dependency{
		{Name: "gn-active", VersionRange: "^1.0.0"},
		{Name: "gn-inactive", VersionRange: "^1.0.0"},
		{Name: "gn-old", VersionRange: "^1.0.0"},
		{Name: "gn-ghost", VersionRange: "^1.0.0"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Missing) != 1 || report.Missing[0] != "gn-ghost" {
		t.Errorf("Missing: %v", report.Missing)
	}
	if len(report.Inactive) != 1 || report.Inactive[0] != "gn-inactive" {
		t.Errorf("Inactive: %v", report.Inactive)
	}
	if len(report.Incompatible) != 1 || report.Incompatible[0].Name != "gn-old" {
		t.Errorf("Incompatible: %v", report.Incompatible)
	}
}

func TestResolver_RegistryRequired(t *testing.T) {
	t.Parallel()
	r := &Resolver{}
	_, err := r.Check([]Dependency{{Name: "x", VersionRange: "*"}})
	if err == nil {
		t.Fatal("want error for nil Registry")
	}
}

func TestResolver_EmptyDepFields(t *testing.T) {
	t.Parallel()
	r := &Resolver{Registry: fakeRegistry()}
	if _, err := r.Check([]Dependency{{Name: ""}}); err == nil {
		t.Error("empty name: want error")
	}
	if _, err := r.Check([]Dependency{{Name: "ok"}}); err == nil {
		t.Error("empty range: want error")
	}
}
