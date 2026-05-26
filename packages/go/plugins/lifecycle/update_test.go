package lifecycle

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// makeBundle builds a minimal .gnplugin ZIP with a manifest carrying
// the requested slug/version/abi. Wraps the existing buildBundle
// helper in manager_test.go so update-specific tests can build many
// bundles concisely.
func makeBundle(t *testing.T, slug, version string, abi int) io.Reader {
	t.Helper()
	manifest := `{"slug":"` + slug + `","version":"` + version + `","abi_version":` + itoaUT(abi) + `}`
	return buildBundle(t, manifest)
}

func itoaUT(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// TestUpdate_AppendsActiveAndRetainsOld verifies the happy path:
// install + activate v1.0.0, then Update to v1.1.0. The version log
// has a retained 1.0.0 row at the end.
func TestUpdate_AppendsActiveAndRetainsOld(t *testing.T) {
	vl := NewMemoryVersionLog()
	rt := &recordingRuntime{}
	m, store, _ := newManagerForTest(t,
		WithRuntime(rt),
		EnableVersionedUpdates(WithVersionLog(vl), WithRetention(2*time.Hour)),
	)

	ctx := context.Background()
	if _, err := m.Install(ctx, makeBundle(t, "test-plugin", "1.0.0", 1)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := m.Activate(ctx, "test-plugin"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	// Seed the version log with the initial active row (Install
	// doesn't touch the version log — Update is the first interaction).
	_, err := vl.AppendActive(ctx, VersionRow{
		Slug: "test-plugin", Version: "1.0.0", ABIVersion: 1,
		InstalledAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	v, err := m.Update(ctx, makeBundle(t, "test-plugin", "1.1.0", 1))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if v != "1.1.0" {
		t.Errorf("returned version: %q", v)
	}

	// plugins row should reflect 1.1.0.
	got, err := store.Get(ctx, "test-plugin")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "1.1.0" {
		t.Errorf("plugins.Version: got %q want 1.1.0", got.Version)
	}

	// 1.0.0 should now be retained.
	retained, _ := vl.ListRetained(ctx, "test-plugin")
	if len(retained) != 1 || retained[0].Version != "1.0.0" {
		t.Errorf("retained: %v", retained)
	}
}

// TestUpdate_RejectsSameVersion treats same-version as a no-op.
func TestUpdate_RejectsSameVersion(t *testing.T) {
	vl := NewMemoryVersionLog()
	rt := &recordingRuntime{}
	m, _, _ := newManagerForTest(t,
		WithRuntime(rt),
		EnableVersionedUpdates(WithVersionLog(vl)),
	)
	ctx := context.Background()
	if _, err := m.Install(ctx, makeBundle(t, "plug-x", "2.0.0", 1)); err != nil {
		t.Fatal(err)
	}
	if err := m.Activate(ctx, "plug-x"); err != nil {
		t.Fatal(err)
	}
	_, _ = vl.AppendActive(ctx, VersionRow{Slug: "plug-x", Version: "2.0.0", ABIVersion: 1, InstalledAt: time.Now()})

	v, err := m.Update(ctx, makeBundle(t, "plug-x", "2.0.0", 1))
	if err != nil {
		t.Fatalf("Update same version: %v", err)
	}
	if v != "2.0.0" {
		t.Errorf("version: %q", v)
	}
	// No new row in the log.
	rows := vl.List("plug-x")
	if len(rows) != 1 {
		t.Errorf("rows: %d (no-op should not append)", len(rows))
	}
}

// TestUpdate_LoadFailureLeavesOldVersionRunning verifies that a
// failed Runtime.Load on the new version doesn't disturb the active
// plugin row.
func TestUpdate_LoadFailureLeavesOldVersionRunning(t *testing.T) {
	vl := NewMemoryVersionLog()
	rt := &recordingRuntime{}
	m, store, _ := newManagerForTest(t,
		WithRuntime(rt),
		EnableVersionedUpdates(WithVersionLog(vl)),
	)
	ctx := context.Background()
	if _, err := m.Install(ctx, makeBundle(t, "plug-a", "1.0.0", 1)); err != nil {
		t.Fatal(err)
	}
	if err := m.Activate(ctx, "plug-a"); err != nil {
		t.Fatal(err)
	}
	_, _ = vl.AppendActive(ctx, VersionRow{Slug: "plug-a", Version: "1.0.0", ABIVersion: 1, InstalledAt: time.Now()})

	rt.loadErr = errors.New("wasm explode")
	_, err := m.Update(ctx, makeBundle(t, "plug-a", "1.1.0", 1))
	if err == nil {
		t.Fatal("expected error")
	}
	got, _ := store.Get(ctx, "plug-a")
	if got.Version != "1.0.0" {
		t.Errorf("plugin row mutated: %q", got.Version)
	}
}

// TestRollback_RestoresRetainedVersion installs 1.0.0, updates to 1.1.0,
// then rolls back. After rollback the plugins row reports 1.0.0.
func TestRollback_RestoresRetainedVersion(t *testing.T) {
	vl := NewMemoryVersionLog()
	m, store, _ := newManagerForTest(t,
		WithRuntime(&recordingRuntime{}),
		EnableVersionedUpdates(WithVersionLog(vl), WithRetention(2*time.Hour)),
	)
	ctx := context.Background()
	if _, err := m.Install(ctx, makeBundle(t, "plug-rb", "1.0.0", 1)); err != nil {
		t.Fatal(err)
	}
	if err := m.Activate(ctx, "plug-rb"); err != nil {
		t.Fatal(err)
	}
	_, _ = vl.AppendActive(ctx, VersionRow{Slug: "plug-rb", Version: "1.0.0", ABIVersion: 1, InstalledAt: time.Now()})

	if _, err := m.Update(ctx, makeBundle(t, "plug-rb", "1.1.0", 1)); err != nil {
		t.Fatalf("Update: %v", err)
	}

	v, err := m.Rollback(ctx, "plug-rb", "")
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if v != "1.0.0" {
		t.Errorf("rolled back to %q", v)
	}
	got, _ := store.Get(ctx, "plug-rb")
	if got.Version != "1.0.0" {
		t.Errorf("plugins.Version: %q", got.Version)
	}
}

// TestRollback_NoRetainedReturnsErr returns ErrNoRollback when no
// previous version is retained.
func TestRollback_NoRetainedReturnsErr(t *testing.T) {
	vl := NewMemoryVersionLog()
	m, _, _ := newManagerForTest(t,
		WithRuntime(&recordingRuntime{}),
		EnableVersionedUpdates(WithVersionLog(vl)),
	)
	ctx := context.Background()
	if _, err := m.Install(ctx, makeBundle(t, "fresh", "1.0.0", 1)); err != nil {
		t.Fatal(err)
	}
	if err := m.Activate(ctx, "fresh"); err != nil {
		t.Fatal(err)
	}
	_, err := m.Rollback(ctx, "fresh", "")
	if !errors.Is(err, ErrNoRollback) {
		t.Errorf("err: %v", err)
	}
}

// TestRetentionCleanup_PurgesExpired verifies the cron-style helper.
func TestRetentionCleanup_PurgesExpired(t *testing.T) {
	vl := NewMemoryVersionLog()
	now := fixedTime
	m, _, _ := newManagerForTest(t,
		WithRuntime(&recordingRuntime{}),
		WithNowFunc(func() time.Time { return now }),
		EnableVersionedUpdates(WithVersionLog(vl), WithRetention(time.Minute)),
	)
	ctx := context.Background()
	if _, err := m.Install(ctx, makeBundle(t, "plug-rt", "1.0.0", 1)); err != nil {
		t.Fatal(err)
	}
	if err := m.Activate(ctx, "plug-rt"); err != nil {
		t.Fatal(err)
	}
	_, _ = vl.AppendActive(ctx, VersionRow{Slug: "plug-rt", Version: "1.0.0", ABIVersion: 1, InstalledAt: now})
	if _, err := m.Update(ctx, makeBundle(t, "plug-rt", "1.1.0", 1)); err != nil {
		t.Fatal(err)
	}
	// Advance the clock past the retention window.
	now = now.Add(2 * time.Minute)
	purged, err := m.RunRetentionCleanup(ctx)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if purged != 1 {
		t.Errorf("purged: %d", purged)
	}
}

// TestDrainTracker_BeginEnd verifies the in-flight counter.
func TestDrainTracker_BeginEnd(t *testing.T) {
	d := newDrainTracker()
	end1 := d.Begin("s", "v")
	end2 := d.Begin("s", "v")
	if got := d.Snapshot("s", "v"); got != 2 {
		t.Errorf("snapshot: %d", got)
	}
	end1()
	if got := d.Snapshot("s", "v"); got != 1 {
		t.Errorf("snapshot after one End: %d", got)
	}
	end2()
	if got := d.Snapshot("s", "v"); got != 0 {
		t.Errorf("snapshot drained: %d", got)
	}
}

// TestUpdate_DrainObserved verifies Update waits for in-flight calls
// against the old version before marking it retained.
func TestUpdate_DrainObserved(t *testing.T) {
	vl := NewMemoryVersionLog()
	m, _, _ := newManagerForTest(t,
		WithRuntime(&recordingRuntime{}),
		EnableVersionedUpdates(
			WithVersionLog(vl),
			WithDrainTimeout(200*time.Millisecond),
		),
	)
	ctx := context.Background()
	if _, err := m.Install(ctx, makeBundle(t, "plug-dr", "1.0.0", 1)); err != nil {
		t.Fatal(err)
	}
	if err := m.Activate(ctx, "plug-dr"); err != nil {
		t.Fatal(err)
	}
	_, _ = vl.AppendActive(ctx, VersionRow{Slug: "plug-dr", Version: "1.0.0", ABIVersion: 1, InstalledAt: time.Now()})

	// Start an in-flight request that finishes shortly.
	dt := m.DrainTracker()
	end := dt.Begin("plug-dr", "1.0.0")
	go func() {
		time.Sleep(50 * time.Millisecond)
		end()
	}()

	start := time.Now()
	if _, err := m.Update(ctx, makeBundle(t, "plug-dr", "1.1.0", 1)); err != nil {
		t.Fatalf("Update: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 40*time.Millisecond {
		t.Errorf("Update returned before drain: %v", elapsed)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("Update exceeded drain timeout: %v", elapsed)
	}
	if dt.Snapshot("plug-dr", "1.0.0") != 0 {
		t.Errorf("drain not zero")
	}
}
