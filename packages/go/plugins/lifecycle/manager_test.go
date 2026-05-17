package lifecycle

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
)

// newManagerForTest assembles a Manager wired to fresh in-memory
// storage + a fresh audit MemoryStore. Returning all three lets tests
// assert on storage state and audit events.
func newManagerForTest(t *testing.T, opts ...ManagerOption) (*Manager, *MemoryStorage, *audit.MemoryStore) {
	t.Helper()
	storage := newMemForTest(t)
	auditStore := audit.NewMemoryStore()
	auditStore.NowFunc = func() time.Time { return fixedTime }
	emitter := audit.NewEmitter(auditStore)

	// Pin Manager.now so audit metadata timestamps are deterministic.
	allOpts := append([]ManagerOption{WithNowFunc(func() time.Time { return fixedTime })}, opts...)

	mgr := NewManager(storage, emitter, allOpts...)
	return mgr, storage, auditStore
}

// buildBundle writes a minimal .gnplugin ZIP containing only the
// supplied manifest JSON. It's the smallest valid bundle the lifecycle
// Manager will accept today (issue #44 adds more).
func buildBundle(t *testing.T, manifest string) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("manifest.json")
	if err != nil {
		t.Fatalf("zip Create: %v", err)
	}
	if _, err := w.Write([]byte(manifest)); err != nil {
		t.Fatalf("zip Write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip Close: %v", err)
	}
	return &buf
}

// validManifestJSON is the canonical manifest fixture. Tests that need
// to vary one field reach into manifestWith(...).
const validManifestJSON = `{
  "slug": "gn-seo",
  "version": "1.0.0",
  "abi_version": 1,
  "capabilities": {"kv": true, "audit.emit": true}
}`

// manifestWith returns validManifestJSON with selected key/value pairs
// replaced. Used to build invalid manifests for negative tests.
func manifestWith(replacements map[string]string) string {
	out := validManifestJSON
	for k, v := range replacements {
		old := fmt.Sprintf(`"%s":`, k)
		idx := strings.Index(out, old)
		if idx == -1 {
			panic("manifestWith: key not in fixture: " + k)
		}
		// Replace from old up to the next comma or closing brace.
		rest := out[idx+len(old):]
		end := strings.IndexAny(rest, ",}")
		out = out[:idx] + fmt.Sprintf(`"%s": %s`, k, v) + rest[end:]
	}
	return out
}

// recordingRuntime captures calls so tests can assert that Load/Unload
// fired in the right order and against the right slug.
type recordingRuntime struct {
	mu        sync.Mutex
	loaded    []string
	unloaded  []string
	loadErr   error
	unloadErr error

	// loadHook fires on every Load BEFORE the error short-circuit, so
	// tests can synchronize concurrent goroutines.
	loadHook func(slug string)
}

func (r *recordingRuntime) Load(_ context.Context, p Plugin) error {
	r.mu.Lock()
	r.loaded = append(r.loaded, p.Slug)
	r.mu.Unlock()
	if r.loadHook != nil {
		r.loadHook(p.Slug)
	}
	return r.loadErr
}

func (r *recordingRuntime) Unload(_ context.Context, slug string) error {
	r.mu.Lock()
	r.unloaded = append(r.unloaded, slug)
	r.mu.Unlock()
	return r.unloadErr
}

// recordingMigrator captures MigrateUp / MigrateDown calls and lets
// tests inject errors.
type recordingMigrator struct {
	mu      sync.Mutex
	ups     []string
	downs   []string
	upErr   error
	downErr error
}

func (m *recordingMigrator) MigrateUp(_ context.Context, p Plugin) error {
	m.mu.Lock()
	m.ups = append(m.ups, p.Slug)
	m.mu.Unlock()
	return m.upErr
}

func (m *recordingMigrator) MigrateDown(_ context.Context, slug string) error {
	m.mu.Lock()
	m.downs = append(m.downs, slug)
	m.mu.Unlock()
	return m.downErr
}

