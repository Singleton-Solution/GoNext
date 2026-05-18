package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/depends"
)

// v1Manifest builds a gonext.io/v1 manifest blob with optional
// depends entries. This is the manifest shape the validator
// (plugins/manifest) accepts and the dependency gate parses on
// Activate.
func v1Manifest(t *testing.T, name string, deps ...map[string]string) []byte {
	t.Helper()
	m := map[string]any{
		"apiVersion": "gonext.io/v1",
		"name":       name,
		"version":    "1.0.0",
		"entry":      "plugin.wasm",
	}
	if len(deps) > 0 {
		entries := make([]map[string]string, 0, len(deps))
		entries = append(entries, deps...)
		m["depends"] = entries
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal v1Manifest: %v", err)
	}
	return raw
}

// installRow inserts a Plugin row directly into storage so the test
// doesn't have to thread a full bundle through Install. The state and
// manifest body are set explicitly; everything else gets sensible
// defaults.
func installRow(t *testing.T, storage *MemoryStorage, slug, version string, state State, manifestBlob []byte) {
	t.Helper()
	if err := storage.Insert(context.Background(), Plugin{
		Slug:       slug,
		Version:    version,
		ABIVersion: 1,
		Manifest:   manifestBlob,
		State:      state,
	}); err != nil {
		t.Fatalf("Insert %s: %v", slug, err)
	}
}

