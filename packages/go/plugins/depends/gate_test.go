package depends

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/manifest"
)

// buildManifest is a tiny convenience for tests: produce a
// *manifest.Manifest whose Depends field is the supplied list.
// Tests don't need to round-trip through Validate — Gate consumes the
// typed Manifest, not the raw bytes.
func buildManifest(name string, deps ...manifest.Dependency) *manifest.Manifest {
	return &manifest.Manifest{
		APIVersion: manifest.APIVersion,
		Name:       name,
		Version:    "1.0.0",
		Entry:      "plugin.wasm",
		Depends:    deps,
	}
}

func TestGate_NoDeps(t *testing.T) {
	t.Parallel()
	g := NewGate(fakeRegistry())
	if err := g.AllowActivate(buildManifest("gn-x")); err != nil {
		t.Fatalf("no-deps activation should pass: %v", err)
	}
	if err := g.AllowActivate(nil); err != nil {
		t.Fatalf("nil manifest should pass: %v", err)
	}
}

func TestGate_AllPresentActiveCompatible(t *testing.T) {
	t.Parallel()
	reg := fakeRegistry(
		PluginRecord{Slug: "gn-core", Version: "1.2.0", Active: true},
		PluginRecord{Slug: "gn-i18n", Version: "2.0.5", Active: true},
	)
	g := NewGate(reg)
	m := buildManifest("gn-x",
		manifest.Dependency{Name: "gn-core", Version: "^1.0.0"},
		manifest.Dependency{Name: "gn-i18n", Version: ">=2.0.0 <3.0.0"},
	)
	if err := g.AllowActivate(m); err != nil {
		t.Fatalf("clean activation should pass: %v", err)
	}
}

func TestGate_Missing(t *testing.T) {
	t.Parallel()
	g := NewGate(fakeRegistry())
	m := buildManifest("gn-x",
		manifest.Dependency{Name: "gn-core", Version: "^1.0.0"},
	)
	err := g.AllowActivate(m)
	if err == nil {
		t.Fatal("want ErrMissingDependency, got nil")
	}
	if !errors.Is(err, ErrMissingDependency) {
		t.Fatalf("want ErrMissingDependency, got %v", err)
	}
	var de *DependencyError
	if !errors.As(err, &de) {
		t.Fatalf("want *DependencyError, got %T", err)
	}
	if de.Plugin != "gn-x" || len(de.Slugs) != 1 || de.Slugs[0] != "gn-core" {
		t.Errorf("error detail: %+v", de)
	}
}

func TestGate_Inactive(t *testing.T) {
	t.Parallel()
	reg := fakeRegistry(PluginRecord{Slug: "gn-core", Version: "1.0.0", Active: false})
	g := NewGate(reg)
	err := g.AllowActivate(buildManifest("gn-x",
		manifest.Dependency{Name: "gn-core", Version: "^1.0.0"},
	))
	if !errors.Is(err, ErrInactiveDependency) {
		t.Fatalf("want ErrInactiveDependency, got %v", err)
	}
}

func TestGate_VersionMismatch(t *testing.T) {
	t.Parallel()
	reg := fakeRegistry(PluginRecord{Slug: "gn-core", Version: "2.0.0", Active: true})
	g := NewGate(reg)
	err := g.AllowActivate(buildManifest("gn-x",
		manifest.Dependency{Name: "gn-core", Version: "^1.0.0"},
	))
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("want ErrVersionMismatch, got %v", err)
	}
	var de *DependencyError
	if !errors.As(err, &de) {
		t.Fatalf("want *DependencyError, got %T", err)
	}
	if len(de.Mismatches) != 1 {
		t.Fatalf("Mismatches: got %v", de.Mismatches)
	}
	m := de.Mismatches[0]
	if m.Name != "gn-core" || m.Got != "2.0.0" || m.Want != "^1.0.0" {
		t.Errorf("mismatch detail: %+v", m)
	}
}