// assertAuditEvent finds the most recent event with the given type and
// asserts it's present. Returns the event so callers can check metadata.
func assertAuditEvent(t *testing.T, store *audit.MemoryStore, eventType string) audit.Event {
	t.Helper()
	events, err := store.List(context.Background(), audit.Filter{EventType: eventType})
	if err != nil {
		t.Fatalf("audit list: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("no audit event of type %q", eventType)
	}
	return events[0]
}

func assertNoAuditEvent(t *testing.T, store *audit.MemoryStore, eventType string) {
	t.Helper()
	events, _ := store.List(context.Background(), audit.Filter{EventType: eventType})
	if len(events) > 0 {
		t.Fatalf("unexpected audit event %q: %+v", eventType, events[0])
	}
}

// ----- Install -----

func TestManager_Install_HappyPath(t *testing.T) {
	mgr, storage, auditStore := newManagerForTest(t)

	slug, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if slug != "gn-seo" {
		t.Errorf("slug: got %q want %q", slug, "gn-seo")
	}

	p, err := storage.Get(context.Background(), slug)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.State != StateInstalled {
		t.Errorf("state: got %q want %q", p.State, StateInstalled)
	}
	if p.Version != "1.0.0" {
		t.Errorf("version: got %q", p.Version)
	}
	if p.ABIVersion != 1 {
		t.Errorf("abi: got %d", p.ABIVersion)
	}
	// Capabilities are extracted, sorted.
	wantCaps := []string{"audit.emit", "kv"}
	if len(p.Capabilities) != len(wantCaps) {
		t.Errorf("caps len: got %d", len(p.Capabilities))
	}
	for i, c := range wantCaps {
		if p.Capabilities[i] != c {
			t.Errorf("caps[%d]: got %q want %q", i, p.Capabilities[i], c)
		}
	}

	ev := assertAuditEvent(t, auditStore, "plugin.installed")
	if ev.ActorPluginSlug != slug {
		t.Errorf("audit plugin slug: got %q want %q", ev.ActorPluginSlug, slug)
	}
	if ev.ResourceType != "plugin" || ev.ResourceID != slug {
		t.Errorf("audit target: got %s/%s", ev.ResourceType, ev.ResourceID)
	}
}

func TestManager_Install_RejectsInvalidSlug(t *testing.T) {
	mgr, _, auditStore := newManagerForTest(t)
	bad := manifestWith(map[string]string{"slug": `"BAD_SLUG"`})
	_, err := mgr.Install(context.Background(), buildBundle(t, bad))
	if err == nil {
		t.Fatal("expected error for invalid slug")
	}
	assertNoAuditEvent(t, auditStore, "plugin.installed")
}

func TestManager_Install_RejectsMissingVersion(t *testing.T) {
	mgr, _, _ := newManagerForTest(t)
	bad := manifestWith(map[string]string{"version": `""`})
	_, err := mgr.Install(context.Background(), buildBundle(t, bad))
	if err == nil {
		t.Fatal("expected error for missing version")
	}
}

func TestManager_Install_RejectsBadABI(t *testing.T) {
	mgr, _, _ := newManagerForTest(t)
	bad := manifestWith(map[string]string{"abi_version": `0`})
	_, err := mgr.Install(context.Background(), buildBundle(t, bad))
	if err == nil {
		t.Fatal("expected error for abi 0")
	}
}

func TestManager_Install_RejectsBundleWithoutManifest(t *testing.T) {
	mgr, _, _ := newManagerForTest(t)

	// ZIP with no manifest.json.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	_, _ = zw.Create("something-else.json")
	_ = zw.Close()

	_, err := mgr.Install(context.Background(), &buf)
	if err == nil {
		t.Fatal("expected error for missing manifest.json")
	}
}

func TestManager_Install_RejectsNonZipBundle(t *testing.T) {
	mgr, _, _ := newManagerForTest(t)
	_, err := mgr.Install(context.Background(),
		strings.NewReader("not a zip"))
	if err == nil {
		t.Fatal("expected error for non-zip bundle")
	}
}

func TestManager_Install_RejectsNilReader(t *testing.T) {
	mgr, _, _ := newManagerForTest(t)
	_, err := mgr.Install(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil reader")
	}
}

func TestManager_Install_MigrationFailure_ParksErrored(t *testing.T) {
	mig := &recordingMigrator{upErr: errors.New("schema borked")}
	mgr, storage, auditStore := newManagerForTest(t, WithMigrator(mig))

	_, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON))
	if err == nil {
		t.Fatal("expected install error")
	}

	p, getErr := storage.Get(context.Background(), "gn-seo")
	if getErr != nil {
		t.Fatalf("Get: %v", getErr)
	}
	if p.State != StateErrored {
		t.Errorf("state: got %q want %q", p.State, StateErrored)
	}
	if !strings.Contains(p.LastError, "schema borked") {
		t.Errorf("LastError missing reason: %q", p.LastError)
	}

	// No plugin.installed audit event — only plugin.errored.
	assertNoAuditEvent(t, auditStore, "plugin.installed")
	assertAuditEvent(t, auditStore, "plugin.errored")
}