// registryFromStorage returns a depends.Registry backed by the given
// MemoryStorage. It walks the row's Manifest bytes to extract that
// plugin's own depends[] so the resolver can follow transitive edges
// (though AllowActivate only checks direct deps today; the conversion
// keeps the test honest).
func registryFromStorage(s *MemoryStorage) depends.Registry {
	return func(name string) (*depends.PluginRecord, bool) {
		p, err := s.Get(context.Background(), name)
		if err != nil {
			return nil, false
		}
		rec := &depends.PluginRecord{
			Slug:    p.Slug,
			Version: p.Version,
			Active:  p.State == StateActive,
		}
		// Optional: parse the row's manifest for transitive deps.
		// Not needed by AllowActivate itself but useful for the topo
		// sort in larger flows. We swallow parse errors — a legacy
		// manifest has no depends[] anyway.
		var probe struct {
			Depends []struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"depends"`
		}
		if err := json.Unmarshal(p.Manifest, &probe); err == nil {
			for _, d := range probe.Depends {
				rec.Depends = append(rec.Depends, depends.Dependency{
					Name:         d.Name,
					VersionRange: d.Version,
				})
			}
		}
		return rec, true
	}
}

// TestManager_Activate_DepGate_Pass exercises the happy path: a v1
// manifest with depends[] activates cleanly when every entry is
// already Active at a compatible version.
func TestManager_Activate_DepGate_Pass(t *testing.T) {
	rt := &recordingRuntime{}
	mgr, storage, _ := newManagerForTest(t, WithRuntime(rt))
	gate := depends.NewGate(registryFromStorage(storage))
	mgr.dependGate = gate // direct assignment; the option-based wiring
	// is exercised separately. We do this so the helper can install
	// the dependency row first without depending on Manager.Install
	// understanding v1 manifests (#44 will unify that).

	installRow(t, storage, "gn-core", "1.2.0", StateActive, v1Manifest(t, "gn-core"))
	installRow(t, storage, "gn-x", "1.0.0", StateInstalled, v1Manifest(t, "gn-x",
		map[string]string{"name": "gn-core", "version": "^1.0.0"}))

	if err := mgr.Activate(context.Background(), "gn-x"); err != nil {
		t.Fatalf("Activate gn-x: %v", err)
	}
	p, _ := storage.Get(context.Background(), "gn-x")
	if p.State != StateActive {
		t.Errorf("state: got %q want %q", p.State, StateActive)
	}
	if len(rt.loaded) != 1 || rt.loaded[0] != "gn-x" {
		t.Errorf("runtime.Load expected once for gn-x; got %v", rt.loaded)
	}
}

// TestManager_Activate_DepGate_MissingBlocks ensures a missing
// dependency surfaces ErrMissingDependency without touching the row
// or calling Runtime.Load.
func TestManager_Activate_DepGate_MissingBlocks(t *testing.T) {
	rt := &recordingRuntime{}
	mgr, storage, _ := newManagerForTest(t, WithRuntime(rt))
	mgr.dependGate = depends.NewGate(registryFromStorage(storage))

	installRow(t, storage, "gn-x", "1.0.0", StateInstalled, v1Manifest(t, "gn-x",
		map[string]string{"name": "gn-core", "version": "^1.0.0"}))

	err := mgr.Activate(context.Background(), "gn-x")
	if err == nil {
		t.Fatal("Activate: want error")
	}
	if !errors.Is(err, depends.ErrMissingDependency) {
		t.Fatalf("want ErrMissingDependency, got %v", err)
	}
	p, _ := storage.Get(context.Background(), "gn-x")
	if p.State != StateInstalled {
		t.Errorf("state: dep failure must not transition; got %q", p.State)
	}
	if len(rt.loaded) != 0 {
		t.Errorf("runtime.Load should NOT fire on dep failure; got %v", rt.loaded)
	}
}

// TestManager_Activate_DepGate_InactiveBlocks: dependency installed
// but in StateInactive trips ErrInactiveDependency.
func TestManager_Activate_DepGate_InactiveBlocks(t *testing.T) {
	mgr, storage, _ := newManagerForTest(t)
	mgr.dependGate = depends.NewGate(registryFromStorage(storage))

	installRow(t, storage, "gn-core", "1.2.0", StateInactive, v1Manifest(t, "gn-core"))
	installRow(t, storage, "gn-x", "1.0.0", StateInstalled, v1Manifest(t, "gn-x",
		map[string]string{"name": "gn-core", "version": "^1.0.0"}))

	err := mgr.Activate(context.Background(), "gn-x")
	if !errors.Is(err, depends.ErrInactiveDependency) {
		t.Fatalf("want ErrInactiveDependency, got %v", err)
	}
}

// TestManager_Activate_DepGate_VersionMismatch: dependency present
// and Active but at a version outside the requested range.
func TestManager_Activate_DepGate_VersionMismatch(t *testing.T) {
	mgr, storage, _ := newManagerForTest(t)
	mgr.dependGate = depends.NewGate(registryFromStorage(storage))

	installRow(t, storage, "gn-core", "2.0.0", StateActive, v1Manifest(t, "gn-core"))
	installRow(t, storage, "gn-x", "1.0.0", StateInstalled, v1Manifest(t, "gn-x",
		map[string]string{"name": "gn-core", "version": "^1.0.0"}))

	err := mgr.Activate(context.Background(), "gn-x")
	if !errors.Is(err, depends.ErrVersionMismatch) {
		t.Fatalf("want ErrVersionMismatch, got %v", err)
	}
	var de *depends.DependencyError
	if !errors.As(err, &de) {
		t.Fatalf("want *DependencyError, got %T", err)
	}
	if len(de.Mismatches) != 1 || de.Mismatches[0].Got != "2.0.0" {
		t.Errorf("mismatch detail: %+v", de.Mismatches)
	}
}

// TestManager_Activate_DepGate_LegacyBypass confirms a legacy
// manifest (no apiVersion, no depends) skips the gate cleanly.
// This is the back-compat contract.
func TestManager_Activate_DepGate_LegacyBypass(t *testing.T) {
	mgr, _, _ := newManagerForTest(t)
	// Wire a gate that would normally fail (empty registry) — we want
	// to prove the gate is bypassed entirely for legacy manifests.
	mgr.dependGate = depends.NewGate(func(string) (*depends.PluginRecord, bool) {
		return nil, false
	})

	// Use the install path so the legacy manifest goes through the
	// standard structural checks.
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("legacy Activate should pass gate: %v", err)
	}
}

// TestManager_Activate_DepGate_WithDependencyGateOption pins that
// WithDependencyGate wires the option through NewManager (rather than
// relying on direct field assignment as the other tests do for
// convenience).
func TestManager_Activate_DepGate_WithDependencyGateOption(t *testing.T) {
	calls := atomic.Int64{}
	gate := depends.NewGate(func(name string) (*depends.PluginRecord, bool) {
		calls.Add(1)
		return nil, false
	})

	mgr, storage, _ := newManagerForTest(t, WithDependencyGate(gate))
	installRow(t, storage, "gn-x", "1.0.0", StateInstalled, v1Manifest(t, "gn-x",
		map[string]string{"name": "gn-core", "version": "^1.0.0"}))

	err := mgr.Activate(context.Background(), "gn-x")
	if !errors.Is(err, depends.ErrMissingDependency) {
		t.Fatalf("want ErrMissingDependency, got %v", err)
	}
	if calls.Load() == 0 {
		t.Error("gate registry was never consulted; option may not be wired")
	}
}

// TestManager_Activate_DepGate_Concurrent fires 100 goroutines at a
// gated Activate to assert the gate's internal lock is honoured and
// the manager's CAS still picks a single winner.
func TestManager_Activate_DepGate_Concurrent(t *testing.T) {
	rt := &recordingRuntime{}
	mgr, storage, _ := newManagerForTest(t, WithRuntime(rt))
	mgr.dependGate = depends.NewGate(registryFromStorage(storage))

	installRow(t, storage, "gn-core", "1.2.0", StateActive, v1Manifest(t, "gn-core"))
	installRow(t, storage, "gn-x", "1.0.0", StateInstalled, v1Manifest(t, "gn-x",
		map[string]string{"name": "gn-core", "version": "^1.0.0"}))

	const goroutines = 100
	var (
		wg              sync.WaitGroup
		wins, transErrs atomic.Int64
		startGate       sync.WaitGroup
	)
	startGate.Add(1)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			startGate.Wait()
			err := mgr.Activate(context.Background(), "gn-x")
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrInvalidTransition):
				transErrs.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	startGate.Done()
	wg.Wait()

	if wins.Load() != 1 {
		t.Fatalf("winners: got %d want 1", wins.Load())
	}
	if int(wins.Load()+transErrs.Load()) != goroutines {
		t.Fatalf("totals: wins=%d trans=%d", wins.Load(), transErrs.Load())
	}
}