// TestGate_PriorityOrdering pins the documented contract: a manifest
// that hits Missing + Inactive + Version simultaneously surfaces
// Missing first, because once a plugin is installed the operator may
// already see different errors.
func TestGate_PriorityOrdering(t *testing.T) {
	t.Parallel()
	reg := fakeRegistry(
		PluginRecord{Slug: "gn-inactive", Version: "1.0.0", Active: false},
		PluginRecord{Slug: "gn-old", Version: "0.9.0", Active: true},
	)
	g := NewGate(reg)
	err := g.AllowActivate(buildManifest("gn-x",
		manifest.Dependency{Name: "gn-ghost", Version: "^1.0.0"},
		manifest.Dependency{Name: "gn-inactive", Version: "^1.0.0"},
		manifest.Dependency{Name: "gn-old", Version: "^1.0.0"},
	))
	if !errors.Is(err, ErrMissingDependency) {
		t.Fatalf("priority: want ErrMissingDependency first, got %v", err)
	}
}

// TestGate_Diamond confirms a manifest depending on two plugins that
// themselves share a transitive dependency (A → B,C; B → D; C → D)
// activates cleanly when D, B, C are all already Active.
func TestGate_Diamond(t *testing.T) {
	t.Parallel()
	reg := fakeRegistry(
		PluginRecord{Slug: "d", Version: "1.0.0", Active: true},
		PluginRecord{Slug: "b", Version: "1.0.0", Active: true, Depends: []Dependency{{Name: "d", VersionRange: "^1.0.0"}}},
		PluginRecord{Slug: "c", Version: "1.0.0", Active: true, Depends: []Dependency{{Name: "d", VersionRange: "^1.0.0"}}},
	)
	g := NewGate(reg)
	err := g.AllowActivate(buildManifest("a",
		manifest.Dependency{Name: "b", Version: "^1.0.0"},
		manifest.Dependency{Name: "c", Version: "^1.0.0"},
	))
	if err != nil {
		t.Fatalf("diamond should activate cleanly: %v", err)
	}
}

// TestGate_Concurrent runs 100 goroutines through AllowActivate at
// once and asserts no race (race detector) and a clean result every
// time. The lock inside the Gate is what's being exercised.
func TestGate_Concurrent(t *testing.T) {
	t.Parallel()
	reg := fakeRegistry(PluginRecord{Slug: "gn-core", Version: "1.2.0", Active: true})
	g := NewGate(reg)
	m := buildManifest("gn-x",
		manifest.Dependency{Name: "gn-core", Version: "^1.0.0"},
	)

	const goroutines = 100
	var (
		wg        sync.WaitGroup
		failures  atomic.Int64
		startGate sync.WaitGroup
	)
	startGate.Add(1)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			startGate.Wait()
			if err := g.AllowActivate(m); err != nil {
				failures.Add(1)
			}
		}()
	}
	startGate.Done()
	wg.Wait()

	if got := failures.Load(); got != 0 {
		t.Fatalf("concurrent activations had %d failures, want 0", got)
	}
}

// TestGate_NilRegistryPanics is a wiring-time guarantee: forgetting to
// pass a registry must blow up at boot, not at the first activation.
func TestGate_NilRegistryPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewGate(nil) should panic")
		}
	}()
	_ = NewGate(nil)
}

// TestFromManifest sanity-checks the typed conversion used by Activate.
func TestFromManifest(t *testing.T) {
	t.Parallel()
	in := []manifest.Dependency{
		{Name: "a", Version: "^1.0.0"},
		{Name: "b", Version: "~2.0.0"},
	}
	out := FromManifest(in)
	if len(out) != 2 {
		t.Fatalf("len: got %d", len(out))
	}
	if out[0] != (Dependency{Name: "a", VersionRange: "^1.0.0"}) {
		t.Errorf("[0]: got %+v", out[0])
	}
	if out[1] != (Dependency{Name: "b", VersionRange: "~2.0.0"}) {
		t.Errorf("[1]: got %+v", out[1])
	}
	if FromManifest(nil) != nil {
		t.Error("nil input should yield nil output")
	}
}