// ----- Activate -----

func TestManager_Activate_FromInstalled(t *testing.T) {
	rt := &recordingRuntime{}
	mgr, storage, auditStore := newManagerForTest(t, WithRuntime(rt))
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	p, _ := storage.Get(context.Background(), "gn-seo")
	if p.State != StateActive {
		t.Errorf("state: got %q want %q", p.State, StateActive)
	}
	if p.ActivatedAt.IsZero() {
		t.Error("ActivatedAt was not set")
	}
	if len(rt.loaded) != 1 || rt.loaded[0] != "gn-seo" {
		t.Errorf("runtime.Load not called: %v", rt.loaded)
	}

	assertAuditEvent(t, auditStore, "plugin.activated")
}

func TestManager_Activate_FromInactive(t *testing.T) {
	rt := &recordingRuntime{}
	mgr, storage, _ := newManagerForTest(t, WithRuntime(rt))
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Activate 1: %v", err)
	}
	if err := mgr.Deactivate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Activate 2: %v", err)
	}
	p, _ := storage.Get(context.Background(), "gn-seo")
	if p.State != StateActive {
		t.Errorf("state after re-activate: got %q want %q", p.State, StateActive)
	}
}

func TestManager_Activate_FromPendingUninstall_Rejected(t *testing.T) {
	mgr, storage, _ := newManagerForTest(t)
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	// Force the state to PendingUninstall.
	if err := storage.UpdateState(context.Background(), "gn-seo",
		StateInstalled, StatePendingUninstall, nil); err != nil {
		t.Fatalf("force PendingUninstall: %v", err)
	}

	err := mgr.Activate(context.Background(), "gn-seo")
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("got %v want ErrInvalidTransition", err)
	}
}

func TestManager_Activate_NotFound(t *testing.T) {
	mgr, _, _ := newManagerForTest(t)
	err := mgr.Activate(context.Background(), "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
}

func TestManager_Activate_RuntimeFailure_ParksErrored(t *testing.T) {
	rt := &recordingRuntime{loadErr: errors.New("wasm trap")}
	mgr, storage, auditStore := newManagerForTest(t, WithRuntime(rt))
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}

	err := mgr.Activate(context.Background(), "gn-seo")
	if err == nil || !strings.Contains(err.Error(), "wasm trap") {
		t.Fatalf("expected wasm trap error, got %v", err)
	}

	p, _ := storage.Get(context.Background(), "gn-seo")
	if p.State != StateErrored {
		t.Errorf("state: got %q want %q", p.State, StateErrored)
	}
	if !strings.Contains(p.LastError, "wasm trap") {
		t.Errorf("LastError: got %q", p.LastError)
	}
	assertAuditEvent(t, auditStore, "plugin.errored")
}

// TestManager_Activate_Concurrent verifies that under contention only
// one Activate wins; the rest see ErrInvalidTransition. This is the
// canonical race-the-state-machine assertion.
func TestManager_Activate_Concurrent(t *testing.T) {
	rt := &recordingRuntime{}
	mgr, storage, _ := newManagerForTest(t, WithRuntime(rt))
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}

	const goroutines = 64
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
			err := mgr.Activate(context.Background(), "gn-seo")
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
	if int(transErrs.Load()) != goroutines-1 {
		t.Fatalf("transErrs: got %d want %d", transErrs.Load(), goroutines-1)
	}

	p, _ := storage.Get(context.Background(), "gn-seo")
	if p.State != StateActive {
		t.Errorf("final state: got %q want %q", p.State, StateActive)
	}
	if p.RowVersion != 2 {
		// 1 (after Insert) + 1 (after one successful UpdateState).
		t.Errorf("RowVersion: got %d want 2", p.RowVersion)
	}
}

// ----- Deactivate -----

func TestManager_Deactivate_HappyPath(t *testing.T) {
	rt := &recordingRuntime{}
	mgr, storage, auditStore := newManagerForTest(t, WithRuntime(rt))
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	if err := mgr.Deactivate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	p, _ := storage.Get(context.Background(), "gn-seo")
	if p.State != StateInactive {
		t.Errorf("state: got %q want %q", p.State, StateInactive)
	}
	// ActivatedAt should still be set (we don't clear it on Deactivate).
	if p.ActivatedAt.IsZero() {
		t.Error("ActivatedAt cleared by Deactivate; should be preserved")
	}

	if len(rt.unloaded) != 1 {
		t.Errorf("runtime.Unload not called: %v", rt.unloaded)
	}
	assertAuditEvent(t, auditStore, "plugin.deactivated")
}

func TestManager_Deactivate_FromInstalled_Rejected(t *testing.T) {
	mgr, _, _ := newManagerForTest(t)
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	err := mgr.Deactivate(context.Background(), "gn-seo")
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("got %v want ErrInvalidTransition", err)
	}
}

func TestManager_Deactivate_NotFound(t *testing.T) {
	mgr, _, _ := newManagerForTest(t)
	err := mgr.Deactivate(context.Background(), "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
}

func TestManager_Deactivate_UnloadFailure_ParksErrored(t *testing.T) {
	rt := &recordingRuntime{unloadErr: errors.New("stuck module")}
	mgr, storage, auditStore := newManagerForTest(t, WithRuntime(rt))
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	// Activate using a runtime that doesn't fail Load — switch the
	// unload error in mid-flight.
	rt.unloadErr = nil
	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	rt.unloadErr = errors.New("stuck module")

	err := mgr.Deactivate(context.Background(), "gn-seo")
	if err == nil {
		t.Fatal("expected error")
	}
	p, _ := storage.Get(context.Background(), "gn-seo")
	if p.State != StateErrored {
		t.Errorf("state: got %q want %q", p.State, StateErrored)
	}
	assertAuditEvent(t, auditStore, "plugin.errored")
}

// ----- Uninstall -----

func TestManager_Uninstall_FromInactive(t *testing.T) {
	mig := &recordingMigrator{}
	rt := &recordingRuntime{}
	mgr, storage, auditStore := newManagerForTest(t, WithRuntime(rt), WithMigrator(mig))
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := mgr.Deactivate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	if err := mgr.Uninstall(context.Background(), "gn-seo", false); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	_, err := storage.Get(context.Background(), "gn-seo")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after uninstall, got %v", err)
	}

	// removeData=false → no MigrateDown call.
	if len(mig.downs) != 0 {
		t.Errorf("unexpected MigrateDown calls: %v", mig.downs)
	}

	assertAuditEvent(t, auditStore, "plugin.uninstalled")
}

func TestManager_Uninstall_RemoveDataRunsDownMigrations(t *testing.T) {
	mig := &recordingMigrator{}
	mgr, _, _ := newManagerForTest(t, WithMigrator(mig))
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := mgr.Deactivate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	if err := mgr.Uninstall(context.Background(), "gn-seo", true); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if len(mig.downs) != 1 || mig.downs[0] != "gn-seo" {
		t.Errorf("MigrateDown: got %v want [gn-seo]", mig.downs)
	}
}

func TestManager_Uninstall_FromErrored(t *testing.T) {
	rt := &recordingRuntime{loadErr: errors.New("bad wasm")}
	mgr, _, _ := newManagerForTest(t, WithRuntime(rt))
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	// Force into Errored via failing Activate.
	_ = mgr.Activate(context.Background(), "gn-seo")

	// Uninstall from Errored should succeed.
	if err := mgr.Uninstall(context.Background(), "gn-seo", false); err != nil {
		t.Fatalf("Uninstall from Errored: %v", err)
	}
}

func TestManager_Uninstall_FromActive_Rejected(t *testing.T) {
	mgr, _, _ := newManagerForTest(t)
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	err := mgr.Uninstall(context.Background(), "gn-seo", false)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("got %v want ErrInvalidTransition", err)
	}
}

func TestManager_Uninstall_FromInstalled_Rejected(t *testing.T) {
	mgr, _, _ := newManagerForTest(t)
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}

	err := mgr.Uninstall(context.Background(), "gn-seo", false)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("got %v want ErrInvalidTransition", err)
	}
}

func TestManager_Uninstall_UnloadErrorIsLoggedNotFatal(t *testing.T) {
	// During uninstall, an unload failure must NOT block the deletion.
	rt := &recordingRuntime{}
	mgr, _, _ := newManagerForTest(t, WithRuntime(rt))
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := mgr.Deactivate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	rt.unloadErr = errors.New("module already gone")

	if err := mgr.Uninstall(context.Background(), "gn-seo", false); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
}

func TestManager_Uninstall_DownMigrationErrorIsLoggedNotFatal(t *testing.T) {
	mig := &recordingMigrator{downErr: errors.New("migration sad")}
	mgr, storage, _ := newManagerForTest(t, WithMigrator(mig))
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := mgr.Deactivate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	if err := mgr.Uninstall(context.Background(), "gn-seo", true); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	// Row should be gone even though MigrateDown failed.
	if _, err := storage.Get(context.Background(), "gn-seo"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ----- Reset -----

func TestManager_Reset_FromErrored(t *testing.T) {
	rt := &recordingRuntime{loadErr: errors.New("bad wasm")}
	mgr, storage, auditStore := newManagerForTest(t, WithRuntime(rt))
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	_ = mgr.Activate(context.Background(), "gn-seo")

	// Confirm we're in Errored.
	p, _ := storage.Get(context.Background(), "gn-seo")
	if p.State != StateErrored {
		t.Fatalf("setup: state got %q want %q", p.State, StateErrored)
	}
	if p.LastError == "" {
		t.Fatal("setup: LastError empty")
	}

	if err := mgr.Reset(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	p, _ = storage.Get(context.Background(), "gn-seo")
	if p.State != StateInactive {
		t.Errorf("state: got %q want %q", p.State, StateInactive)
	}
	if p.LastError != "" {
		t.Errorf("LastError not cleared: %q", p.LastError)
	}
	if !p.ErrorAt.IsZero() {
		t.Errorf("ErrorAt not cleared: %v", p.ErrorAt)
	}

	assertAuditEvent(t, auditStore, "plugin.reset")
}

func TestManager_Reset_FromActive_Rejected(t *testing.T) {
	mgr, _, _ := newManagerForTest(t)
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	err := mgr.Reset(context.Background(), "gn-seo")
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("got %v want ErrInvalidTransition", err)
	}
}

func TestManager_Reset_NotFound(t *testing.T) {
	mgr, _, _ := newManagerForTest(t)
	err := mgr.Reset(context.Background(), "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
}

// ----- Errored recovery flow (end-to-end) -----

// TestManager_ErroredRecoveryFlow walks an install → fail-activate →
// reset → activate path to verify that the recovery transitions are
// composable and the audit chain is intact.
func TestManager_ErroredRecoveryFlow(t *testing.T) {
	rt := &recordingRuntime{loadErr: errors.New("bad wasm")}
	mgr, storage, auditStore := newManagerForTest(t, WithRuntime(rt))

	// 1. Install
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// 2. Activate fails → Errored
	_ = mgr.Activate(context.Background(), "gn-seo")
	p, _ := storage.Get(context.Background(), "gn-seo")
	if p.State != StateErrored {
		t.Fatalf("expected Errored, got %q", p.State)
	}

	// 3. Operator fixes whatever was broken.
	rt.loadErr = nil

	// 4. Reset → Inactive
	if err := mgr.Reset(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	// 5. Activate again → Active
	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Activate retry: %v", err)
	}
	p, _ = storage.Get(context.Background(), "gn-seo")
	if p.State != StateActive {
		t.Fatalf("expected Active, got %q", p.State)
	}

	// Audit chain: installed, errored, reset, activated.
	all, _ := auditStore.List(context.Background(), audit.Filter{})
	got := make([]string, 0, len(all))
	for _, e := range all {
		got = append(got, e.EventType)
	}
	want := map[string]bool{
		"plugin.installed": false,
		"plugin.errored":   false,
		"plugin.reset":     false,
		"plugin.activated": false,
	}
	for _, e := range got {
		if _, ok := want[e]; ok {
			want[e] = true
		}
	}
	for e, seen := range want {
		if !seen {
			t.Errorf("audit event %q never emitted; saw %v", e, got)
		}
	}
}

// ----- Get / List -----

func TestManager_Get_And_List(t *testing.T) {
	mgr, _, _ := newManagerForTest(t)

	for _, slug := range []string{"gn-a", "gn-b", "gn-c"} {
		m := strings.ReplaceAll(validManifestJSON, "gn-seo", slug)
		if _, err := mgr.Install(context.Background(), buildBundle(t, m)); err != nil {
			t.Fatalf("Install %q: %v", slug, err)
		}
	}

	got, err := mgr.Get(context.Background(), "gn-b")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Slug != "gn-b" {
		t.Errorf("Get slug: got %q want %q", got.Slug, "gn-b")
	}

	list, err := mgr.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("List len: got %d want 3", len(list))
	}
}

// ----- Wiring sanity -----

func TestNewManager_PanicsOnNilStorage(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	NewManager(nil, audit.NewEmitter(audit.NewMemoryStore()))
}

func TestNewManager_PanicsOnNilEmitter(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	NewManager(NewMemoryStorage(), nil)
}

func TestNewManager_AppliesDefaults(t *testing.T) {
	mgr := NewManager(NewMemoryStorage(), audit.NewEmitter(audit.NewMemoryStore()))
	// Just exercise the smoke path so we know the defaults work.
	if mgr.runtime == nil {
		t.Error("runtime is nil; expected NoopRuntime default")
	}
	if mgr.migrator == nil {
		t.Error("migrator is nil; expected NoopMigrator default")
	}
	if mgr.logger == nil {
		t.Error("logger is nil")
	}
}

func TestNewManager_OptionsIgnoreNil(t *testing.T) {
	mgr := NewManager(NewMemoryStorage(), audit.NewEmitter(audit.NewMemoryStore()),
		WithRuntime(nil), WithMigrator(nil), WithLogger(nil), WithNowFunc(nil))
	if mgr.runtime == nil || mgr.migrator == nil || mgr.logger == nil || mgr.now == nil {
		t.Error("nil options should be ignored, defaults retained")
	}
}

func TestNoopRuntimeAndMigrator(t *testing.T) {
	if err := (NoopRuntime{}).Load(context.Background(), Plugin{}); err != nil {
		t.Errorf("NoopRuntime.Load: %v", err)
	}
	if err := (NoopRuntime{}).Unload(context.Background(), "x"); err != nil {
		t.Errorf("NoopRuntime.Unload: %v", err)
	}
	if err := (NoopMigrator{}).MigrateUp(context.Background(), Plugin{}); err != nil {
		t.Errorf("NoopMigrator.MigrateUp: %v", err)
	}
	if err := (NoopMigrator{}).MigrateDown(context.Background(), "x"); err != nil {
		t.Errorf("NoopMigrator.MigrateDown: %v", err)
	}
}

func TestStateValid(t *testing.T) {
	for _, s := range []State{
		StateInstalled, StateActive, StateInactive,
		StatePendingUninstall, StateErrored,
	} {
		if !s.Valid() {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []State{"", "weird", "ACTIVE"} {
		if s.Valid() {
			t.Errorf("%q should NOT be valid", s)
		}
	}
}

func TestTransitionError_FormatsActualState(t *testing.T) {
	err := transitionError("gn-x", "Activate", StateInstalled, StateActive)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatal("not wrapped with ErrInvalidTransition")
	}
	if !strings.Contains(err.Error(), `"active"`) {
		t.Errorf("missing actual state in message: %v", err)
	}
}

func TestTransitionError_NoActualState(t *testing.T) {
	err := transitionError("gn-x", "UpdateState", StateActive, "")
	if !strings.Contains(err.Error(), "current state") {
		t.Errorf("missing fallback message: %v", err)
	}
}
